"""Exercise update failures and file preservation in a private mount namespace.
Run only as root through unshare --mount --net --propagation private.
"""
import json
import os
from pathlib import Path
import shutil
import shlex
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
    go_root = Path(os.environ.get('S2L_TEST_GOROOT') or subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    repository = Path(__file__).resolve().parents[1]
    if not (repository / "script/update.sh").is_file():
        raise SystemExit("missing update script in source checkout")
    with tempfile.TemporaryDirectory(prefix="s2l-update-test-") as tmp:
        root = Path(tmp)
        # /root, /opt and /etc are deliberately hidden below. A checkout in
        # one of those paths must remain readable by the upgrade subprocess.
        checkout = root / "source"
        shutil.copytree(repository, checkout, ignore=shutil.ignore_patterns(
            ".git", ".codex", ".agents", ".aws", "node_modules", "tmp", "bin"))
        script = checkout / "script/update.sh"
        mounted = []
        try:
            # Keep the actual Go toolchain available after hiding /opt. The
            # no-HOME case must compile real Go, not just exercise a mock.
            toolchain = root / 'go-toolchain'
            toolchain.mkdir()
            subprocess.run(['mount', '--bind', str(go_root), str(toolchain)], check=True)
            mounted.append(str(toolchain))
            go_source = root / 'probe.go'
            go_source.write_text('package main\nfunc main() {}\n')
            for target in ("/opt", "/root", "/etc", "/run", "/usr/local", "/var/lib"):
                source = root / target.lstrip("/")
                source.mkdir(parents=True, exist_ok=True)
                subprocess.run(["mount", "--bind", str(source), target], check=True)
                mounted.append(target)
            for directory in ("/etc/ppp", "/etc/xl2tpd", "/etc/systemd/system", "/usr/local/bin"):
                Path(directory).mkdir(parents=True)
            tools = root / "tools"
            tools.mkdir()
            mocks = {
                "go": r'''echo build-attempt >> "$S2L_TEST_LOG"
if [[ "${S2L_TEST_REQUIRE_MANAGED:-0}" == 1 && "${S2L_TEST_MANAGED_GO:-0}" != 1 ]]; then
  echo incompatible-system-go >&2
  exit 1
fi
[[ "${FAIL_BUILD:-0}" != 1 ]] || exit 1
if [[ -n "${S2L_TEST_REAL_GO:-}" ]]; then
  "$S2L_TEST_REAL_GO" build -o "$S2L_TEST_GO_OUTPUT" "$S2L_TEST_GO_SOURCE"
fi
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
start|restart) shift; for name in "$@"; do
  [[ "${FAIL_IPSEC_START:-0}:$name" != 1:s2l-ipsec ]] || exit 1
  touch "$S2L_TEST_STATE/$name"
done ;;
*) exit 0 ;;
esac''',
                "install": r'''if [[ "${FAIL_INSTALL:-0}" == 1 && "${@: -1}" == /etc/systemd/system/s2l.service ]]; then exit 1; fi
exec /usr/bin/install "$@"''',
                "iptables": "exit 0", "iptables-restore": "cat >/dev/null", "ip6tables": "exit 0",
                "iptables-save": "echo fixture", "ip6tables-save": "echo fixture", "conntrack": "exit 0",
                "dpkg-query": "exit 1",  # Actual legacy package auditing is covered in the full VMs.
                "apt-get": r'''echo "apt $*" >> "$S2L_TEST_LOG"
[[ "$DEBIAN_FRONTEND:$NEEDRESTART_MODE" == noninteractive:l ]]
if [[ "$*" == *"update -qq" ]]; then
  [[ "${FAIL_DEPENDENCY:-}" != update ]]
elif [[ "$*" == *"install -y --no-install-recommends --no-upgrade conntrack" ]]; then
  [[ "${FAIL_DEPENDENCY:-}" != install ]] || exit 1
  if [[ "${FAIL_DEPENDENCY:-}" != missing ]]; then touch "$S2L_TEST_CONNTRACK"; fi
else
  exit 1
fi''',
            }
            for name, body in mocks.items():
                path = tools / name
                path.write_text("#!/bin/bash\nset -e\n" + body + "\n")
                path.chmod(0o755)
            state = root / "services"
            state.mkdir()
            log = root / "commands.log"
            env = dict(os.environ, PATH=f"{tools}:/usr/bin:/bin", S2L_TEST_STATE=str(state), S2L_TEST_LOG=str(log))
            # Hide only the dependency lookup; even a host with conntrack already
            # installed must exercise the real updater's missing-package path.
            lookup = root / 'dependency-lookup.sh'
            lookup.write_text('''command() {
  if [[ "${1:-}" == -v && "${2:-}" == conntrack && ! -e "$S2L_TEST_CONNTRACK" ]]; then return 1; fi
  if [[ "${1:-}" == -v && "${2:-}" == apt-get && "${FAIL_DEPENDENCY:-}" == manager ]]; then return 1; fi
  builtin command "$@"
}
''')
            dependency_failures = ('dependency_update_failure', 'dependency_install_failure',
                                   'dependency_missing_failure', 'dependency_manager_failure')
            successes = ('success_stopped', 'success_no_home', 'success_managed_go', 'success_ipsec', 'success_dependency')
            for scenario in ("build_failure", "success_stopped", "success_no_home", "install_failure", "success_managed_go", "success_ipsec", "ipsec_start_failure", 'success_dependency', *dependency_failures):
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
                receipt_root = Path('/var/lib/s2l-dependencies')
                if receipt_root.exists():
                    shutil.rmtree(receipt_root)
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
                if scenario == 'success_dependency' or scenario in dependency_failures:
                    installed = root / 'conntrack-installed'
                    installed.unlink(missing_ok=True)
                    case_env.update(BASH_ENV=str(lookup), S2L_TEST_CONNTRACK=str(installed))
                    if scenario in dependency_failures:
                        case_env['FAIL_DEPENDENCY'] = scenario.split('_')[1]
                        for name in ('s2l', 'xl2tpd', 's2l-ipsec'):
                            (state / name).touch()
                elif scenario == "build_failure":
                    case_env["FAIL_BUILD"] = "1"
                elif scenario == "success_no_home":
                    for key in ('HOME', 'XDG_CACHE_HOME', 'GOCACHE', 'GOPATH', 'GOENV'):
                        case_env.pop(key, None)
                    case_env.update(S2L_TEST_REAL_GO=str(toolchain / 'bin/go'), GOROOT=str(toolchain),
                                    GOTOOLCHAIN='local', S2L_TEST_GO_SOURCE=str(go_source),
                                    S2L_TEST_GO_OUTPUT=str(root / 'probe'))
                    control = subprocess.run([str(toolchain / 'bin/go'), 'build', '-o', str(root / 'uncached-probe'), str(go_source)],
                                             env=case_env, capture_output=True, text=True)
                    assert control.returncode != 0 and 'GOCACHE' in control.stderr, \
                        'missing-HOME control did not reproduce the build-cache failure: ' + control.stdout + control.stderr
                elif scenario == "install_failure":
                    case_env["FAIL_INSTALL"] = "1"
                    (state / "s2l").touch()
                    (state / "xl2tpd").touch()
                elif scenario in ('success_ipsec', 'ipsec_start_failure'):
                    (state / 's2l-ipsec').touch()
                    (app / 'ipsec-settings.json').write_text('{"enabled":true,"psk":"fixture-unchanged-psk"}')
                    if scenario == 'ipsec_start_failure':
                        case_env['FAIL_IPSEC_START'] = '1'
                elif scenario == "success_managed_go":
                    managed_go = Path('/usr/local/go/bin/go')
                    managed_go.parent.mkdir(parents=True)
                    managed_go.write_text('#!/bin/bash\necho managed-go >> "$S2L_TEST_LOG"\n'
                                          'export S2L_TEST_MANAGED_GO=1\nexec ' + shlex.quote(str(tools / 'go')) + ' "$@"\n')
                    managed_go.chmod(0o755)
                    case_env['S2L_TEST_REQUIRE_MANAGED'] = '1'
                    control = subprocess.run(['go', 'version'], env=case_env, capture_output=True, text=True)
                    assert control.returncode != 0 and 'incompatible-system-go' in control.stderr, \
                        'control did not reproduce selection of the incompatible system Go'
                result = subprocess.run(["bash", str(script)], env=case_env, capture_output=True, text=True)
                assert (result.returncode == 0) == (scenario in successes), result.stdout + result.stderr
                assert (app / "config.json").read_bytes() == original
                assert json.loads((app / "data/state.json").read_text()) == {"nodes": [], "mappings": []}
                if scenario in dependency_failures:
                    assert 'conntrack dependency installation failed' in result.stderr
                    assert 'build-attempt' not in log.read_text() and 'stop' not in log.read_text()
                    assert binary.read_bytes() == old_binary
                    assert Path('/etc/systemd/system/s2l.service').read_text() == 'original-unit'
                    assert all((state / name).exists() for name in ('s2l', 'xl2tpd', 's2l-ipsec'))
                elif scenario == "build_failure":
                    assert "build-attempt" in log.read_text(), "failure did not reach the build step: " + result.stdout + result.stderr
                    assert "stop" not in log.read_text(), "build failure stopped live services"
                    assert binary.read_bytes() == old_binary
                elif scenario in successes:
                    assert log.read_text().index("guard-only") < log.read_text().index("stop xl2tpd"), "PPP was stopped before guards were installed"
                    if scenario == 'success_no_home':
                        assert (root / 'probe').is_file(), 'real compilation was not exercised'
                    if scenario == 'success_managed_go':
                        assert 'managed-go' in log.read_text(), 'managed Go was not selected'
                    if scenario == 'success_dependency':
                        commands = log.read_text()
                        assert commands.index('apt ') < commands.index('build-attempt') < commands.index('stop xl2tpd')
                        assert 'DPkg::Lock::Timeout=120' in commands and 'Acquire::Retries=2' in commands
                        assert installed.exists(), 'dependency was not installed'
                    assert binary.read_bytes() != old_binary
                    assert not (state / "s2l").exists(), "stopped service was started"
                    assert not (state / "xl2tpd").exists()
                    assert (app / 'ipsec_nat.py').is_file()
                    assert (app / 'manage_kernel.py').read_bytes() == (checkout / 'script/manage_kernel.py').read_bytes()
                    assert json.loads(Path('/var/lib/s2l-dependencies/receipt.json').read_text())['legacy_packages'] == []
                    assert (state / 's2l-ipsec').exists() == (scenario == 'success_ipsec')
                    if scenario == 'success_ipsec':
                        assert json.loads((app / 'ipsec-settings.json').read_text()) == dict(enabled=True, psk='fixture-unchanged-psk')
                        assert log.read_text().index('stop s2l-ipsec') < log.read_text().index('start s2l-ipsec')
                    backups = list(Path("/root").glob("s2l-backup.*"))
                    assert backups and all(p.stat().st_mode & 0o077 == 0 for p in backups)
                else:
                    assert binary.read_bytes() == old_binary, "failed install did not restore old binary"
                    assert Path("/etc/systemd/system/s2l.service").read_text() == "original-unit"
                    assert not (state / "s2l").exists() and not (state / "xl2tpd").exists()
                    assert not (state / 's2l-ipsec').exists(), 'IPsec left running after failed upgrade'
                    assert Path("/etc/systemd/system/xl2tpd.service.d/s2l.conf").exists(), "rollback removed boot guard"
                assert Path("/").stat().st_mode == original_root_mode, "upgrade changed root directory permissions"
                if scenario != 'success_dependency' and scenario not in dependency_failures:
                    assert 'apt ' not in log.read_text(), 'existing conntrack triggered a package operation'
                print(f"PASS: {scenario}")
        finally:
            for target in reversed(mounted):
                subprocess.run([umount, target], check=True)


if __name__ == "__main__":
    main()
