#!/usr/bin/env bash
# Offline test for the operator-pinned private address (#906) in
# packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The bug this guards: setup_private_network derives the management /24 from
# the router's own view (LAN + upstream addresses) and writes it
# unconditionally on every full setup, so an operator who hand-fixed a
# collision with `uci set network.private.ipaddr=…` had the fix reverted by
# the next reinstall/upgrade. The fix mirrors the PSK preservation: an address
# recorded by `tollgate network private set-address` in the durable store
# (/etc/config/tollgate) is used verbatim, and recheck_private_subnet refuses
# to move it even on an upstream collision.
#
# Everything runs against a fake `uci` and a fake `ip` — no router.
#
# Usage: bash tests/uci-defaults-private-addr-preserve_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# fake uci — flat "key=value" state, the subset the setup script uses.
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
state_get() { grep -F -- "$1=" "$STATE" | head -n1 | cut -d= -f2-; }
case "$cmd" in
    get)
        v=$(state_get "$1") || v=""
        [ -n "$v" ] || exit 1
        printf '%s\n' "$v"
        ;;
    set)
        key="${1%%=*}"; val="${1#*=}"
        [ "$key" = "$1" ] && exit 1
        if grep -qF -- "$key=" "$STATE"; then
            sed -i "s|^$(printf '%s' "$key" | sed 's/[.[\*^$]/\\&/g')=.*|$key=$val|" "$STATE"
        else
            printf '%s=%s\n' "$key" "$val" >> "$STATE"
        fi
        ;;
    commit) : ;;
    *) exit 0 ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

# fake ip — reports no live addresses, so upstream_networks uses the UCI WAN.
cat > "$TMP/bin/ip" <<'SHIM'
#!/usr/bin/env bash
exit 0
SHIM
chmod +x "$TMP/bin/ip"

export PATH="$TMP/bin:$PATH"
export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"

# Source once, exactly like tests/uci-defaults-private-subnet_test.sh: the
# script's top level rebinds LOGFILE (to /tmp/tollgate-setup.log), and every
# assertion below reads the rebound value through "$LOGFILE".
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1 || true

seed() { printf '%s\n' "$@" > "$UCI_STATE"; : > "$LOGFILE"; }
run_recheck() { ( TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT" >/dev/null 2>&1; recheck_private_subnet ); }
private_addr() { grep '^network.private.ipaddr=' "$UCI_STATE" | cut -d= -f2- | tail -n1; }
pick() {
    : > "$LOGFILE"
    R2G=radio0 R5G=radio1 NYM=test CODE=AB12 setup_private_network >/dev/null 2>&1
    private_addr
}

BASE_LAN=(
    'network.lan.ipaddr=192.168.1.1'
    'network.lan.netmask=255.255.255.0'
    'wireless.private_radio0.ssid=test-private'
    'wireless.private_radio0.key=Alpha-Bravo-Charlie-11'
    'network.private=interface'
)

echo "== a pinned address is used verbatim by a full setup"
seed "${BASE_LAN[@]}" 'tollgate.private.ipaddr=10.9.8.1'
got=$(pick)
[ "$got" = "10.9.8.1" ] \
    && ok "pinned 10.9.8.1 survives setup_private_network verbatim" \
    || bad "pinned address: expected 10.9.8.1, got '${got}'"
grep -q "preserved from the operator's choice" "$LOGFILE" \
    && ok "the pin is recorded in the setup log" \
    || bad "no log line recording the preserved address"

echo "== a pinned address wins even where derivation would have collided"
seed "${BASE_LAN[@]}" 'network.wan.ipaddr=10.9.8.50' 'network.wan.netmask=255.255.255.0' 'tollgate.private.ipaddr=10.9.8.1'
got=$(pick)
[ "$got" = "10.9.8.1" ] \
    && ok "pinned 10.9.8.1 kept although the WAN sits inside 10.9.8.0/24" \
    || bad "pin lost to the collision heuristic: got '${got}'"

echo "== the netmask contract is written alongside the pin"
seed "${BASE_LAN[@]}" 'tollgate.private.ipaddr=172.20.30.1'
pick >/dev/null
[ "$(grep '^network.private.netmask=' "$UCI_STATE" | cut -d= -f2- | tail -n1)" = "255.255.255.0" ] \
    && ok "netmask 255.255.255.0 written with the pinned address" \
    || bad "pinned address written without the /24 netmask"

echo "== no pin: the derivation is unchanged (regression)"
seed "${BASE_LAN[@]}"
got=$(pick)
[ "$got" = "192.168.2.1" ] \
    && ok "unpinned router still derives the LAN-adjacent 192.168.2.1" \
    || bad "derivation changed without a pin: got '${got}'"

echo "== a garbage store value is refused with a warning, not trusted"
seed "${BASE_LAN[@]}" 'tollgate.private.ipaddr=not-an-ip'
got=$(pick)
[ "$got" = "192.168.2.1" ] \
    && ok "garbage pin ignored, derivation used (192.168.2.1)" \
    || bad "garbage pin trusted or derivation broken: got '${got}'"
grep -q "not a usable IPv4 address" "$LOGFILE" \
    && ok "the refusal of the garbage pin is logged" \
    || bad "no WARNING for the garbage pin"

echo "== recheck refuses to move a pinned address on an upstream collision"
seed "${BASE_LAN[@]}" \
    'network.private.ipaddr=10.9.8.1' 'network.private.netmask=255.255.255.0' \
    'network.wan.ipaddr=10.9.8.50' 'network.wan.netmask=255.255.255.0' \
    'tollgate.private.ipaddr=10.9.8.1'
if run_recheck; then bad "recheck must not move a pinned address"; else ok "recheck reports no move (rc=1)"; fi
[ "$(private_addr)" = "10.9.8.1" ] \
    && ok "pinned address unchanged by the colliding recheck" \
    || bad "recheck moved the pinned address to '$(private_addr)'"
grep -q "pinned by the operator" "$LOGFILE" \
    && ok "the refusal is logged with the fix-it command" \
    || bad "no WARNING explaining why the address was not moved"

echo "== recheck still moves an UNPINNED colliding address (regression)"
seed "${BASE_LAN[@]}" \
    'network.private.ipaddr=192.168.2.1' 'network.private.netmask=255.255.255.0' \
    'network.wan.ipaddr=192.168.2.50' 'network.wan.netmask=255.255.255.0'
if run_recheck; then ok "unpinned collision still moves (rc=0)"; else bad "unpinned collision no longer repaired"; fi
got=$(private_addr)
[ "$got" != "192.168.2.1" ] && [ -n "$got" ] \
    && ok "unpinned private moved off 192.168.2.1 (now ${got})" \
    || bad "unpinned private still '${got}'"

echo "== the store survives sysupgrade (keep.d lists the tollgate config)"
if grep -qx '/etc/config/tollgate' packaging/files/lib/upgrade/keep.d/tollgate; then
    ok "keep.d lists /etc/config/tollgate"
else
    bad "keep.d no longer lists /etc/config/tollgate — a pin would not survive sysupgrade"
fi

printf '\nPASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
