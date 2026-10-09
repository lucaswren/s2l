#!/usr/bin/env bash
# Configure a trusted public-IP certificate and nginx reverse proxy.
set -euo pipefail
[[ "$(id -u)" -eq 0 ]] || { echo "请使用 sudo 执行" >&2; exit 1; }
[[ $# -eq 1 ]] || { echo "用法：sudo bash script/setup_https.sh 公网IPv4" >&2; exit 2; }
IP="$(python3 - "$1" <<'PY'
import ipaddress, sys
address = ipaddress.IPv4Address(sys.argv[1])
if not address.is_global:
    raise SystemExit('请输入公网 IPv4 地址')
print(address)
PY
)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG=/opt/s2l/config.json
SITE=/etc/nginx/conf.d/s2l.conf
[[ -f "$CONFIG" ]] || { echo "请先安装 s2l" >&2; exit 1; }
if [[ -e "$SITE" ]] && ! grep -q '^# Managed by s2l HTTPS' "$SITE"; then
  echo "检测到非 s2l 管理的同名 nginx 配置，已停止" >&2; exit 1
fi
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends nginx openssl ca-certificates
CERTBOT_READY=0
if apt-get install -y --no-install-recommends python3-venv \
  && python3 -m venv /opt/s2l/certbot \
  && /opt/s2l/certbot/bin/pip install --quiet 'certbot>=5.4,<6'; then
  CERTBOT_READY=1
else
  echo "证书申请组件未就绪，将使用可用证书或自签证书。" >&2
fi
BACKUP="$(mktemp -d /opt/s2l/https-backup-XXXXXXXX)"
chmod 700 "$BACKUP"
cp "$CONFIG" "$BACKUP/config.json"
chmod 600 "$BACKUP/config.json"
[[ ! -f "$SITE" ]] || cp "$SITE" "$BACKUP/nginx.conf"
MODIFIED_CONFIG=0
rollback() {
  local result=$?
  trap - ERR
  if [[ "$MODIFIED_CONFIG" == 1 ]]; then
    install -m 600 "$BACKUP/config.json" "$CONFIG"
    systemctl restart s2l || true
  fi
  if [[ -f "$BACKUP/nginx.conf" ]]; then
    install -m 644 "$BACKUP/nginx.conf" "$SITE"
  else
    rm -f "$SITE"
  fi
  nginx -t && systemctl reload nginx || true
  echo "HTTPS 配置失败，已恢复原应用与 nginx 配置。备份：$BACKUP" >&2
  exit "$result"
}
trap rollback ERR
mkdir -p /var/www/s2l-acme
# Keep an already working HTTPS site online during repeated setup.
if [[ ! -f "$SITE" ]]; then
  cat >"$SITE" <<EOF
# Managed by s2l HTTPS
server {
    listen 80;
    server_name ${IP};
    location ^~ /.well-known/acme-challenge/ { root /var/www/s2l-acme; }
    location / { return 404; }
}
EOF
fi
nginx -t
systemctl enable --now nginx
systemctl reload nginx
CERT=/etc/letsencrypt/live/s2l-ip/fullchain.pem
KEY=/etc/letsencrypt/live/s2l-ip/privkey.pem
CERT_TYPE=letsencrypt
if [[ "$CERTBOT_READY" == 1 ]] && /opt/s2l/certbot/bin/certbot certonly --non-interactive --agree-tos \
  --register-unsafely-without-email --preferred-profile shortlived \
  --webroot --webroot-path /var/www/s2l-acme --ip-address "$IP" --cert-name s2l-ip; then
  echo "可信公网 IP 证书已就绪。"
elif [[ -f "$CERT" && -f "$KEY" ]] \
  && openssl x509 -in "$CERT" -noout -checkend 86400 >/dev/null 2>&1 \
  && openssl x509 -in "$CERT" -noout -checkip "$IP" >/dev/null 2>&1; then
  echo "申请失败，继续使用仍有效的现有可信证书。" >&2
else
  CERT_TYPE=self_signed
  mkdir -p /opt/s2l/tls
  chmod 700 /opt/s2l/tls
  CERT=/opt/s2l/tls/self-signed.crt
  KEY=/opt/s2l/tls/self-signed.key
  if [[ ! -f "$CERT" || ! -f "$KEY" ]] \
    || ! openssl x509 -in "$CERT" -noout -checkend 86400 >/dev/null 2>&1 \
    || ! openssl x509 -in "$CERT" -noout -checkip "$IP" >/dev/null 2>&1; then
    temporary="$(mktemp -d /opt/s2l/tls/.generate-XXXXXXXX)"
    chmod 700 "$temporary"
    openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
      -subj "/CN=${IP}" -addext "subjectAltName=IP:${IP}" \
      -keyout "$temporary/key.pem" -out "$temporary/cert.pem" >/dev/null 2>&1
    chmod 600 "$temporary/key.pem" "$temporary/cert.pem"
    mv "$temporary/key.pem" "$KEY"
    mv "$temporary/cert.pem" "$CERT"
    rmdir "$temporary"
  fi
  echo "可信证书申请失败，已使用自签证书启用 HTTPS（有效期 365 天）。" >&2
  echo "浏览器会提示证书不受信任；请核对以下 SHA-256 指纹后手动信任或导入证书。" >&2
  openssl x509 -in "$CERT" -noout -fingerprint -sha256
fi
PORT="$(python3 - "$CONFIG" <<'PY'
import json, sys
config=json.load(open(sys.argv[1]))
port=int(config['listen'].rsplit(':',1)[1])
if port in (80,443) or not 1 <= port <= 65535:
    raise SystemExit('后端端口不能为 80 或 443；请先修改网页端口')
print(port)
PY
)"
cat >"$SITE" <<EOF
# Managed by s2l HTTPS
server {
    listen 80;
    server_name ${IP};
    location ^~ /.well-known/acme-challenge/ { root /var/www/s2l-acme; }
    location / { return 308 https://${IP}\$request_uri; }
}
server {
    listen 443 ssl;
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
# Commit only after obtaining a certificate and validating nginx.
cp "$CONFIG" "$BACKUP/config.json"
chmod 600 "$BACKUP/config.json"
MODIFIED_CONFIG=1
python3 - "$CONFIG" "$IP" "$PORT" "$CERT_TYPE" <<'PY'
import json, os, sys, tempfile
path, address, port, certificate_type=sys.argv[1:]
config=json.load(open(path))
config['listen']='127.0.0.1:'+port
config['public_url']='https://'+address
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
systemctl restart s2l
sleep 2
systemctl is-active --quiet s2l
systemctl reload nginx
install -m 644 "$ROOT_DIR/deploy/systemd/s2l-cert-renew.service" /etc/systemd/system/s2l-cert-renew.service
install -m 644 "$ROOT_DIR/deploy/systemd/s2l-cert-renew.timer" /etc/systemd/system/s2l-cert-renew.timer
mkdir -p /etc/letsencrypt/renewal-hooks/deploy
cat >/etc/letsencrypt/renewal-hooks/deploy/s2l-nginx.sh <<'EOF'
#!/bin/sh
set -eu
/usr/sbin/nginx -t
systemctl reload nginx
EOF
chmod 755 /etc/letsencrypt/renewal-hooks/deploy/s2l-nginx.sh
systemctl daemon-reload
if [[ "$CERT_TYPE" == letsencrypt && "$CERTBOT_READY" == 1 ]]; then
  systemctl enable --now s2l-cert-renew.timer
else
  systemctl disable --now s2l-cert-renew.timer
fi
trap - ERR
echo "HTTPS 已启用：https://${IP}"
echo "s2l 后端仅监听本机；TCP 80 用于证书验证和跳转，TCP 443 用于管理。"
if [[ "$CERT_TYPE" == letsencrypt && "$CERTBOT_READY" == 1 ]]; then
  echo "自动续期：s2l-cert-renew.timer；配置备份：$BACKUP"
else
  echo "自签证书或续期组件未就绪；请在修复证书申请条件后重新执行本脚本。配置备份：$BACKUP"
fi
