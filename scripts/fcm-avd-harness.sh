#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ANDROID_DIR="$REPO_ROOT/android"
PACKAGE="top.miceworld.pocketbridge"
RUNTIME_DIR="${PB_HARNESS_RUNTIME_DIR:-$REPO_ROOT/.tmp/pb-avd-harness}"
BIN_DIR="$RUNTIME_DIR/bin"
GOOD_RELAY_URL="${PB_HARNESS_GOOD_RELAY_URL:-ws://223.109.140.254:18080/ws}"
BAD_RELAY_URL="${PB_HARNESS_BAD_RELAY_URL:-ws://223.109.140.254:18081/ws}"
AGENT_RELAY_URL="${PB_HARNESS_AGENT_RELAY_URL:-$GOOD_RELAY_URL}"
SERVICE_ACCOUNT_FILE="${PB_HARNESS_FIREBASE_SERVICE_ACCOUNT:-$HOME/.config/pocket-bridge/firebase-service-account.json}"
AGENT_SOURCE_CONFIG="${PB_HARNESS_AGENT_SOURCE_CONFIG:-/etc/pocket-bridge/agentd-cagedbird.json}"
USE_SYSTEM_AGENT="${PB_HARNESS_USE_SYSTEM_AGENT:-1}"
PHONE_DEVICE_ID="${PB_HARNESS_PHONE_DEVICE_ID:-phone_avd}"
ADB_SERIAL="${ADB_SERIAL:-}"

AGENT_PID_FILE="$RUNTIME_DIR/agent.pid"
AGENT_LOG="$RUNTIME_DIR/agent.log"
AGENT_SOCKET="${PB_HARNESS_AGENT_SOCKET:-$HOME/.local/state/pocket-bridge/agent.sock}"
TOKEN_STORE="${PB_HARNESS_TOKEN_STORE:-$HOME/.local/state/pocket-bridge/fcm-tokens.json}"
AGENT_CONFIG="$RUNTIME_DIR/agent.json"
AGENT_BIN="$BIN_DIR/pocket-bridge-agentd"
PB_BIN="$BIN_DIR/pb"
APK_PATH="$ANDROID_DIR/app/build/outputs/apk/debug/app-debug.apk"
UI_DUMP_REMOTE="/sdcard/pb_harness_ui.xml"

usage() {
  cat <<USAGE
用法:
  scripts/fcm-avd-harness.sh <command>

命令:
  up        复用本机真实 laptop agent（默认）或启动临时 agent，构建并安装 APK，provision 到 JDCloud relay，等到 token + 在线就绪
  ws        在在线 WebSocket 路径下发一条通知并验证 UI / notification 证据
  fcm       切到坏 relay，强制离线后通过 laptop agent 触发 FCM fallback 并验证
  restore   把 AVD 恢复到 JDCloud relay 在线状态
  status    打印 harness 运行状态、token、prefs、最近日志
  full      依次执行 up -> ws -> fcm -> restore
  down      停止本地 harness agent

环境变量:
  ADB_SERIAL                         指定 adb serial；默认自动选择唯一在线设备
  PB_HARNESS_RUNTIME_DIR             运行时目录，默认 $REPO_ROOT/.tmp/pb-avd-harness
  PB_HARNESS_GOOD_RELAY_URL          在线场景使用的真实 relay URL，默认 ws://223.109.140.254:18080/ws
  PB_HARNESS_BAD_RELAY_URL           离线场景使用的坏 relay URL，默认 ws://223.109.140.254:18081/ws
  PB_HARNESS_AGENT_RELAY_URL         laptop agent 连接的 relay URL，默认跟 GOOD_RELAY_URL 一致
  PB_HARNESS_AGENT_SOURCE_CONFIG     优先读取的本机真实 agent 配置，默认 /etc/pocket-bridge/agentd-cagedbird.json
  PB_HARNESS_USE_SYSTEM_AGENT        默认 1，直接复用本机真实 agent/socket；设为 0 才启动临时 harness agent
  PB_HARNESS_AGENT_SOCKET            agent unix socket，默认 ~/.local/state/pocket-bridge/agent.sock
  PB_HARNESS_TOKEN_STORE             token store，默认 ~/.local/state/pocket-bridge/fcm-tokens.json
  PB_HARNESS_PHONE_DEVICE_ID         AVD 调试设备 ID，默认 phone_avd，避免和真机 phone 互相覆盖
  PB_HARNESS_FIREBASE_SERVICE_ACCOUNT Firebase service account JSON，默认 ~/.config/pocket-bridge/firebase-service-account.json
USAGE
}

note() {
  printf '[pb-harness] %s\n' "$*"
}

die() {
  printf '[pb-harness] ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

extract_project_id() {
  python3 - <<'PY' "$REPO_ROOT/android/app/google-services.json"
import json, sys
with open(sys.argv[1], 'r', encoding='utf-8') as f:
    data = json.load(f)
print(data['project_info']['project_id'])
PY
}

extract_phone_private_key() {
  python3 - <<'PY' "$REPO_ROOT/android/app/src/main/java/top/miceworld/pocketbridge/BridgePrefs.kt"
import re, sys
text = open(sys.argv[1], 'r', encoding='utf-8').read()
m = re.search(r'DEFAULT_PHONE_PRIVATE_KEY_BASE64\s*=\s*\n\s*"([^"]+)"', text)
if not m:
    raise SystemExit('failed to extract DEFAULT_PHONE_PRIVATE_KEY_BASE64')
print(m.group(1))
PY
}

extract_agent_private_key() {
  python3 - <<'PY' "$AGENT_SOURCE_CONFIG" "$REPO_ROOT/configs/agent.laptop.fcm.example.json"
import json, sys
for path in sys.argv[1:]:
    try:
        with open(path, 'r', encoding='utf-8') as f:
            data = json.load(f)
    except FileNotFoundError:
        continue
    if data.get('private_key_base64'):
        print(data['private_key_base64'])
        raise SystemExit(0)
raise SystemExit('failed to extract agent private_key_base64')
PY
}

PROJECT_ID=""
PHONE_PRIVATE_KEY_BASE64=""
AGENT_PRIVATE_KEY_BASE64=""

load_repo_defaults() {
  [[ -n "$PROJECT_ID" ]] || PROJECT_ID="$(extract_project_id)"
  [[ -n "$PHONE_PRIVATE_KEY_BASE64" ]] || PHONE_PRIVATE_KEY_BASE64="$(extract_phone_private_key)"
  [[ -n "$AGENT_PRIVATE_KEY_BASE64" ]] || AGENT_PRIVATE_KEY_BASE64="$(extract_agent_private_key)"
}

detect_serial() {
  if [[ -n "$ADB_SERIAL" ]]; then
    return
  fi
  mapfile -t devices < <(adb devices | awk 'NR > 1 && $2 == "device" {print $1}')
  if (( ${#devices[@]} == 1 )); then
    ADB_SERIAL="${devices[0]}"
    return
  fi
  if (( ${#devices[@]} == 0 )); then
    die '没有在线 adb 设备'
  fi
  die "检测到多个 adb 设备，请显式设置 ADB_SERIAL: ${devices[*]}"
}

adb_cmd() {
  if [[ -n "$ADB_SERIAL" ]]; then
    adb -s "$ADB_SERIAL" "$@"
  else
    adb "$@"
  fi
}

local_pb() {
  if [[ ! -x "$PB_BIN" ]]; then
    die "pb harness binary missing: $PB_BIN; 先运行 scripts/fcm-avd-harness.sh up"
  fi
  "$PB_BIN" --socket "$AGENT_SOCKET" "$@"
}

local_pb_retry() {
  local attempts="$1"
  shift
  local i
  for ((i = 1; i <= attempts; i++)); do
    if local_pb "$@"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

grep_file_contains() {
  local path="$1"
  local needle="$2"
  [[ -f "$path" ]] && grep -Fq "$needle" "$path"
}

prefs_xml() {
  adb_cmd shell "run-as $PACKAGE cat shared_prefs/pocket_bridge.xml" 2>/dev/null
}

prefs_contains() {
  local needle="$1"
  prefs_xml | grep -Fq "$needle"
}

extract_pref_value() {
  local key="$1"
  PREFS_XML="$(prefs_xml)" python3 -c '
import os, sys, xml.etree.ElementTree as ET
key = sys.argv[1]
text = os.environ.get("PREFS_XML", "").strip()
if not text:
    raise SystemExit(1)
root = ET.fromstring(text)
for child in root.findall("string"):
    if child.attrib.get("name") == key:
        print(child.text or "")
        raise SystemExit(0)
raise SystemExit(1)
' "$key"
}

ui_dump() {
  adb_cmd shell uiautomator dump "$UI_DUMP_REMOTE" >/dev/null
  adb_cmd shell cat "$UI_DUMP_REMOTE"
}

ui_contains() {
  local needle="$1"
  ui_dump | grep -Fq "$needle"
}

notification_contains() {
  local needle="$1"
  adb_cmd shell dumpsys notification --noredact | grep -Fq "$needle"
}

token_store_contains() {
  local needle="$1"
  [[ -f "$TOKEN_STORE" ]] && grep -Fq "$needle" "$TOKEN_STORE"
}

seed_push_token_from_device() {
  local token
  token="$(extract_pref_value fcm_token || true)"
  [[ -n "$token" ]] || die "AVD prefs 里没有 fcm_token，无法为 $PHONE_DEVICE_ID 种入 token store"
  mkdir -p "$(dirname "$TOKEN_STORE")"
  python3 - <<'PY' "$TOKEN_STORE" "$PHONE_DEVICE_ID" "$token" "$PACKAGE"
import json, os, sys
from datetime import datetime, timezone

path, device_id, token, package_name = sys.argv[1:5]
data = {}
if os.path.exists(path):
    with open(path, "r", encoding="utf-8") as f:
        raw = f.read().strip()
        if raw:
            data = json.loads(raw)
data[device_id] = {
    "provider": "fcm",
    "token": token,
    "platform": "android",
    "package_name": package_name,
    "updated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
PY
  note "seeded token store entry for $PHONE_DEVICE_ID from current AVD prefs"
}

wait_for_device_fcm_token() {
  wait_for 90 'AVD prefs contain fcm_token' bash -lc "scripts/fcm-avd-harness.sh __has-fcm-token"
}

wait_for() {
  local timeout_s="$1"
  shift
  local description="$1"
  shift
  local deadline=$((SECONDS + timeout_s))
  until "$@"; do
    if (( SECONDS >= deadline )); then
      die "等待超时: $description"
    fi
    sleep 1
  done
  note "ok: $description"
}

process_running() {
  local pid_file="$1"
  [[ -f "$pid_file" ]] || return 1
  local pid
  pid="$(cat "$pid_file")"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null
}

stop_process() {
  local name="$1"
  local pid_file="$2"
  if process_running "$pid_file"; then
    local pid
    pid="$(cat "$pid_file")"
    note "stopping $name pid=$pid"
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -f "$pid_file"
}

write_runtime_configs() {
  mkdir -p "$RUNTIME_DIR"
  export AGENT_CONFIG AGENT_RELAY_URL AGENT_SOCKET TOKEN_STORE SERVICE_ACCOUNT_FILE PROJECT_ID AGENT_PRIVATE_KEY_BASE64
  python3 - <<'PY'
import json, os
agent_path = os.environ['AGENT_CONFIG']
agent = {
    'device_id': 'laptop',
    'private_key_base64': os.environ['AGENT_PRIVATE_KEY_BASE64'],
    'relay_url': os.environ['AGENT_RELAY_URL'],
    'unix_socket': os.environ['AGENT_SOCKET'],
    'targets': {'phone': 'phone'},
    'incoming_notify_command': ['/usr/bin/notify-send'],
    'log_incoming': True,
    'fcm': {
        'project_id': os.environ['PROJECT_ID'],
        'credentials_file': os.environ['SERVICE_ACCOUNT_FILE'],
        'token_store_path': os.environ['TOKEN_STORE'],
    },
}
with open(agent_path, 'w', encoding='utf-8') as f:
    json.dump(agent, f, indent=2)
    f.write('\n')
PY
}

build_go_binaries() {
  note 'building local Go binaries for harness'
  mkdir -p "$BIN_DIR"
  (
    cd "$REPO_ROOT"
    go build -o "$PB_BIN" ./cmd/pb
    if [[ "$USE_SYSTEM_AGENT" != "1" ]]; then
      go build -o "$AGENT_BIN" ./cmd/agentd
    fi
  )
}

start_daemons() {
  if [[ "$USE_SYSTEM_AGENT" == "1" ]]; then
    note "using system agent socket $AGENT_SOCKET"
    wait_for 15 'system agent socket ready' test -S "$AGENT_SOCKET"
    wait_for 15 'system agent connected to relay' bash -lc "'$PB_BIN' --socket '$AGENT_SOCKET' status | grep -Fq 'connected=true'"
    return
  fi
  stop_process agent "$AGENT_PID_FILE"
  rm -f "$AGENT_LOG" "$AGENT_SOCKET"
  write_runtime_configs
  : > "$AGENT_LOG"

  note "starting local laptop agent -> $AGENT_RELAY_URL"
  setsid bash -lc "echo \$\$ > '$AGENT_PID_FILE' && exec '$AGENT_BIN' -config '$AGENT_CONFIG' >> '$AGENT_LOG' 2>&1" </dev/null &
  wait_for 30 'agent unix socket ready' grep_file_contains "$AGENT_LOG" "agent unix socket ready: $AGENT_SOCKET"
  wait_for 30 'agent connected to relay' grep_file_contains "$AGENT_LOG" 'agent connected to relay as laptop'
}

build_android() {
  note 'building Android debug APK'
  (cd "$ANDROID_DIR" && ./gradlew :app:assembleDebug >/dev/null)
}

install_app() {
  note "installing APK to $ADB_SERIAL"
  adb_cmd install -r "$APK_PATH" >/dev/null
  adb_cmd shell pm grant "$PACKAGE" android.permission.POST_NOTIFICATIONS >/dev/null 2>&1 || true
}

provision_app() {
  local relay_url="$1"
  note "provisioning app relay_url=$relay_url"
  "$REPO_ROOT/scripts/provision-android-debug.sh" \
    --serial "$ADB_SERIAL" \
    --relay-url "$relay_url" \
    --private-key-base64 "$PHONE_PRIVATE_KEY_BASE64" \
    --device-id "$PHONE_DEVICE_ID" \
    --notify-target laptop \
    --start >/dev/null
}

wait_online() {
  wait_for 45 'prefs switched to good relay' prefs_contains "$GOOD_RELAY_URL"
  wait_for 45 'AVD UI shows connected state' ui_contains '已连接'
  wait_for 45 'AVD UI shows configured device' ui_contains "device=$PHONE_DEVICE_ID"
  wait_for 45 'AVD UI shows JDCloud relay' ui_contains 'relay=ws://223.109.140.254:18080/ws'
}

wait_offline() {
  wait_for 45 'prefs switched to bad relay' prefs_contains "$BAD_RELAY_URL"
  wait_for 45 'AVD UI shows disconnected state' ui_contains '未连接'
  wait_for 45 'AVD UI shows configured device while offline' ui_contains "device=$PHONE_DEVICE_ID"
  wait_for 45 'AVD UI records bad relay host/port' ui_contains '223.109.140.254:18081'
}

command_up() {
  require_cmd adb
  require_cmd go
  require_cmd python3
  [[ -f "$REPO_ROOT/android/app/google-services.json" ]] || die '缺少 android/app/google-services.json'
  [[ -f "$SERVICE_ACCOUNT_FILE" ]] || die "缺少 Firebase service account: $SERVICE_ACCOUNT_FILE"
  detect_serial
  load_repo_defaults
  build_android
  build_go_binaries
  start_daemons
  install_app
  provision_app "$GOOD_RELAY_URL"
  wait_online
  wait_for_device_fcm_token
  seed_push_token_from_device
  wait_for 15 'token store has phone_avd entry' token_store_contains "\"$PHONE_DEVICE_ID\""
  note 'up complete'
}

command_ws() {
  detect_serial
  local title="HarnessWS-$(date +%s)"
  local body='websocket-path-alive'
  note "sending WebSocket notify target=$PHONE_DEVICE_ID title=$title"
  local_pb_retry 10 notify "$PHONE_DEVICE_ID" "$title" "$body" >/dev/null
  wait_for 20 'recent activity prefs capture websocket title' prefs_contains "$title"
  wait_for 20 'home timeline shows websocket notification title' ui_contains "$title"
  note 'websocket verification complete'
}

command_fcm() {
  detect_serial
  load_repo_defaults
  provision_app "$BAD_RELAY_URL"
  wait_offline
  wait_for_device_fcm_token
  seed_push_token_from_device
  local title="HarnessFCM-$(date +%s)"
  local body='fcm-offline-path-alive'
  note "sending offline notify target=$PHONE_DEVICE_ID title=$title"
  local_pb_retry 10 notify "$PHONE_DEVICE_ID" "$title" "$body" >/dev/null
  wait_for 20 'recent activity prefs capture offline title' prefs_contains "$title"
  wait_for 20 'home timeline shows offline notification title' ui_contains "$title"
  wait_for 20 'notification manager shows FCM title' notification_contains "$title"
  wait_for 20 'notification manager shows FCM body' notification_contains "$body"
  note 'FCM verification complete'
}

command_restore() {
  detect_serial
  load_repo_defaults
  provision_app "$GOOD_RELAY_URL"
  wait_online
  note 'AVD restored to JDCloud relay online state'
}

command_status() {
  detect_serial
  note "serial=$ADB_SERIAL"
  echo '--- local pb status ---'
  if [[ -S "$AGENT_SOCKET" ]]; then
    local_pb status || true
  else
    echo 'agent socket missing'
  fi
  echo '--- token store ---'
  if [[ -f "$TOKEN_STORE" ]]; then
    cat "$TOKEN_STORE"
  else
    echo '(missing)'
  fi
  echo '--- app prefs ---'
  prefs_xml || true
  if [[ "$USE_SYSTEM_AGENT" != "1" ]]; then
    echo '--- agent log tail ---'
    tail -n 20 "$AGENT_LOG" 2>/dev/null || true
  fi
  echo '--- app status excerpt ---'
  ui_dump | rg -n '状态：|relay=|lastError=|收到 FCM|通知 from|已应用 adb provision' -C 0 | tail -n 40 || true
}

command_down() {
  if [[ "$USE_SYSTEM_AGENT" == "1" ]]; then
    note 'using system agent; nothing to stop'
    return
  fi
  stop_process agent "$AGENT_PID_FILE"
  note 'harness daemons stopped'
}

command_full() {
  command_up
  command_ws
  command_fcm
  command_restore
  note 'full harness verification complete'
}

main() {
  local command="${1:-}"
  case "$command" in
    __has-fcm-token)
      extract_pref_value fcm_token >/dev/null
      ;;
    up) shift; command_up "$@" ;;
    ws) shift; command_ws "$@" ;;
    fcm) shift; command_fcm "$@" ;;
    restore) shift; command_restore "$@" ;;
    status) shift; command_status "$@" ;;
    full) shift; command_full "$@" ;;
    down) shift; command_down "$@" ;;
    -h|--help|help|'') usage ;;
    *) die "未知命令: $command" ;;
  esac
}

main "$@"
