"""Run the real L2TP fixture in disposable Linux mount/network namespaces.

Build: go test -c -o /tmp/s2l-l2tp.test ./internal/service
Run: sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test
     --singbox /absolute/path/to/sing-box
No internet access is used by the test itself. Requires xl2tpd, ppp, curl,
iproute2, iptables, and kernel PPP/L2TP support on a disposable Linux test host.
"""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--test-binary", required=True, type=Path)
    parser.add_argument("--singbox", required=True, type=Path)
    parser.add_argument("--transport", choices=("plain", "ipsec", "ipsec-nat", "ipsec-shared-nat", "ipsec-shared-nat-1701"), default="plain",
                        help="IPsec modes require strongSwan starter and XFRM kernel support")
    parser.add_argument("--inner", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if sys.platform != "linux" or os.geteuid() != 0:
        raise SystemExit("requires Linux root on a disposable test host")
    binary, singbox = args.test_binary.resolve(strict=True), args.singbox.resolve(strict=True)
    if not args.inner:
        return subprocess.run([
            "unshare", "--mount", "--net", "--pid", "--fork", "--propagation", "private",
            sys.executable, str(Path(__file__).resolve()), "--inner",
            "--test-binary", str(binary), "--singbox", str(singbox),
            "--transport", args.transport,
        ], check=False).returncode
    if any(os.readlink(f"/proc/self/ns/{kind}") == os.readlink(f"/proc/1/ns/{kind}")
           for kind in ("mnt", "net")):
        raise SystemExit("refusing to run in host namespace")
    mounted = []

    def mount(*options):
        subprocess.run(["mount", *options], check=True)
        mounted.append(options[-1])

    with tempfile.TemporaryDirectory(prefix="s2l-l2tp-fixture-") as temp:
        root = Path(temp)
        try:
            # Inherit namespace FDs: bind-mounting an ancestor mount namespace
            # into its descendant is rejected by the kernel as a reference loop.
            host_fds = {kind: os.open("/proc/1/ns/" + kind, os.O_RDONLY)
                        for kind in ("mnt", "net")}
            mount("-t", "proc", "proc", "/proc")
            # Avoid host PPP hooks, secrets, runtime files and device changes.
            for target in ("/etc/ppp", "/run"):
                source = root / target.lstrip("/")
                source.mkdir(parents=True)
                if not Path(target).is_dir():
                    raise SystemExit(f"missing prerequisite directory: {target}")
                mount("--bind", str(source), target)
            mount("-t", "tmpfs", "-o", "mode=755", "tmpfs", "/dev")
            for name, major, minor in (("null", 1, 3), ("zero", 1, 5),
                                       ("random", 1, 8), ("urandom", 1, 9),
                                       ("ppp", 108, 0)):
                os.mknod(f"/dev/{name}", 0o20600, os.makedev(major, minor))
            Path("/dev/net").mkdir()
            os.mknod("/dev/net/tun", 0o20600, os.makedev(10, 200))
            Path("/dev/pts").mkdir()
            mount("-t", "devpts", "-o", "newinstance,ptmxmode=0666", "devpts", "/dev/pts")
            Path("/dev/ptmx").symlink_to("pts/ptmx")
            Path("/dev/fd").symlink_to("/proc/self/fd")
            # sysfs network counters otherwise still refer to the namespace
            # in which the original /sys was mounted.
            mount("-t", "sysfs", "-o", "ro", "sysfs", "/sys")
            env = dict(os.environ, S2L_L2TP_INTEGRATION="1", **{"S2L_FIXTURE_HOST_" + kind.upper() + "_FD": str(fd) for kind, fd in host_fds.items()},
                       S2L_SINGBOX_CHECK_BIN=str(singbox), S2L_FIXTURE_TRANSPORT=args.transport,
                       S2L_FIXTURE_SCRIPT_DIR=str(Path(__file__).resolve().parent))
            return subprocess.run([str(binary), "-test.run", "^TestRealL2TPFixedExitsAndRecovery$",
                                   "-test.v", "-test.timeout", "600s"], env=env,
                                  check=False, pass_fds=tuple(host_fds.values())).returncode
        finally:
            for fd in locals().get("host_fds", {}).values():
                os.close(fd)
            for target in reversed(mounted):
                subprocess.run(["umount", "--lazy", target], check=False)


if __name__ == "__main__":
    raise SystemExit(main())
