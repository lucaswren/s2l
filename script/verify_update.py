"""Validate the managed layout and check the local authenticated API after upgrade."""
from http.cookiejar import CookieJar
import json
import ipaddress
from pathlib import Path
import sys
import time
from urllib.error import HTTPError
from urllib.request import Request, build_opener, ProxyHandler, HTTPRedirectHandler, HTTPCookieProcessor


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


def locally_ready(status, expected_ids=None, auto_restore=True):
    if not isinstance(status, dict) or status.get("startup_complete") is not True:
        return False
    health, mappings, pids = status.get("upstream_health"), status.get("mappings"), status.get("singbox_pids")
    if not isinstance(health, dict) or not isinstance(mappings, list) or not isinstance(pids, dict):
        return False
    if not auto_restore:
        return True
    current = {m.get("id"): m for m in mappings if isinstance(m, dict)}
    required = expected_ids if expected_ids is not None else [m.get("id") for m in mappings if isinstance(m, dict) and m.get("status") == "running"]
    for mapping_id in required:
        m, h = current.get(mapping_id, {}), health.get(mapping_id, {})
        if m.get("status") != "running" or not isinstance(h, dict) or h.get("local_state") != "healthy" or not isinstance(pids.get(m.get("tun_name")), int) or pids.get(m.get("tun_name"), 0) <= 0:
            return False
    return True


def check_health(config, attempts=120, delay=1, expected_ids=None):
    listen = config.get("listen", "127.0.0.1:8080")
    host, port = listen.rsplit(":", 1)
    host = host.strip("[]")
    if host in ("", "0.0.0.0", "localhost"):
        host = "127.0.0.1"
    elif host == "::":
        host = "::1"
    if ":" in host:
        host = f"[{host}]"
    base_url = f"http://{host}:{int(port)}"
    request = Request(base_url + "/api/status")
    user, password = config.get("admin_user", ""), config.get("admin_pass", "")
    cookies = CookieJar()
    opener = build_opener(ProxyHandler({}), NoRedirect(), HTTPCookieProcessor(cookies))
    logged_in = not (user and password)
    try:
        for attempt in range(attempts):
            try:
                if not logged_in:
                    login = Request(base_url + "/api/auth/login",
                                    data=json.dumps({"username": user, "password": password}).encode(),
                                    headers={"Content-Type": "application/json"})
                    with opener.open(login, timeout=2) as response:
                        result = json.loads(response.read(4096))
                    if not isinstance(result, dict) or result.get("authenticated") is not True or not any(
                            cookie.name == "s2l_session" for cookie in cookies):
                        raise ValueError("登录验证失败")
                    logged_in = True
                with opener.open(request, timeout=2) as response:
                    status = json.loads(response.read(1024 * 1024))
                if locally_ready(status, expected_ids, config.get("auto_restore", True) is not False):
                    return
            except HTTPError as error:
                if error.code == 401:
                    logged_in = not (user and password)
                    cookies.clear()
                error.close()
            except (OSError, ValueError):
                pass
            if attempt + 1 < attempts:
                time.sleep(delay)
    finally:
        if user and password and any(cookie.name == "s2l_session" for cookie in cookies):
            try:
                logout = Request(base_url + "/api/auth/logout", data=b"{}",
                                 headers={"Content-Type": "application/json"})
                with opener.open(logout, timeout=2):
                    pass
            except HTTPError as error:
                error.close()
            except OSError:
                pass

    raise ValueError("新服务的本机 API 验证失败；请检查服务日志")


if __name__ == "__main__":
    try:
        config = preflight(sys.argv[2])
        if sys.argv[1] == "health":
            expected = None
            if len(sys.argv) > 3:
                state_path = Path(sys.argv[3])
                old_state = json.loads(state_path.read_text(encoding="utf-8")) if state_path.exists() else {"mappings": []}
                expected = [m["id"] for m in old_state["mappings"] if m.get("status") == "running"]
            check_health(config, expected_ids=expected)
        elif sys.argv[1] != "preflight":
            raise ValueError("未知验证模式")
    except (OSError, ValueError, TypeError, IndexError):
        # Never expose the config, URL credentials or authentication headers.
        print("[ERROR] 安装路径/配置验证或本机 API 检查失败；自定义部署请使用手动升级流程。", file=sys.stderr)
        sys.exit(1)
