#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: setup never binds an AP to a network that does not exist, and
# never silently renames an operator-chosen SSID (#756 class 1).
#
# Why: configure_radio_ap wrote `wireless.$iface.network=lan` and
# `ssid=$GATEWAY_NAME` unconditionally. On a split-plane DUT (dedicated OOB
# mgmt + separate portal bridge, network.lan deleted), the rebind bridged
# every AP to a network that did not exist — clients associate, DHCP dies,
# the portal is unreachable — and the staged operator SSID was silently
# renamed on top. Bench-verified on rc1, exactly:
# https://github.com/OpenTollGate/tollgate-module-basic-go/issues/756#issuecomment-6066068844
#
# The fix, pinned here against a stub uci (the ssid harness's discipline —
# the real functions sourced above the driver marker):
#   - network.lan exists  → bind 'lan' (stock behaviour, byte-identical)
#   - lan absent          → ADOPT the network owning the NDS gatewayinterface
#   - neither exists      → preserve the section's binding / leave unbound,
#                           never a ghost — and every non-stock path logs
#   - the SSID rewrite is one-way (#444 discipline): machine-shaped names
#     (empty / current derivation / *-<device code>) are re-derived; an
#     operator's name is preserved and said so

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"

printf 'check-wifi-rebind-guard: root %s\n' "$ROOT"

fail=0
ok()  { printf '  PASS: %s\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin"

cat > "$SANDBOX/bin/uci" <<'EOF'
#!/bin/sh
STATE="${UCI_STATE:?}"
[ "$1" = "-q" ] && shift
case "$1 $2" in
    "show network") cat "$STATE/show.network" 2>/dev/null; exit 0 ;;
    "show firewall") cat "$STATE/show.firewall" 2>/dev/null; exit 0 ;;
    "show wireless") cat "$STATE/show.wireless" 2>/dev/null; exit 0 ;;
    # Real uci exits 1 on a missing subtree — the existence check under
    # test keys on the exit code, not the (empty) output.
    "show network.lan")
        if [ -f "$STATE/show.network.lan" ]; then cat "$STATE/show.network.lan"; exit 0; fi
        exit 1
        ;;
esac
case "$1" in
    get)
        key=$(echo "$2" | tr '.' '_')
        [ -f "$STATE/$key" ] && cat "$STATE/$key" && exit 0
        exit 1
        ;;
    set)
        echo "$2" >> "$STATE/uci-set.log"
        key=$(echo "${2%%=*}" | tr '.' '_')
        printf '%s' "${2#*=}" > "$STATE/$key"
        exit 0
        ;;
    delete)
        echo "delete $2" >> "$STATE/uci-set.log"
        exit 0
        ;;
esac
exit 1
EOF
chmod +x "$SANDBOX/bin/uci"

awk '/^# -- driver/{exit} {print}' "$SCRIPT" > "$SANDBOX/lib.sh"

seed_iface() {
    # section, device, mode, ssid, network — both the show.wireless stream
    # (first_ap_iface_on's enumeration) and the flat get-keys every get
    # reads; a real uci answers both from one config.
    local sec="$1" dev="$2" mode="$3" ssid="$4" net="$5"
    [ -f "$STATE/show.wireless" ] || : > "$STATE/show.wireless"
    {
        echo "wireless.radio0=radio"
        echo "wireless.$sec=wifi-iface"
        echo "wireless.$sec.device='$dev'"
        echo "wireless.$sec.mode='$mode'"
        echo "wireless.$sec.ssid='$ssid'"
        echo "wireless.$sec.network='$net'"
    } >> "$STATE/show.wireless"
    printf '%s' "$dev"  > "$STATE/wireless_${sec}_device"
    printf '%s' "$mode" > "$STATE/wireless_${sec}_mode"
    printf '%s' "$ssid" > "$STATE/wireless_${sec}_ssid"
    printf '%s' "$net"  > "$STATE/wireless_${sec}_network"
}

run_ap_setup() {
    (
        set -u
        export UCI_STATE="$STATE"
        export PATH="$SANDBOX/bin:$PATH"
        export LOGFILE="$SANDBOX/setup.log"
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        LOGFILE="$SANDBOX/setup.log"
        # The globals setup_public_wifi would derive (device identity is not
        # this contract's subject; the functions read these verbatim).
        CODE="9C3F"
        GATEWAY_NAME="TollGate-9C3F"
        setup_band_ap "radio0" "tollgate_2g_open"
    ) > "$SANDBOX/harness.log" 2>&1
}

# --- 1. stock topology: byte-identical binding + generated SSID ---------------
STATE="$SANDBOX/stock"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.lan=interface
network.lan.device='br-lan'
network.@device[0]=device
network.@device[0].name='br-lan'
EOF
: > "$STATE/show.network.lan"   # network.lan EXISTS
seed_iface bench_ap radio0 ap TollGate-9C3F lan
printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
grep -q "^wireless.bench_ap.network=lan$" "$STATE/uci-set.log" \
    && ok "stock: AP bound to 'lan' (byte-identical stock behaviour)" \
    || bad "stock: AP not bound to lan"
grep -q "^wireless.bench_ap.ssid=TollGate-9C3F$" "$STATE/uci-set.log" \
    && ok "stock: machine-shaped SSID re-derived" \
    || bad "stock: SSID not written"

# --- 2. the verdict's split-plane repro: adopt + preserve ---------------------
STATE="$SANDBOX/splitplane"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.portal=interface
network.portal.device='br-portal'
EOF
# NO show.network.lan — network.lan is deleted
seed_iface bench_ap radio0 ap BenchPortal portal
printf 'br-portal' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
grep -q "^wireless.bench_ap.network=portal$" "$STATE/uci-set.log" \
    && ok "split-plane: AP bound to the ADOPTED portal network (not the deleted 'lan')" \
    || { bad "split-plane: AP not bound to the adopted network"; grep network "$STATE/uci-set.log" 2>/dev/null; }
if grep -q "^wireless.bench_ap.network=lan$" "$STATE/uci-set.log" 2>/dev/null; then
    bad "split-plane: AP bound to nonexistent 'lan' (the rc1 damage)"
else
    ok "split-plane: never bound to the nonexistent 'lan'"
fi
grep -q "preserving operator-chosen SSID 'BenchPortal'" "$SANDBOX/setup.log" \
    && ok "split-plane: operator SSID 'BenchPortal' preserved (the rc1 rename undone)" \
    || bad "split-plane: operator SSID not preserved (or not logged)"
grep -q "binding bench_ap to the existing portal network 'portal'" "$SANDBOX/setup.log" \
    && ok "split-plane: the adoption is logged" \
    || bad "split-plane: adoption not logged"
if grep -q "^wireless.bench_ap.ssid=" "$STATE/uci-set.log" 2>/dev/null; then
    bad "split-plane: SSID was rewritten despite the operator guard"
else
    ok "split-plane: no SSID write issued at all"
fi

# --- 3. machine-shaped old-code SSID still re-derives -------------------------
STATE="$SANDBOX/recoded"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.lan=interface
network.lan.device='br-lan'
EOF
: > "$STATE/show.network.lan"
seed_iface bench_ap radio0 ap TollGate-9C3F lan
printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
grep -q "^wireless.bench_ap.ssid=TollGate-9C3F$" "$STATE/uci-set.log" \
    && ok "machine-shaped SSID (ends in the device code) is re-derived" \
    || bad "machine-shaped SSID not re-derived"

# --- 4. no lan, no bridge owner: preserve, never a ghost ----------------------
STATE="$SANDBOX/orphan"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.mgmt=interface
network.mgmt.device='br-mgmt'
EOF
seed_iface bench_ap radio0 ap TollGate-9C3F islandnet
printf '' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
grep -q "^wireless.bench_ap.network=islandnet$" "$STATE/uci-set.log" \
    && ok "orphan case: existing binding preserved (no rebind to a ghost)" \
    || { bad "orphan case: binding not preserved"; grep network "$STATE/uci-set.log" 2>/dev/null || echo "(no network write at all)"; }
grep -q "not rebinding to a nonexistent network" "$SANDBOX/setup.log" \
    && ok "orphan case: the non-rebind is warned" \
    || bad "orphan case: no warning"

# --- 5. STA sections stay untouched (regression pin) ---------------------------
STATE="$SANDBOX/sta"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.lan=interface
network.lan.device='br-lan'
EOF
: > "$STATE/show.network.lan"
seed_iface uplink radio0 sta Upstream ""
printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
if grep -q "^wireless.uplink.network=" "$STATE/uci-set.log" 2>/dev/null; then
    bad "STA section was rewritten"
else
    ok "STA section untouched (preserved upstream)"
fi

# --- 6. credentials guard: an operator AP is never silently opened ----------
STATE="$SANDBOX/creds"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.portal=interface
network.portal.device='br-portal'
EOF
cat > "$STATE/show.wireless" <<'EOF'
wireless.radio0=radio
wireless.bench_ap=wifi-iface
wireless.bench_ap.device='radio0'
wireless.bench_ap.mode='ap'
wireless.bench_ap.ssid='BenchPortal'
wireless.bench_ap.network='portal'
wireless.bench_ap.encryption='psk2'
EOF
printf 'radio0' > "$STATE/wireless_bench_ap_device"
printf 'ap'     > "$STATE/wireless_bench_ap_mode"
printf 'BenchPortal' > "$STATE/wireless_bench_ap_ssid"
printf 'portal' > "$STATE/wireless_bench_ap_network"
printf 'psk2'   > "$STATE/wireless_bench_ap_encryption"
printf 'br-portal' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
if grep -q "^wireless.bench_ap.encryption=" "$STATE/uci-set.log" 2>/dev/null; then
    bad "operator AP's encryption was rewritten (silently opened or changed)"
else
    ok "operator AP keeps its encryption (never silently opened)"
fi
grep -q "preserving operator credentials" "$SANDBOX/setup.log" \
    && ok "the credentials preservation is logged" \
    || bad "credentials preservation not logged"

# machine-shaped section still gets the open-AP contract
STATE="$SANDBOX/credsmachine"; mkdir -p "$STATE"
cat > "$STATE/show.network" <<'EOF'
network.lan=interface
network.lan.device='br-lan'
EOF
: > "$STATE/show.network.lan"
seed_iface bench_ap radio0 ap TollGate-9C3F lan
printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_ap_setup
grep -q "^wireless.bench_ap.encryption=none$" "$STATE/uci-set.log" \
    && ok "machine-shaped AP keeps the open-AP contract (encryption=none)" \
    || bad "machine-shaped AP not forced open"

printf 'check-wifi-rebind-guard: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
