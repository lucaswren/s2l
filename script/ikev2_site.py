#!/usr/bin/env python3
"""Render IKEv2 profiles and attach SOCKS5 routes on an isolated test server."""

import argparse
import ipaddress
import json
from pathlib import Path
import re
import subprocess
import sys

WAN_TABLE_BASE = 20000


def ipv4_address(value):
    addr = ipaddress.IPv4Address(value)
    if addr.is_unspecified or addr.is_multicast or addr.is_loopback:
        raise ValueError("gateways must be unicast IPv4 addresses")
    return addr


def ipv4_subnet(value):
    net = ipaddress.IPv4Network(value)
    if net.prefixlen == 0 or net.overlaps(ipaddress.IPv4Network("224.0.0.0/4")):
        raise ValueError("lab traffic selectors must specify a unicast IPv4 subnet")
    return net


def render_profiles(config):
    """Return responder and initiator configs. PSKs are provisioned separately."""
    gateway = ipv4_address(config["gateway"])
    local = ipv4_subnet(config["local_subnet"])
    sites = config["sites"]
    if not isinstance(sites, list) or not 1 <= len(sites) <= 16:
        raise ValueError("configure 1 to 16 sites")

    responder = ["config setup\n"]
    initiators = {}
    subnets = [local]
    gateways = {gateway}
    reqids = set()
    for site in sites:
        name = site["name"]
        if not isinstance(name, str) or not re.fullmatch(r"[a-z][a-z0-9-]{0,30}", name) or name in initiators:
            raise ValueError("site names must be unique lowercase identifiers")
        peer = ipv4_address(site["gateway"])
        remote = ipv4_subnet(site["subnet"])
        reqid = site["reqid"]
        if type(reqid) is not int or not 1 <= reqid <= 16383 or reqid in reqids:
            raise ValueError("site reqids must be unique integers from 1 to 16383")
        if peer in gateways:
            raise ValueError("lab sites require distinct gateway addresses")
        if any(remote.overlaps(net) for net in subnets):
            raise ValueError("site traffic selectors overlap")
        subnets.append(remote)
        gateways.add(peer)
        reqids.add(reqid)

        responder.append(render_connection(name, gateway, local, peer, remote, reqid))
        initiators[name] = "config setup\n" + render_connection(name, peer, remote, gateway, local, reqid)

    if any(addr in net for addr in gateways for net in subnets):
        raise ValueError("gateway addresses must be outside the protected subnets")
    return {"responder": "".join(responder), "initiators": initiators}


def render_connection(name, left, local, right, remote, reqid):
    return f"""
conn s2l-site-{name}
    keyexchange=ikev2
    type=tunnel
    authby=psk
    left={left}
    leftid={left}
    leftsubnet={local}
    right={right}
    rightid={right}
    rightsubnet={remote}
    ike=aes256-sha256-modp2048!
    esp=aes256-sha256!
    reqid={reqid}
    dpdaction=clear
    auto=add
"""


def bindings(config, state, allow_offline=False):
    render_profiles(config)
    mappings = state["mappings"]
    result = []
    for site in config["sites"]:
        subnet = ipv4_subnet(site["subnet"])
        if any(subnet.overlaps(ipv4_subnet(m["subnet"])) for m in mappings):
            raise ValueError("site overlaps a PPP subnet")
        mode = site.get("egress", "socks5")
        if mode == "wan":
            dev = site.get("wan_interface", "")
            if (not isinstance(dev, str) or not re.fullmatch(r"[A-Za-z0-9_.][A-Za-z0-9_.-]{0,14}", dev)
                    or dev in (".", "..", "lo") or dev.startswith(("ppp", "tun", "xfrm"))
                    or any(dev == m["tun_name"] for m in mappings)):
                raise ValueError("WAN requires a dedicated uplink interface")
            result.append((str(subnet), WAN_TABLE_BASE + site["reqid"], dev, site["reqid"]))
            continue
        if mode != "socks5":
            raise ValueError("egress must be socks5 or wan")
        matches = [m for m in mappings if m["id"] == site.get("mapping_id")]
        if len(matches) != 1 or (not allow_offline and matches[0]["status"] != "running"):
            raise ValueError("each site needs an existing running S2L mapping")
        mapping = matches[0]
        table, tun = mapping["table_id"], mapping["tun_name"]
        if type(table) is not int or not 1 <= table <= 252 or not re.fullmatch(r"[A-Za-z0-9_.-]{1,15}", tun):
            raise ValueError("invalid S2L table or TUN")
        if tun in (".", "..") or tun.startswith("-") or tun.startswith("ppp"):
            raise ValueError("invalid S2L TUN")
        if any(table == b[1] or tun == b[2] for b in result):
            raise ValueError("sites require separate S2L tables and TUN interfaces")
        result.append((str(subnet), table, tun, site["reqid"]))
    return result


def wan_nat_payload(sites):
    lines = ["*nat", ":S2L-SITE-NAT - [0:0]", "-I POSTROUTING 1 -j S2L-SITE-NAT"]
    for subnet, table, dev, _ in sites:
        if table >= WAN_TABLE_BASE:
            lines.append(f"-A S2L-SITE-NAT -s {subnet} -o {dev} -j MASQUERADE")
    return "\n".join(lines + ["COMMIT", ""])


def wan_next_hop(defaults, dev):
    choices = [route for route in defaults if route.get("dev") == dev
               and route.get("gateway") and not route.get("nexthops")]
    return str(ipv4_address(choices[0]["gateway"])) if len(choices) == 1 else None


def firewall_payload(sites, admit, config=None):
    lines = ["*filter"]
    for chain in ("INPUT", "FORWARD", "OUTPUT"):
        lines.append(f":S2L-SITE-{chain} - [0:0]")
    for chain in ("INPUT", "FORWARD", "OUTPUT"):
        lines.append(f"-F S2L-SITE-{chain}")
        if not admit:
            lines.append(f"-I {chain} 1 -j S2L-SITE-{chain}")
    if admit and config is not None:
        for site in config["sites"]:
            peer, local = ipv4_address(site["gateway"]), ipv4_address(config["gateway"])
            for protocol in ("udp -m multiport --dports 500,4500", "esp"):
                lines.append(f"-A S2L-SITE-INPUT -s {peer} -d {local} -p {protocol} -j ACCEPT")
    for source, _, _, _ in sites:
        for destination, _, _, _ in sites:
            lines.append(f"-A S2L-SITE-FORWARD -s {source} -d {destination} -j DROP")
    for subnet, _, tun, reqid in sites:
        incoming = f"-m policy --dir in --pol ipsec --strict --mode tunnel --reqid {reqid}"
        outgoing = f"-m policy --dir out --pol ipsec --strict --mode tunnel --reqid {reqid}"
        if admit:
            lines += [
                f"-A S2L-SITE-FORWARD -s {subnet} -o {tun} {incoming} -j ACCEPT",
                f"-A S2L-SITE-FORWARD -d {subnet} -i {tun} {outgoing} -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT",
            ]
        lines += [
            f"-A S2L-SITE-INPUT -s {subnet} -j DROP",
            f"-A S2L-SITE-FORWARD -s {subnet} -j DROP",
            f"-A S2L-SITE-FORWARD -d {subnet} -j DROP",
            f"-A S2L-SITE-OUTPUT -d {subnet} -j DROP",
        ]
    return "\n".join(lines + ["COMMIT", ""])


def attach(config, state, allow_offline=False):
    """Attach once, before starting the lab's responder. Fail closed on partial setup."""
    import fcntl

    sites = bindings(config, state, allow_offline)

    def run(*args, **kwargs):
        return subprocess.run(args, check=True, text=True, capture_output=True, timeout=15, **kwargs).stdout

    with open("/run/s2l-ikev2-lab.lock", "a", encoding="utf-8") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        filter_rules = run("iptables", "-w", "5", "-S")
        has_wan = any(row[1] >= WAN_TABLE_BASE for row in sites)
        nat_rules = run("iptables", "-w", "5", "-t", "nat", "-S") if has_wan else ""
        if "S2L-SITE-" in filter_rules or "S2L-SITE-NAT" in nat_rules:
            raise ValueError("lab guard already exists; rebuild the isolated test environment")
        rules = json.loads(run("ip", "-j", "-4", "rule", "show"))
        used = {r.get("priority") for r in rules}
        if any(0 < priority <= 8700 for priority in used if isinstance(priority, int)):
            raise ValueError("higher-priority policy routing exists")
        wan_routes = {}
        if has_wan:
            tables = json.loads(run("ip", "-j", "-N", "-4", "route", "show", "table", "all"))
            defaults = json.loads(run("ip", "-j", "-4", "route", "show", "table", "main", "default"))
            for _, table, dev, _ in sites:
                if table < WAN_TABLE_BASE:
                    continue
                if any(str(route.get("table")) == str(table) for route in tables):
                    raise ValueError("WAN route table is already in use")
                gateway = wan_next_hop(defaults, dev)
                if gateway:
                    wan_routes[table] = ["via", gateway, "dev", dev, "onlink"]
                elif not allow_offline:
                    raise ValueError("WAN interface needs one IPv4 default gateway")
        if not allow_offline:
            for _, table, tun, _ in sites:
                if table >= WAN_TABLE_BASE:
                    continue
                routes = json.loads(run("ip", "-j", "-4", "route", "show", "table", str(table)))
                if not any(r.get("dst") == "default" and r.get("dev") == tun for r in routes):
                    raise ValueError("S2L mapping has no active TUN route")
        run("iptables-restore", "--wait", "5", "--noflush", input=firewall_payload(sites, False))
        for index, (subnet, table, _, _) in enumerate(sites):
            priority = 8600 + index * 2 if table >= WAN_TABLE_BASE else 8000 + table * 2
            run("ip", "-4", "rule", "add", "pref", str(priority + 1), "from", subnet, "unreachable")
            run("ip", "-4", "rule", "add", "pref", str(priority), "from", subnet, "lookup", str(table))
            if table in wan_routes:
                run("ip", "-4", "route", "add", "default", *wan_routes[table], "table", str(table))
        if has_wan:
            run("iptables-restore", "--wait", "5", "--noflush", input=wan_nat_payload(sites))
        run("iptables-restore", "--wait", "5", "--noflush", input=firewall_payload(sites, True, config))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("config", type=Path)
    parser.add_argument("--attach-lab-state", type=Path, metavar="STATE_JSON",
                        help="attach to running mappings on an isolated Linux test server")
    args = parser.parse_args()
    try:
        config = json.loads(args.config.read_text(encoding="utf-8"))
        if args.attach_lab_state:
            attach(config, json.loads(args.attach_lab_state.read_text(encoding="utf-8")))
            print("Lab policy attached. Start the isolated IKEv2 responder next.")
            return
        profiles = render_profiles(config)
    except (OSError, KeyError, TypeError, ValueError, subprocess.SubprocessError) as err:
        parser.exit(2, f"Invalid lab configuration ({type(err).__name__}).\n")
    json.dump(profiles, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
