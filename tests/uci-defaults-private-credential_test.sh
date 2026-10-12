#!/usr/bin/env bash
# Offline test for the private-WiFi credential invariant in setup_private_network()
# (99-tollgate-setup) — no router, no network.
#
# The defect this guards: the package used to MINT a private-WiFi PSK when none
# was present (and, in the pre-RC2 design, apply one supplied via
# TOLLGATE_PRIVATE_WIFI_PASSWORD or a root-only /etc/tollgate/wifi-password
# file). A passphrase the operator never chose is a credential they cannot know,
# and a wireless passphrase the package silently set is exactly the lockout the
# admin-credential work exists to prevent. This package must therefore:
#   * NEVER generate or read a private-WiFi passphrase;
#   * PRESERVE an existing one;
#   * when none exists, leave the private AP DISABLED (the wired br-private
#     management bridge is unaffected) until the operator sets one with
#     `tollgate network private set-password`.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"
export GATEWAY_NAME="TestGate"
export RANDOM_SUFFIX="abcd"
export FAKE_IP_UPSTREAM="$TMP/ip.upstream"
export HEXDUMP_QUEUE_FILE="$TMP/hexdump.queue"
: > "$FAKE_IP_UPSTREAM"
: > "$HEXDUMP_QUEUE_FILE"

cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
case "$cmd" in
    get)
        vals="$(grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-)"
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    set)
        key="${1%%=*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list) printf '%s\n' "$1" >> "$state" ;;
    add) printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state" ;;
    delete)
        grep -v -F -e "$1=" -e "$1." "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    commit|revert|show|export) : ;;
    *) : ;;
esac
exit 0
SHIM

cat > "$TMP/bin/ip" <<'SHIM'
#!/usr/bin/env bash
lan_ip="$(grep -F -- 'network.lan.ipaddr=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1)"
[ -n "$lan_ip" ] || lan_ip="192.168.1.1"
printf '1: lo    inet 127.0.0.1/8 scope host lo\n'
printf '2: br-lan    inet %s/24 brd 192.168.1.255 scope global br-lan\n' "$lan_ip"
[ -s "$FAKE_IP_UPSTREAM" ] && cat "$FAKE_IP_UPSTREAM"
exit 0
SHIM

cat > "$TMP/bin/hexdump" <<'SHIM'
#!/usr/bin/env bash
printf '%s\n' "${HEXDUMP_FIXED:-5a}"
SHIM

chmod +x "$TMP/bin/uci" "$TMP/bin/ip" "$TMP/bin/hexdump"
PATH="$TMP/bin:$PATH"
export PATH

TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1 || true

state_key()   { grep -F -- 'wireless.private_radio0.key=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1; }
state_dis()   { grep -F -- 'wireless.private_radio0.disabled=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1; }
state_key1()  { grep -F -- 'wireless.private_radio1.key=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1; }
state_dis1()  { grep -F -- 'wireless.private_radio1.disabled=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1; }

# seed <existing-key-or-empty>: a LAN + radios, optional pre-existing passphrase.
seed() {
    : > "$UCI_STATE"
    printf 'network.lan.ipaddr=192.168.1.1\n' >> "$UCI_STATE"
    printf 'network.lan.netmask=255.255.255.0\n' >> "$UCI_STATE"
    printf 'wireless.private_radio0.ssid=test-private\n' >> "$UCI_STATE"
    [ -n "${1:-}" ] && printf 'wireless.private_radio0.key=%s\n' "$1" >> "$UCI_STATE"
    return 0
}
run_private() {
    : > "$LOGFILE"
    R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
}

echo "== with no passphrase the package sets NO key and DISABLES the private AP"
seed ""
run_private
[ -z "$(state_key)" ] \
    && ok "no private-WiFi key was written (the package mints nothing)" \
    || bad "a private-WiFi key was written ('$(state_key)') — the package set a passphrase the operator never chose"
[ "$(state_dis)" = "1" ] \
    && ok "the private AP is left disabled" \
    || bad "the private AP is not disabled (disabled='$(state_dis)') — it would be served with no usable key"
grep -q 'No private WiFi passphrase set' "$LOGFILE" \
    && ok "the log tells the operator the AP was disabled and how to set one" \
    || bad "no log line explaining the disabled private AP"

echo "== a supplied TOLLGATE_PRIVATE_WIFI_PASSWORD is IGNORED"
seed ""
: > "$LOGFILE"
R2G=radio0 R5G=radio1 TOLLGATE_PRIVATE_WIFI_PASSWORD='ChosenByEnv-1' setup_private_network >/dev/null 2>&1
[ -z "$(state_key)" ] \
    && ok "a supplied env value does not reach the private AP" \
    || bad "TOLLGATE_PRIVATE_WIFI_PASSWORD set the key ('$(state_key)')"
[ "$(state_dis)" = "1" ] && ok "the private AP stays disabled" || bad "the private AP is not disabled"

echo "== a root-only /etc/tollgate/wifi-password file is IGNORED"
seed ""
: > "$LOGFILE"
printf '%s' 'ChosenByFile-1' > "$TMP/wifi-password"
R2G=radio0 R5G=radio1 PRIVATE_WIFI_PASSWORD_FILE="$TMP/wifi-password" setup_private_network >/dev/null 2>&1
[ -z "$(state_key)" ] \
    && ok "a staged file does not reach the private AP" \
    || bad "the staged wifi-password file set the key ('$(state_key)')"
[ "$(state_dis)" = "1" ] && ok "the private AP stays disabled" || bad "the private AP is not disabled"

echo "== an existing passphrase is preserved and the AP is enabled"
seed 'Existing-Key-123'
run_private
[ "$(state_key)" = "Existing-Key-123" ] \
    && ok "the existing private-WiFi key is preserved" \
    || bad "the existing key was changed ('$(state_key)')"
[ "$(state_dis)" = "0" ] \
    && ok "the private AP is enabled when a key exists" \
    || bad "the private AP is not enabled with a key present (disabled='$(state_dis)')"

echo "== the generated-passphrase machinery is gone from the shipped script"
if grep -q 'TOLLGATE_PRIVATE_WIFI_PASSWORD\|PRIVATE_WIFI_PASSWORD_FILE\|Generated new private WiFi key' "$ROOT/$SCRIPT"; then
    bad "the shipped script still references a private-WiFi credential generator/source"
else
    ok "no private-WiFi credential source remains in the shipped script"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
