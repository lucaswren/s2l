"""Persist and restore S2L's opt-in IKEv2 site profiles."""

import fcntl
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

import ikev2_site
import manage_ipsec as ipsec
from manage_config import write_atomic

SAVED = ipsec.SITE_STATE
ACTIVE = ipsec.SITE_ACTIVE
BOOT_ID = Path("/proc/sys/kernel/random/boot_id")


def load(path):
    return json.loads(path.read_text(encoding="utf-8"))


def load_mappings(path):
    try:
        return load(path)
    except FileNotFoundError:
        # A fresh S2L installation does not write state until its first edit.
        return {"mappings": []}


def status():
    data = SAVED.read_bytes() if SAVED.exists() else b""
    config = json.loads(data) if data else {"enabled": False, "sites": []}
    config.pop("saved_boot_id", None)
    config = dict(config, sites=[
        {key: value for key, value in site.items() if key != "psk"}
        for site in config["sites"]
    ])
    config["active"] = ACTIVE.exists() and ipsec.active(ipsec.UNIT)
    config["pending"] = (ACTIVE.read_text() != hashlib.sha256(data).hexdigest()
                         if ACTIVE.exists() else config["enabled"])
    return config


def save(request, state):
    enabled = request.get("enabled")
    if type(enabled) is not bool:
        raise ValueError("enabled must be a boolean")
    if not enabled:
        config = {"enabled": False, "sites": []}
    else:
        if ipsec.read_state().get("enabled"):
            raise ValueError("Disable L2TP/IPsec before scheduling IKEv2")
        if not isinstance(request.get("sites"), list) or not 1 <= len(request["sites"]) <= 16:
            raise ValueError("Configure 1 to 16 sites")
        config = {key: request[key] for key in ("enabled", "gateway", "local_subnet")}
        previous = {site["name"]: site for site in (load(SAVED)["sites"] if SAVED.exists() else [])}
        config["sites"] = []
        for raw in request["sites"]:
            site = {key: raw[key] for key in ("name", "gateway", "subnet", "reqid")}
            mode = raw.get("egress", "socks5")
            if mode == "wan":
                site.update(egress=mode, wan_interface=raw.get("wan_interface"))
            elif mode == "socks5":
                site["mapping_id"] = raw.get("mapping_id")
            else:
                raise ValueError("Invalid site egress")
            old = previous.get(site["name"], {})
            site["psk"] = raw.get("psk") or (old.get("psk") if old.get("gateway") == site["gateway"] else "")
            if not isinstance(site["psk"], str) or not re.fullmatch(r"[A-Za-z0-9_.@+-]{24,128}", site["psk"]):
                raise ValueError("Each site needs a 24–128 character PSK")
            config["sites"].append(site)
        if len({site["psk"] for site in config["sites"]}) != len(config["sites"]):
            raise ValueError("Use a different PSK for each site")
        sites = ikev2_site.bindings(config, state)
        if any(table >= ikev2_site.WAN_TABLE_BASE for _, table, _, _ in sites):
            defaults = json.loads(ipsec.run("ip", "-j", "-4", "route", "show", "table", "main", "default").stdout)
            for _, table, dev, _ in sites:
                if table >= ikev2_site.WAN_TABLE_BASE and not ikev2_site.wan_next_hop(defaults, dev):
                    raise ValueError("WAN interface needs one IPv4 default gateway")
    config["saved_boot_id"] = BOOT_ID.read_text().strip()
    write_atomic(SAVED, (json.dumps(config) + "\n").encode())
    return status()


def restore(state_path):
    if ACTIVE.exists():
        # The kernel rules survive application restarts within the same boot.
        ipsec.run("systemctl", "start", ipsec.UNIT)
        return
    if not SAVED.exists():
        return
    config = load(SAVED)
    if not config["enabled"]:
        return
    if config.get("saved_boot_id") == BOOT_ID.read_text().strip():
        return
    if not ipsec.ready():
        raise RuntimeError("IPsec dependencies unavailable")
    if ipsec.read_state().get("enabled") or ipsec.active(ipsec.UNIT):
        raise ValueError("An IPsec service is already active")
    ipsec.check_conflicts()
    profiles = ikev2_site.render_profiles(config)
    ikev2_site.attach(config, load_mappings(state_path), allow_offline=True)
    ipsec.prepare_apparmor()
    write_atomic(ipsec.CONF, profiles["responder"].encode())
    secrets = "".join(f'{config["gateway"]} {site["gateway"]} : PSK "{site["psk"]}"\n' for site in config["sites"])
    write_atomic(ipsec.SECRET, secrets.encode())
    content = ipsec.SYSTEM_SECRET.read_text() if ipsec.SYSTEM_SECRET.exists() else ""
    if ipsec.INCLUDE not in content.splitlines():
        write_atomic(ipsec.SYSTEM_SECRET, (content.rstrip() + "\n" + ipsec.INCLUDE + "\n").encode())
    write_atomic(ACTIVE, hashlib.sha256(SAVED.read_bytes()).hexdigest().encode())
    ipsec.run("systemctl", "start", ipsec.UNIT)


def main():
    mode = sys.argv[1]
    if mode == "status":
        print(json.dumps(status()))
        return
    with open(ipsec.ROOT / "ipsec.lock", "a", encoding="utf-8") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if mode == "save":
            print(json.dumps(save(json.load(sys.stdin), load_mappings(Path(sys.argv[2])))))
        elif mode == "restore":
            restore(Path(sys.argv[2]))
        else:
            raise ValueError("Invalid IKEv2 operation")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, RuntimeError, subprocess.SubprocessError):
        sys.exit("IKEv2 operation failed. Check site settings, mapping state and IPsec service conflicts.")
