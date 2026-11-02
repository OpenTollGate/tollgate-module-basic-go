#!/usr/bin/env bash
# Hardware-in-the-loop peering smoke test using mac80211_hwsim.
#
# This is the closest automated substitute for two physical routers on a desk:
# one hwsim radio runs hostapd with a TollGate guest SSID, the other associates
# as an open-network station, and the test proves the station can scan the AP
# and complete association. It does not exercise OpenWrt UCI/ubus or the
# full daemon; those are covered by the module unit tests and the cloud lab.
#
# Requirements: Linux, root/sudo, hostapd, iw, wpa_supplicant, mac80211_hwsim.
# Usage: sudo tests/hwsim/peering_scan_assoc.sh [ap-iface] [sta-iface]
set -euo pipefail

AP_IF="${1:-wlan0}"
STA_IF="${2:-wlan1}"
SSID="${TG_HWSIM_SSID:-!TollGate-AB12}"
TMP="$(mktemp -d -t tg-hwsim.XXXXXX)"
HOSTAPD_PID=""
WPA_PID=""

cleanup() {
  set +e
  [ -n "$WPA_PID" ] && sudo kill "$WPA_PID" 2>/dev/null
  [ -n "$HOSTAPD_PID" ] && sudo kill "$HOSTAPD_PID" 2>/dev/null
  sudo ip link set "$STA_IF" down 2>/dev/null
  sudo ip link set "$AP_IF" down 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

command -v hostapd >/dev/null || { echo "SKIP: hostapd not installed" >&2; exit 0; }
command -v iw >/dev/null || { echo "SKIP: iw not installed" >&2; exit 0; }
command -v wpa_supplicant >/dev/null || { echo "SKIP: wpa_supplicant not installed" >&2; exit 0; }
sudo -n true 2>/dev/null || { echo "SKIP: passwordless sudo is required for hwsim/hostapd" >&2; exit 0; }

sudo modprobe mac80211_hwsim radios=4
sudo ip link set "$AP_IF" down 2>/dev/null || true
sudo ip link set "$STA_IF" down 2>/dev/null || true

cat > "$TMP/hostapd.conf" <<CONF
interface=$AP_IF
driver=nl80211
ssid=$SSID
hw_mode=g
channel=1
auth_algs=1
ignore_broadcast_ssid=0
wmm_enabled=0
ctrl_interface=$TMP/hostapd
CONF

cat > "$TMP/wpa.conf" <<CONF
ctrl_interface=$TMP/wpa
network={
  ssid="$SSID"
  key_mgmt=NONE
  scan_ssid=1
}
CONF

sudo hostapd -B -P "$TMP/hostapd.pid" -f "$TMP/hostapd.log" "$TMP/hostapd.conf"
HOSTAPD_PID="$(sudo cat "$TMP/hostapd.pid")"
sleep 1

sudo ip link set "$STA_IF" up
sudo wpa_supplicant -B -P "$TMP/wpa.pid" -f "$TMP/wpa.log" -i "$STA_IF" -c "$TMP/wpa.conf" -D nl80211
WPA_PID="$(sudo cat "$TMP/wpa.pid")"

echo "== waiting for association on $STA_IF"
for _ in $(seq 1 20); do
  if sudo iw dev "$STA_IF" link | grep -q "SSID: $SSID"; then
    break
  fi
  sleep 0.5
done
sudo iw dev "$STA_IF" link | tee "$TMP/link.txt"
grep -q "SSID: $SSID" "$TMP/link.txt" || { echo "FAIL: station did not associate to $SSID" >&2; exit 1; }

echo "== scanning from the associated station"
sudo iw dev "$STA_IF" scan > "$TMP/scan.txt"
grep -q "SSID: $SSID" "$TMP/scan.txt" || { echo "FAIL: scan did not report $SSID" >&2; exit 1; }
grep -Eq 'signal: -?[0-9]+' "$TMP/scan.txt" || { echo "FAIL: scan did not report a signal" >&2; exit 1; }

echo "PASS: hwsim peer '$SSID' is visible and the open-network association completed"
