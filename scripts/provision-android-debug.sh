#!/usr/bin/env bash
set -euo pipefail

PACKAGE="top.miceworld.pocketbridge"
SERIAL=""
RELAY_URL=""
DEVICE_ID="phone"
PRIVATE_KEY_BASE64=""
NOTIFY_TARGET="laptop"
START_APP=0

usage() {
  cat <<'EOF'
用法:
  scripts/provision-android-debug.sh \
    --relay-url ws://your-relay-host:18080/ws \
    --private-key-base64 BASE64 \
    [--device-id phone] \
    [--notify-target laptop] \
    [--serial SERIAL] \
    [--package top.miceworld.pocketbridge] \
    [--start]
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --relay-url)
      RELAY_URL="${2:-}"
      shift 2
      ;;
    --device-id)
      DEVICE_ID="${2:-}"
      shift 2
      ;;
    --private-key-base64)
      PRIVATE_KEY_BASE64="${2:-}"
      shift 2
      ;;
    --notify-target)
      NOTIFY_TARGET="${2:-}"
      shift 2
      ;;
    --serial)
      SERIAL="${2:-}"
      shift 2
      ;;
    --package)
      PACKAGE="${2:-}"
      shift 2
      ;;
    --start)
      START_APP=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$RELAY_URL" || -z "$PRIVATE_KEY_BASE64" ]]; then
  usage >&2
  exit 1
fi

if ! command -v adb >/dev/null 2>&1; then
  echo "错误: 找不到 adb" >&2
  exit 1
fi

xml_escape() {
  printf '%s' "$1" | sed \
    -e 's/&/\&amp;/g' \
    -e 's/</\&lt;/g' \
    -e 's/>/\&gt;/g' \
    -e "s/'/\&apos;/g" \
    -e 's/"/\&quot;/g'
}

ADB=(adb)
if [[ -n "$SERIAL" ]]; then
  ADB+=( -s "$SERIAL" )
fi

tmp_xml="$(mktemp)"
cleanup() {
  rm -f "$tmp_xml"
}
trap cleanup EXIT

cat >"$tmp_xml" <<EOF
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <string name="relay_url">$(xml_escape "$RELAY_URL")</string>
    <string name="device_id">$(xml_escape "$DEVICE_ID")</string>
    <string name="private_key_base64">$(xml_escape "$PRIVATE_KEY_BASE64")</string>
    <string name="notify_target">$(xml_escape "$NOTIFY_TARGET")</string>
</map>
EOF

"${ADB[@]}" wait-for-device
"${ADB[@]}" shell am force-stop "$PACKAGE" >/dev/null 2>&1 || true
"${ADB[@]}" push "$tmp_xml" /data/local/tmp/pocket_bridge.xml >/dev/null
"${ADB[@]}" shell "run-as $PACKAGE mkdir -p shared_prefs && run-as $PACKAGE cp /data/local/tmp/pocket_bridge.xml shared_prefs/pocket_bridge.xml && run-as $PACKAGE chmod 600 shared_prefs/pocket_bridge.xml"
"${ADB[@]}" shell rm -f /data/local/tmp/pocket_bridge.xml >/dev/null 2>&1 || true

if (( START_APP )); then
  "${ADB[@]}" shell pm grant "$PACKAGE" android.permission.POST_NOTIFICATIONS >/dev/null 2>&1 || true
  "${ADB[@]}" shell am start -S -W \
    -n "$PACKAGE/.MainActivity" \
    -a top.miceworld.pocketbridge.action.PROVISION \
    --es relay_url "$RELAY_URL" \
    --es device_id "$DEVICE_ID" \
    --es private_key_base64 "$PRIVATE_KEY_BASE64" \
    --es notify_target "$NOTIFY_TARGET" \
    --ez auto_start true >/dev/null
fi

echo "已写入 $PACKAGE 调试配置: relay_url=$RELAY_URL device_id=$DEVICE_ID notify_target=$NOTIFY_TARGET"
