#!/usr/bin/env bash
# Detached, commit-pinned upgrades of the default /opt/s2l deployment.
run_update() {
set -euo pipefail
umask 077
text() {
  case "${_S2L_INSTALL_LANG:-en}" in
    zh-Hant) printf '%s' "$2" ;;
    zh-Hans) printf '%s' "$3" ;;
    *) printf '%s' "$1" ;;
  esac
}
[[ "$(id -u)" -eq 0 ]] || { echo "$(text "Run as root: curl ... | sudo bash" "請以 root 執行：curl ... | sudo bash" "请使用 root：curl ... | sudo bash")" >&2; exit 1; }
[[ -f /opt/s2l/config.json && -x /opt/s2l/s2l ]] || { echo "$(text "[ERROR] No S2L installation found; use the fresh installer." "[ERROR] 找不到既有 S2L 安裝，請使用全新安裝腳本。" "[ERROR] 未找到现有 S2L 安装，请使用新安装脚本。")" >&2; exit 1; }
for command in curl tar python3; do command -v "$command" >/dev/null || { echo "$(text "[ERROR] Missing $command" "[ERROR] 缺少 $command" "[ERROR] 缺少 $command")" >&2; exit 1; }; done
export _S2L_INSTALL_LANG="$(python3 - /opt/s2l/config.json <<'PYLANG'
import json,sys
try:
 value=json.load(open(sys.argv[1],encoding='utf-8')).get('ui_language')
except (OSError,ValueError,AttributeError):value=None
print({'en':'en','zh-TW':'zh-Hant','zh-CN':'zh-Hans'}.get(value,'en') if isinstance(value,str) else 'en')
PYLANG
)"
S2L_REF="${S2L_REF:-${S2L_COMMIT:-}}"
[[ -z "$S2L_REF" || "$S2L_REF" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || { echo "$(text "[ERROR] Invalid S2L_REF" "[ERROR] 無效的 S2L_REF" "[ERROR] 无效的 S2L_REF")" >&2; exit 1; }
[[ -z "${S2L_COMMIT:-}" || "$S2L_COMMIT" =~ ^[a-f0-9]{40}$ ]] || { echo "$(text "[ERROR] Invalid S2L_COMMIT" "[ERROR] 無效的 S2L_COMMIT" "[ERROR] 无效的 S2L_COMMIT")" >&2; exit 1; }
[[ -z "${S2L_VERSION:-}" || "$S2L_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$ ]] || { echo "$(text "[ERROR] Invalid S2L_VERSION" "[ERROR] 無效的 S2L_VERSION" "[ERROR] 无效的 S2L_VERSION")" >&2; exit 1; }
job_root=/var/lib/s2l/cli-update
write_status() {
  [[ "${S2L_CLI_UPDATE:-0}" == 1 ]] || return 0
  python3 - "$job_root" "$1" "$2" "${S2L_VERSION:-}" "${S2L_COMMIT:-}" <<'PY'
import datetime,json,os,pathlib,sys,tempfile
root=pathlib.Path(sys.argv[1]);stamp=datetime.datetime.now(datetime.timezone.utc).isoformat()
fd,name=tempfile.mkstemp(prefix='.result-',dir=root)
try:
 with os.fdopen(fd,'w') as f:
  json.dump(dict(status=sys.argv[2],exit_code=int(sys.argv[3]),version=sys.argv[4],commit=sys.argv[5],updated_at=stamp),f);f.flush();os.fsync(f.fileno())
 os.replace(name,root/'result.json')
finally:
 if os.path.exists(name):os.unlink(name)
PY
}
# Web updates already have an independent systemd worker. CLI updates must
# survive loss of SSH, including SSH carried by the mapping being restarted.
if [[ "${S2L_CLI_UPDATE:-0}" != 1 && -z "${S2L_WEB_UPDATE_ID:-}" ]]; then
  for command in systemctl systemd-run flock; do command -v "$command" >/dev/null || { echo "$(text "[ERROR] Missing $command; existing services unchanged." "[ERROR] 缺少 $command；既有服務未變更。" "[ERROR] 缺少 $command；现有服务未修改。")" >&2; exit 1; }; done
  exec 8>/run/s2l-cli-update-launch.lock
  flock -n 8 || { echo "$(text "[ERROR] Another upgrade launch is in progress." "[ERROR] 另一個升級啟動請求正在處理。" "[ERROR] 另一个升级启动请求正在处理。")" >&2; exit 1; }
  if systemctl is-active --quiet s2l-cli-update.service; then echo "$(text "[ERROR] A background upgrade is running; check /var/lib/s2l/cli-update/result.json" "[ERROR] 背景升級正在執行，請查看 /var/lib/s2l/cli-update/result.json" "[ERROR] 后台升级正在运行，请查看 /var/lib/s2l/cli-update/result.json")" >&2; exit 1; fi
  python3 - "$job_root" <<'PY'
import os,pathlib,sys
p=pathlib.Path(sys.argv[1])
if p.is_symlink():raise SystemExit('unsafe update directory')
p.mkdir(parents=True,exist_ok=True,mode=0o700)
if not p.is_dir() or p.stat().st_uid!=os.geteuid():raise SystemExit('unsafe update directory owner')
p.chmod(0o700)
for name in ['runner.sh','update.log','result.json']:
 if (p/name).is_symlink():raise SystemExit('unsafe update file')
PY
  # Functions are already parsed when bash executes a piped script. Stage the
  # complete runner rather than relying on a source path or the SSH stdin.
  { printf '%s\n' '#!/usr/bin/env bash'; declare -f run_update; printf '%s\n' 'run_update "$@"'; } > "$job_root/runner.sh"
  chmod 700 "$job_root/runner.sh"
  : > "$job_root/update.log"
  chmod 600 "$job_root/update.log"
  S2L_CLI_UPDATE=1 write_status queued 0
  systemctl reset-failed s2l-cli-update.service >/dev/null 2>&1 || true
  if ! systemd-run --quiet --collect --unit=s2l-cli-update.service --property=Type=exec --property=User=root --property=WorkingDirectory=/opt/s2l --property=KillMode=control-group --property=UMask=0077 --property=RuntimeMaxSec=3600 --property=TimeoutStopSec=120 --property="StandardOutput=append:$job_root/update.log" --property=StandardError=inherit /usr/bin/env S2L_CLI_UPDATE=1 HOME=/root "S2L_REF=$S2L_REF" "S2L_VERSION=${S2L_VERSION:-}" "S2L_COMMIT=${S2L_COMMIT:-}" /bin/bash "$job_root/runner.sh"; then
    S2L_CLI_UPDATE=1 write_status failed 1
    echo "$(text "[ERROR] Cannot start the background upgrade; existing services unchanged." "[ERROR] 無法啟動背景升級；既有服務未變更。" "[ERROR] 无法启动后台升级；现有服务未修改。")" >&2
    exit 1
  fi
  echo "$(text "[INFO] Background upgrade started; SSH disconnection does not cancel it. Service replacement disconnects L2TP and SSH carried by its mappings." "[INFO] 背景升級已啟動，SSH 中斷不會停止任務。服務切換會中斷 L2TP 及經映射連線的 SSH。" "[INFO] 后台升级已启动，SSH 断开不会中止任务。服务切换会断开 L2TP 及经映射连接的 SSH。")"
  echo "$(text "[INFO] Progress: tail -f $job_root/update.log" "[INFO] 進度：tail -f $job_root/update.log" "[INFO] 进度：tail -f $job_root/update.log")"
  echo "$(text "[INFO] Result: cat $job_root/result.json (only succeeded means success)" "[INFO] 結果：cat $job_root/result.json（succeeded 才表示成功）" "[INFO] 结果：cat $job_root/result.json（succeeded 才表示成功）")"
  exit 0
fi
work="$(mktemp -d /tmp/s2l-download.XXXXXXXX)"
finish() {
  local code=$?
  trap - EXIT INT TERM
  if (( code == 0 )); then write_status succeeded 0 || true; else write_status failed "$code" || true; fi
  [[ "$work" == /tmp/s2l-download.* ]] && rm -rf -- "$work"
  exit "$code"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
write_status running 0
mkdir "$work/source"
# Resolve the stable release once, then download that immutable commit. Explicit
# refs remain available for reviewed source builds; they never masquerade as tags.
if [[ -z "$S2L_REF" ]]; then
  curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 https://api.github.com/repos/lucaswren/s2l/releases/latest -o "$work/release.json"
  S2L_REF="$(python3 - "$work/release.json" <<'PY'
import json,re,sys
r=json.load(open(sys.argv[1]));tag=r.get('tag_name','')
if r.get('draft') or r.get('prerelease') or not isinstance(tag,str) or not re.fullmatch(r'v?[0-9]+\.[0-9]+\.[0-9]+',tag):raise SystemExit('invalid stable release')
print(tag)
PY
)"
  S2L_VERSION="$S2L_REF"
fi
if [[ -z "${S2L_COMMIT:-}" ]]; then
  curl -fsSL --retry 2 --connect-timeout 15 --max-time 60 "https://api.github.com/repos/lucaswren/s2l/commits/$S2L_REF" -o "$work/commit.json"
  S2L_COMMIT="$(python3 - "$work/commit.json" <<'PY'
import json,re,sys
sha=json.load(open(sys.argv[1])).get('sha','')
if not isinstance(sha,str) or not re.fullmatch('[a-f0-9]{40}',sha):raise SystemExit('invalid release commit')
print(sha)
PY
)"
fi
S2L_VERSION="${S2L_VERSION:-$S2L_REF}"
export S2L_REF S2L_COMMIT S2L_VERSION
write_status running 0
echo "$(text "[INFO] Downloading S2L $S2L_VERSION ($S2L_COMMIT); existing services remain running..." "[INFO] 下載 S2L $S2L_VERSION ($S2L_COMMIT)，既有服務繼續執行..." "[INFO] 下载 S2L $S2L_VERSION ($S2L_COMMIT)，现有服务继续运行...")"
curl -fSL --retry 2 --connect-timeout 15 --max-time 180 "https://codeload.github.com/lucaswren/s2l/tar.gz/$S2L_COMMIT" -o "$work/source.tar.gz"
tar -xzf "$work/source.tar.gz" --strip-components=1 -C "$work/source"
[[ -f "$work/source/script/update.sh" ]] || { echo "$(text "[ERROR] The selected version does not support managed upgrades." "[ERROR] 所選版本不支援一鍵升級。" "[ERROR] 所选版本不支持一键升级。")" >&2; exit 1; }
bash "$work/source/script/update.sh"

}
run_update "$@"
