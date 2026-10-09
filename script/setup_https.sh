#!/usr/bin/env bash
# Configure a self-signed public-IP certificate and nginx reverse proxy.
set -euo pipefail
[[ "$(id -u)" -eq 0 ]] || { echo "请使用 sudo 执行" >&2; exit 1; }
[[ $# -ge 1 && $# -le 2 ]] || { echo "用法：sudo bash script/setup_https.sh 公网IPv4 [HTTPS端口]" >&2; exit 2; }
IP="$(python3 - "$1" <<'PY'
import ipaddress, sys
address = ipaddress.IPv4Address(sys.argv[1])
if not address.is_global:
    raise SystemExit('请输入公网 IPv4 地址')
print(address)
PY
)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG=/opt/s2l/config.json
SITE=/etc/nginx/conf.d/s2l.conf
[[ -f "$CONFIG" ]] || { echo "请先安装 s2l" >&2; exit 1; }
HTTPS_PORT="$(python3 - "$CONFIG" "${2:-${HTTPS_PORT:-}}" "${SCRIPT_DIR}" <<'PY'
import json, sys
from urllib.parse import urlsplit
sys.path.insert(0, sys.argv[3])
from manage_config import validate_port, random_port, check_available_port
config=json.load(open(sys.argv[1]))
address=urlsplit(config.get('public_url',''))
existing=(address.port or 443) if address.scheme == 'https' else None
backend=int(config['listen'].rsplit(':',1)[1])
value=sys.argv[2]
port=validate_port(value) if value else existing
if port is None:
    port=random_port()
    while port == backend:
        port=random_port()
if port == backend:
    raise SystemExit('HTTPS 端口不能与本机后端端口相同')
if port != existing:
    check_available_port(port)
print(port)
PY
)"
if [[ -e "$SITE" ]] && ! grep -q '^# Managed by s2l HTTPS' "$SITE"; then
  echo "检测到非 s2l 管理的同名 nginx 配置，已停止" >&2; exit 1
fi
if ! command -v nginx >/dev/null || ! command -v openssl >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y --no-install-recommends nginx openssl ca-certificates
fi
CERT=/opt/s2l/tls/self-signed.crt
KEY=/opt/s2l/tls/self-signed.key
CERT_TYPE=self_signed
BACKUP="$(mktemp -d /opt/s2l/https-backup-XXXXXXXX)"
chmod 700 "$BACKUP"
cp "$CONFIG" "$BACKUP/config.json"
chmod 600 "$BACKUP/config.json"
[[ ! -f "$SITE" ]] || cp "$SITE" "$BACKUP/nginx.conf"
[[ ! -f "$CERT" ]] || cp "$CERT" "$BACKUP/cert.pem"
[[ ! -f "$KEY" ]] || cp "$KEY" "$BACKUP/key.pem"
MODIFIED_CONFIG=0
MODIFIED_CERT=0
DEFAULT_SITE_DISABLED=0
rollback() {
  local result=$?
  trap - ERR
  if [[ "$MODIFIED_CONFIG" == 1 ]]; then
    install -m 600 "$BACKUP/config.json" "$CONFIG"
    systemctl restart s2l || true
  fi
  if [[ "$MODIFIED_CERT" == 1 ]]; then
    if [[ -f "$BACKUP/cert.pem" ]]; then install -m 600 "$BACKUP/cert.pem" "$CERT"; else rm -f "$CERT"; fi
    if [[ -f "$BACKUP/key.pem" ]]; then install -m 600 "$BACKUP/key.pem" "$KEY"; else rm -f "$KEY"; fi
  fi
  if [[ -f "$BACKUP/nginx.conf" ]]; then
    install -m 644 "$BACKUP/nginx.conf" "$SITE"
  else
    rm -f "$SITE"
  fi
  if [[ "$DEFAULT_SITE_DISABLED" == 1 ]]; then
    cp -P "$BACKUP/default-site" /etc/nginx/sites-enabled/default
  fi
  nginx -t && systemctl reload nginx || true
  echo "HTTPS 配置失败，已恢复原应用与 nginx 配置。备份：$BACKUP" >&2
  exit "$result"
}
trap rollback ERR
# Disable only the untouched distribution default site; keep custom sites.
if python3 - <<'PY'
import hashlib, pathlib, subprocess, sys
link=pathlib.Path('/etc/nginx/sites-enabled/default')
source=pathlib.Path('/etc/nginx/sites-available/default')
if not link.is_symlink() or link.resolve() != source or not source.is_file():
    sys.exit(1)
try:
    records=subprocess.check_output(['dpkg-query','-W','-f=${Conffiles}','nginx-common'],text=True)
except (OSError,subprocess.CalledProcessError):
    sys.exit(1)
for line in records.splitlines():
    fields=line.split()
    if len(fields)>=2 and fields[0]==str(source):
        sys.exit(0 if hashlib.md5(source.read_bytes()).hexdigest()==fields[1] else 1)
sys.exit(1)
PY
then
  cp -P /etc/nginx/sites-enabled/default "$BACKUP/default-site"
  rm /etc/nginx/sites-enabled/default
  DEFAULT_SITE_DISABLED=1
fi
mkdir -p /opt/s2l/tls
chmod 700 /opt/s2l/tls
if [[ ! -f "$CERT" || ! -f "$KEY" ]] \
  || ! openssl x509 -in "$CERT" -noout -checkend 86400 >/dev/null 2>&1 \
  || ! openssl x509 -in "$CERT" -noout -checkip "$IP" >/dev/null 2>&1; then
  temporary="$(mktemp -d /opt/s2l/tls/.generate-XXXXXXXX)"
  chmod 700 "$temporary"
  openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
    -subj "/CN=${IP}" -addext "subjectAltName=IP:${IP}" \
    -keyout "$temporary/key.pem" -out "$temporary/cert.pem" >/dev/null 2>&1
  chmod 600 "$temporary/key.pem" "$temporary/cert.pem"
  MODIFIED_CERT=1
  mv "$temporary/key.pem" "$KEY"
  mv "$temporary/cert.pem" "$CERT"
  rmdir "$temporary"
fi
PORT="$(python3 - "$CONFIG" "$HTTPS_PORT" <<'PY'
import json, sys
config=json.load(open(sys.argv[1]))
port=int(config['listen'].rsplit(':',1)[1])
if port == int(sys.argv[2]) or not 1 <= port <= 65535:
    raise SystemExit('后端端口不能与 HTTPS 端口相同')
print(port)
PY
)"
cat >"$SITE" <<EOF
# Managed by s2l HTTPS
server {
    listen ${HTTPS_PORT} ssl;
    server_name ${IP};
    ssl_certificate ${CERT};
    ssl_certificate_key ${KEY};
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:S2L_TLS:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;
    server_tokens off;
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options DENY always;
    add_header Referrer-Policy no-referrer always;
    client_max_body_size 1m;
    location / {
        proxy_pass http://127.0.0.1:${PORT};
        proxy_set_header Host \$http_host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header Connection "";
        proxy_http_version 1.1;
        proxy_read_timeout 75s;
        proxy_send_timeout 75s;
    }
}
EOF
nginx -t
# Commit only after generating a certificate and validating nginx.
cp "$CONFIG" "$BACKUP/config.json"
chmod 600 "$BACKUP/config.json"
MODIFIED_CONFIG=1
RESTART_REQUIRED="$(python3 - "$CONFIG" "$PORT" <<'PY'
import json,sys
print(int(json.load(open(sys.argv[1]))["listen"] != "127.0.0.1:"+sys.argv[2]))
PY
)"
python3 - "$CONFIG" "$IP" "$PORT" "$CERT_TYPE" "$HTTPS_PORT" <<'PY'
import json, os, sys, tempfile
path, address, port, certificate_type, https_port=sys.argv[1:]
config=json.load(open(path))
config['listen']='127.0.0.1:'+port
config['public_url']='https://'+address+':'+https_port
config['https_certificate']=certificate_type
fd, temporary=tempfile.mkstemp(dir=os.path.dirname(path),prefix='.s2l-https-')
try:
    with os.fdopen(fd,'w') as output:
        os.fchmod(output.fileno(),0o600)
        json.dump(config,output,ensure_ascii=False,indent=2)
        output.write('\n'); output.flush(); os.fsync(output.fileno())
    os.replace(temporary,path)
finally:
    if os.path.exists(temporary): os.unlink(temporary)
PY
if [[ "$RESTART_REQUIRED" == 1 ]]; then
  systemctl restart s2l
  sleep 2
fi
systemctl is-active --quiet s2l
systemctl enable --now nginx
systemctl reload nginx
if systemctl cat s2l-cert-renew.timer >/dev/null 2>&1; then
  systemctl disable --now s2l-cert-renew.timer
fi
if systemctl cat s2l-cert-renew.service >/dev/null 2>&1; then
  systemctl stop s2l-cert-renew.service
fi
trap - ERR
echo "HTTPS 已启用：https://${IP}:${HTTPS_PORT}"
echo "使用自签证书，有效期 365 天；浏览器需手动信任或导入证书。"
echo "SHA-256 指纹："
openssl x509 -in "$CERT" -noout -fingerprint -sha256
echo "仅需放行 TCP ${HTTPS_PORT}；无需 TCP 80 或公网证书申请服务。"
echo "证书：${CERT}；到期前重新执行本脚本更新证书。配置备份：${BACKUP}"
