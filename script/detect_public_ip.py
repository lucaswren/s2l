#!/usr/bin/env python3
"""Find and validate a public IPv4 address for HTTPS certificate issuance."""
import ipaddress
import json
import os
import subprocess
import sys
import urllib.request


def public_ipv4(value):
    address = ipaddress.IPv4Address(value.strip())
    if not address.is_global:
        raise ValueError('PUBLIC_IP 必须为公网 IPv4 地址')
    return str(address)


def detect():
    override = os.environ.get('PUBLIC_IP', '')
    if override:
        return public_ipv4(override)
    for endpoint in ('https://api.ipify.org', 'https://4.ident.me'):
        try:
            with urllib.request.urlopen(endpoint, timeout=4) as response:
                return public_ipv4(response.read(64).decode())
        except (OSError, ValueError):
            continue
    try:
        result = subprocess.run(['ip', '-j', '-4', 'addr'], check=True,
                                capture_output=True, text=True, timeout=4)
        for interface in json.loads(result.stdout):
            for address in interface.get('addr_info', []):
                try:
                    return public_ipv4(address.get('local', ''))
                except ValueError:
                    continue
    except (OSError, ValueError, subprocess.SubprocessError):
        pass
    raise ValueError('无法识别公网 IPv4；请设置 PUBLIC_IP=服务器公网IPv4 后重新安装')


if __name__ == '__main__':
    try:
        print(detect())
    except ValueError as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
