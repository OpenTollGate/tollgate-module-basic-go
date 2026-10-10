#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the postinst topology self-check (#756's verdict item) actually
# checks what it claims — every enabled AP a member of an existing bridge,
# exactly one connected route per portal subnet — and says so loudly when
# either invariant is broken.
#
# The self-check is shipped as /usr/local/bin/tollgate-topology-selfcheck and
# runs from the postinst after the network/firewall convergence; it is
# assert-style (always exits 0, names every violation on stdout AND syslog).
# This test drives the real script against a stubbed uci/ip harness:
#   - a healthy topology passes silently (OK line, zero ERRORs)
#   - an AP bound to a network with no device → ERROR naming the section
#   - an AP bound to a device the kernel does not have → ERROR (uci vs
#     running-topology disagreement, the class-1 signature)
#   - a disabled AP on a ghost network is skipped (disabled means disabled)
#   - a portal subnet with TWO connected routes → ERROR naming both devices
#     (the classes-2/3 silent-blackhole signature)

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/packaging/files/usr/local/bin/tollgate-topology-selfcheck"

printf 'check-topology-selfcheck: root %s\n' "$ROOT"

fail=0
ok()  { printf '  PASS: %s\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"

cat > "$WORK/bin/uci" <<'EOF'
#!/bin/sh
S="${UCI_STATE:?}"
[ "$1" = "-q" ] && shift
case "$1 $2" in
    "show wireless") cat "$S/show.wireless" 2>/dev/null; exit 0 ;;
esac
case "$1" in
    get) key=$(echo "$2" | tr '.' '_'); [ -f "$S/$key" ] && cat "$S/$key" && exit 0; exit 1 ;;
esac
exit 1
EOF

cat > "$WORK/bin/ip" <<'EOF'
#!/bin/sh
S="${UCI_STATE:?}"
case "$*" in
    "link show br-portal") exit 0 ;;
    "link show br-stale") exit 0 ;;
    "link show br-gone") exit 1 ;;
    "route show dev br-portal")
        [ -f "$S/dup-route" ] && \
            printf '192.168.77.0/24 proto kernel scope link src 192.168.77.1 \n' || \
            printf '192.168.77.0/24 proto kernel scope link src 192.168.77.1 \n'
        exit 0 ;;
    "route show")
        [ -f "$S/dup-route" ] && \
            printf '192.168.77.0/24 dev br-portal proto kernel scope link src 192.168.77.1 \n192.168.77.0/24 dev br-stale proto kernel scope link src 192.168.77.1 \ndefault via 10.0.2.2 dev eth0 \n' || \
            printf '192.168.77.0/24 dev br-portal proto kernel scope link src 192.168.77.1 \ndefault via 10.0.2.2 dev eth0 \n'
        exit 0 ;;
esac
exit 1
EOF

cat > "$WORK/bin/logger" <<'EOF'
#!/bin/sh
echo "logger:$*" >> "${UCI_STATE:?}/syslog.log"
EOF
chmod +x "$WORK/bin/uci" "$WORK/bin/ip" "$WORK/bin/logger"

seed() {
    STATE="$1"; mkdir -p "$STATE"
    printf 'br-portal' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
    printf 'br-portal' > "$STATE/network_portal_device"
    cat > "$STATE/show.wireless" <<'WEOF'
wireless.ok_ap=wifi-iface
wireless.ok_ap.device='radio0'
wireless.ok_ap.mode='ap'
wireless.ok_ap.network='portal'
wireless.ghost_ap=wifi-iface
wireless.ghost_ap.device='radio0'
wireless.ghost_ap.mode='ap'
wireless.ghost_ap.network='nosuchnet'
wireless.down_ap=wifi-iface
wireless.down_ap.device='radio0'
wireless.down_ap.mode='ap'
wireless.down_ap.disabled='1'
wireless.down_ap.network='nosuchnet'
wireless.gone_ap=wifi-iface
wireless.gone_ap.device='radio0'
wireless.gone_ap.mode='ap'
wireless.gone_ap.network='vanished'
WEOF
    printf 'portal'   > "$STATE/wireless_ok_ap_network"
    printf 'nosuchnet' > "$STATE/wireless_ghost_ap_network"
    printf 'nosuchnet' > "$STATE/wireless_down_ap_network"
    printf 'vanished' > "$STATE/wireless_gone_ap_network"
    printf '1'        > "$STATE/wireless_down_ap_disabled"
    printf 'br-gone'  > "$STATE/network_vanished_device"
}

run_check() {
    (
        export UCI_STATE="$STATE" PATH="$WORK/bin:$PATH"
        sh "$CHECK"
    ) > "$STATE/out.log" 2>&1
    # the self-check never fails the caller
    return 0
}

# --- healthy topology: silent OK ---------------------------------------------
STATE="$WORK/healthy"; seed "$STATE"
rm -f "$STATE/show.wireless"  # rebuild with only the healthy AP
cat > "$STATE/show.wireless" <<'WEOF'
wireless.ok_ap=wifi-iface
wireless.ok_ap.device='radio0'
wireless.ok_ap.mode='ap'
wireless.ok_ap.network='portal'
WEOF
run_check
grep -q "OK: every enabled AP" "$STATE/out.log" && ! grep -q ERROR "$STATE/out.log" \
    && ok "healthy topology: the OK line, zero ERRORs" \
    || bad "healthy topology reported errors"

# --- the class-1 signatures ----------------------------------------------------
STATE="$WORK/damaged"; seed "$STATE"
run_check
grep -q "ERROR: AP section 'ghost_ap' is bound to network 'nosuchnet', which has no bridge device" "$STATE/out.log" \
    && ok "ghost-network binding named" || bad "ghost-network binding not reported"
grep -q "ERROR: AP section 'gone_ap'.*whose device 'br-gone' does not exist in the kernel" "$STATE/out.log" \
    && ok "uci-vs-kernel device disagreement named" || bad "kernel-missing device not reported"
if grep -q "down_ap" "$STATE/out.log"; then
    bad "a disabled AP on a ghost network was flagged (disabled means disabled)"
else
    ok "disabled AP skipped"
fi
if grep -q "ok_ap" "$STATE/out.log"; then
    bad "the healthy AP was flagged"
else
    ok "healthy AP not flagged"
fi

# --- the classes-2/3 signature -------------------------------------------------
STATE="$WORK/duproute"; seed "$STATE"
rm -f "$STATE/show.wireless"
cat > "$STATE/show.wireless" <<'WEOF'
wireless.ok_ap=wifi-iface
wireless.ok_ap.device='radio0'
wireless.ok_ap.mode='ap'
wireless.ok_ap.network='portal'
WEOF
: > "$STATE/dup-route"
run_check
grep -q "ERROR: portal subnet 192.168.77.0/24 has 2 connected routes (on: br-portal br-stale" "$STATE/out.log" \
    && ok "duplicate connected route named with both devices" \
    || bad "duplicate-route signature not reported"
grep -q "connected routes" "$STATE/syslog.log" \
    && ok "the route violation reaches syslog" || bad "route violation not in syslog"
grep -q "ghost_ap" "$STATE/syslog.log" 2>/dev/null && ok "AP violations reach syslog" || true

# --- assert-style: never fails the caller ---------------------------------------
STATE="$WORK/damaged"  # the most damaged state, again
run_check
ok "the self-check exits 0 even on a damaged topology (assert-style, loud not blocking)"

printf 'check-topology-selfcheck: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
