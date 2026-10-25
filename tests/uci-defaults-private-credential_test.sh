#!/usr/bin/env bash
# Offline test for the private-WiFi credential behaviour in setup_private_network()
# (99-tollgate-setup) — no router, no network.
#
# The package sets a FIXED default passphrase (TollGate) when the private AP has
# none, and ENABLES the AP — rather than leaving it disabled (rc2/rc3) or minting
# a random one (pre-rc2). A random passphrase is one the operator cannot know,
# which is exactly the lockout the admin-credential work exists to prevent; the
# fixed default is one they can always read, and the admin/config UI forces them
# to choose their own at first login. The package must therefore:
#   * set the FIXED default (TollGate) when no key exists, and ENABLE the AP;
#   * PRESERVE an existing passphrase (never rewrite it);
#   * NEVER read a passphrase from TOLLGATE_PRIVATE_WIFI_PASSWORD or a staged
#     /etc/tollgate/wifi-password file (an install-time value the operator never
#     chose is how a router gets locked out).
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
export PRIVATE_WIFI_PROVISIONAL_MARKER="$TMP/private-wifi-credential-provisional"
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
    rm -f "$PRIVATE_WIFI_PROVISIONAL_MARKER"
    R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
}

echo "== with no passphrase the package sets the FIXED default and ENABLES the AP"
seed ""
run_private
[ "$(state_key)" = "TollGate" ] \
    && ok "the fixed default ('TollGate') is written" \
    || bad "the default was not applied (key='$(state_key)')"
[ "$(state_dis)" = "0" ] \
    && ok "the private AP is enabled" \
    || bad "the private AP is not enabled (disabled='$(state_dis)')"
[ "$(state_key1)" = "TollGate" ] && [ "$(state_dis1)" = "0" ] \
    && ok "the 5 GHz private AP is also set to the default and enabled" \
    || bad "the 5 GHz AP is wrong (key='$(state_key1)' disabled='$(state_dis1)')"
grep -q 'using the default (TollGate)' "$LOGFILE" \
    && ok "the log tells the operator the default was applied" \
    || bad "no log line explaining the default"
[ -f "$PRIVATE_WIFI_PROVISIONAL_MARKER" ] \
    && ok "the private-WiFi provisional marker was written (the UI forces a change)" \
    || bad "no private-WiFi provisional marker was written"

echo "== a supplied TOLLGATE_PRIVATE_WIFI_PASSWORD does NOT reach the AP"
seed ""
: > "$LOGFILE"
R2G=radio0 R5G=radio1 TOLLGATE_PRIVATE_WIFI_PASSWORD='ChosenByEnv-1' setup_private_network >/dev/null 2>&1
[ "$(state_key)" = "TollGate" ] \
    && ok "the env value does not reach the private AP (the fixed default stands)" \
    || bad "TOLLGATE_PRIVATE_WIFI_PASSWORD set the key ('$(state_key)')"
[ "$(state_dis)" = "0" ] && ok "the private AP is enabled" || bad "the private AP is not enabled"

echo "== a root-only /etc/tollgate/wifi-password file is IGNORED"
seed ""
: > "$LOGFILE"
printf '%s' 'ChosenByFile-1' > "$TMP/wifi-password"
R2G=radio0 R5G=radio1 PRIVATE_WIFI_PASSWORD_FILE="$TMP/wifi-password" setup_private_network >/dev/null 2>&1
[ "$(state_key)" = "TollGate" ] \
    && ok "a staged file does not reach the private AP" \
    || bad "the staged wifi-password file set the key ('$(state_key)')"
[ "$(state_dis)" = "0" ] && ok "the private AP is enabled" || bad "the private AP is not enabled"

echo "== an existing passphrase is preserved and the AP is enabled"
seed 'Existing-Key-123'
run_private
[ "$(state_key)" = "Existing-Key-123" ] \
    && ok "the existing private-WiFi key is preserved" \
    || bad "the existing key was changed ('$(state_key)')"
[ "$(state_dis)" = "0" ] \
    && ok "the private AP is enabled when a key exists" \
    || bad "the private AP is not enabled with a key present (disabled='$(state_dis)')"
[ ! -f "$PRIVATE_WIFI_PROVISIONAL_MARKER" ] \
    && ok "no provisional marker when the operator's key is preserved" \
    || bad "a provisional marker was written although a key already existed"
grep -q 'Preserving existing private WiFi key' "$LOGFILE" \
    && ok "the log reports the existing key was preserved" \
    || bad "the log does not report preservation"
if grep -q 'Existing-Key-123' "$LOGFILE"; then
    bad "the existing key VALUE was written to the install log"
else
    ok "the existing key value is not logged"
fi

echo "== the random/staged-passphrase machinery is gone from the shipped script"
if grep -q 'Generated new private WiFi key' "$ROOT/$SCRIPT"; then
    bad "the shipped script still generates a random private-WiFi key"
else
    ok "no private-WiFi key generator remains in the shipped script"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
