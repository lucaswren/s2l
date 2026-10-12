"""Validate the managed layout and check the local authenticated API after upgrade."""
import base64
import json
import ipaddress
from pathlib import Path
import sys
import time
from urllib.error import HTTPError
from urllib.request import Request, build_opener, ProxyHandler, HTTPRedirectHandler


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, new_url):
        return None


def preflight(path):
    path = Path(path)
    if path.is_symlink() or path.parent.is_symlink():
        raise ValueError("不支持符号链接安装目录或配置；请使用手动升级流程")
    config = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(config, dict):
        raise ValueError("配置须为 JSON 对象")
    expected = {
        "data_dir": "/opt/s2l/data",
        "chap_secrets": "/etc/ppp/chap-secrets",
        "xl2tpd_conf": "/etc/xl2tpd/xl2tpd.conf",
        "ppp_options": "/etc/ppp/options.xl2tpd",
    }
    for key, target in expected.items():
        value = config.get(key, "./data" if key == "data_dir" else target)
        actual = Path(value)
        if not actual.is_absolute():
            actual = Path("/opt/s2l") / actual
        if str(actual.resolve()) != target:
            raise ValueError("自定义文件路径不支持自动升级；请使用手动升级流程")
    host, port = config.get("listen", "127.0.0.1:8080").rsplit(":", 1)
    host = host.strip("[]")
    if not 1 <= int(port) <= 65535:
        raise ValueError("管理端口无效")
    if host not in ("", "localhost"):
        address = ipaddress.ip_address(host)
        if not (address.is_loopback or address.is_unspecified):
            raise ValueError("非本机监听部署须使用手动升级流程")
    return config


def check_health(config, attempts=30, delay=1):
    listen = config.get("listen", "127.0.0.1:8080")
    host, port = listen.rsplit(":", 1)
    host = host.strip("[]")
    if host in ("", "0.0.0.0", "localhost"):
        host = "127.0.0.1"
    elif host == "::":
        host = "::1"
    if ":" in host:
        host = f"[{host}]"
    request = Request(f"http://{host}:{int(port)}/api/status")
    user, password = config.get("admin_user", ""), config.get("admin_pass", "")
    if user or password:
        encoded = base64.b64encode(f"{user}:{password}".encode()).decode()
        request.add_header("Authorization", f"Basic {encoded}")
    opener = build_opener(ProxyHandler({}), NoRedirect())
    for attempt in range(attempts):
        try:
            with opener.open(request, timeout=2) as response:
                status = json.loads(response.read(1024 * 1024))
            if isinstance(status, dict) and isinstance(status.get("upstream_health"), dict) and isinstance(status.get("mappings"), list):
                return
        except HTTPError as error:
            error.close()
        except (OSError, ValueError):
            pass
        if attempt + 1 < attempts:
            time.sleep(delay)
    raise ValueError("新服务的本机 API 验证失败；请检查服务日志")


if __name__ == "__main__":
    try:
        config = preflight(sys.argv[2])
        if sys.argv[1] == "health":
            check_health(config)
        elif sys.argv[1] != "preflight":
            raise ValueError("未知验证模式")
    except (OSError, ValueError, TypeError, IndexError):
        # Never expose the config, URL credentials or authentication headers.
        print("[ERROR] 安装路径/配置验证或本机 API 检查失败；自定义部署请使用手动升级流程。", file=sys.stderr)
        sys.exit(1)
