"""Exercise update failures and file preservation in a private mount namespace.
Run only as root through unshare --mount --net --propagation private.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    if os.geteuid() != 0 or any(os.readlink(f"/proc/self/ns/{kind}") == os.readlink(f"/proc/1/ns/{kind}") for kind in ("mnt", "net")):
        raise SystemExit("requires a private root mount namespace")
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True)
    original_root_mode = Path("/").stat().st_mode
    umount = shutil.which("umount")
    if not umount:
        raise SystemExit("requires umount")
    script = Path(__file__).resolve().parent / "update.sh"
    with tempfile.TemporaryDirectory(prefix="s2l-update-test-") as tmp:
        root = Path(tmp)
        mounted = []
        try:
            for target in ("/opt", "/root", "/etc", "/run", "/usr/local/bin"):
                source = root / target.lstrip("/")
                source.mkdir(parents=True, exist_ok=True)
                subprocess.run(["mount", "--bind", str(source), target], check=True)
                mounted.append(target)
            for directory in ("/etc/ppp", "/etc/xl2tpd", "/etc/systemd/system"):
                Path(directory).mkdir(parents=True)
            tools = root / "tools"
            tools.mkdir()
            mocks = {
                "go": r'''[[ "${FAIL_BUILD:-0}" != 1 ]] || exit 1
for ((i=1; i<=$#; i++)); do if [[ "${!i}" == -o ]]; then j=$((i+1)); target="${!j}"; break; fi; done
cat > "$target" <<'BINARY'
#!/bin/bash
echo guard-only >> "$S2L_TEST_LOG"
exit 0
BINARY
chmod 755 "$target"''',
                "systemctl": r'''echo "$*" >> "$S2L_TEST_LOG"
case "$1" in
is-active) [[ -f "$S2L_TEST_STATE/${@: -1}" ]] ;;
stop) shift; for name in "$@"; do rm -f "$S2L_TEST_STATE/$name"; done ;;
start|restart) shift; for name in "$@"; do touch "$S2L_TEST_STATE/$name"; done ;;
*) exit 0 ;;
esac''',
                "install": r'''if [[ "${FAIL_INSTALL:-0}" == 1 && "${@: -1}" == /etc/systemd/system/s2l.service ]]; then exit 1; fi
exec /usr/bin/install "$@"''',
                "iptables": "exit 0", "ip6tables": "exit 0",
                "iptables-save": "echo fixture", "ip6tables-save": "echo fixture",
            }
            for name, body in mocks.items():
                path = tools / name
                path.write_text("#!/bin/bash\nset -e\n" + body + "\n")
                path.chmod(0o755)
            state = root / "services"
            state.mkdir()
            log = root / "commands.log"
            env = dict(os.environ, PATH=f"{tools}:/usr/bin:/bin", S2L_TEST_STATE=str(state), S2L_TEST_LOG=str(log))
            for scenario in ("build_failure", "success_stopped", "install_failure"):
                units = Path("/etc/systemd/system")
                for child in units.iterdir():
                    if child.is_dir():
                        shutil.rmtree(child)
                    else:
                        child.unlink()
                app = Path("/opt/s2l")
                if app.exists():
                    shutil.rmtree(app)
                app.mkdir()
                (app / "data").mkdir()
                original = b'{"listen":"127.0.0.1:8080","admin_user":"admin","admin_pass":"unchanged-password"}'
                (app / "config.json").write_bytes(original)
                (app / "data/state.json").write_text('{"nodes":[],"mappings":[]}')
                binary = app / "s2l"
                binary.write_text("#!/bin/sh\nexit 0\n")
                binary.chmod(0o755)
                old_binary = binary.read_bytes()
                Path("/usr/local/bin/s2l").write_text("original-cli")
                Path("/etc/systemd/system/s2l.service").write_text("original-unit")
                for item in state.iterdir():
                    item.unlink()
                log.write_text("")
                case_env = env.copy()
                if scenario == "build_failure":
                    case_env["FAIL_BUILD"] = "1"
                elif scenario == "install_failure":
                    case_env["FAIL_INSTALL"] = "1"
                    (state / "s2l").touch()
                    (state / "xl2tpd").touch()
                result = subprocess.run(["bash", str(script)], env=case_env, capture_output=True, text=True)
                assert (result.returncode == 0) == (scenario == "success_stopped"), result.stdout + result.stderr
                assert (app / "config.json").read_bytes() == original
                assert json.loads((app / "data/state.json").read_text()) == {"nodes": [], "mappings": []}
                if scenario == "build_failure":
                    assert "stop" not in log.read_text(), "build failure stopped live services"
                    assert binary.read_bytes() == old_binary
                elif scenario == "success_stopped":
                    assert log.read_text().index("guard-only") < log.read_text().index("stop xl2tpd"), "PPP was stopped before guards were installed"
                    assert binary.read_bytes() != old_binary
                    assert not (state / "s2l").exists(), "stopped service was started"
                    assert not (state / "xl2tpd").exists()
                    backups = list(Path("/root").glob("s2l-backup.*"))
                    assert backups and all(p.stat().st_mode & 0o077 == 0 for p in backups)
                else:
                    assert binary.read_bytes() == old_binary, "failed install did not restore old binary"
                    assert Path("/etc/systemd/system/s2l.service").read_text() == "original-unit"
                    assert not (state / "s2l").exists() and not (state / "xl2tpd").exists()
                    assert Path("/etc/systemd/system/xl2tpd.service.d/s2l.conf").exists(), "rollback removed boot guard"
                assert Path("/").stat().st_mode == original_root_mode, "upgrade changed root directory permissions"
                print(f"PASS: {scenario}")
        finally:
            for target in reversed(mounted):
                subprocess.run([umount, target], check=True)


if __name__ == "__main__":
    main()
