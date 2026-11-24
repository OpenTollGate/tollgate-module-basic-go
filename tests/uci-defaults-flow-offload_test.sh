#!/usr/bin/env bash
# Offline tests for the flow-offloading guard in 99-tollgate-setup
# (setup_flow_offload_guard) and its install-time twin in packaging/postinst.
#
# WHY THIS EXISTS. The whole enforcement story of this package is a single
# nftables base chain, `nds_enforce_forward`, hooked at `forward priority -1`
# (packaging/files/etc/nftables.d/20-nds-enforce.nft). Flow offloading breaks
# exactly that: once a flow is offloaded to the software fast path (or the
# hardware flow table on MediaTek/Qualcomm), its packets stop traversing the
# netfilter hooks, so the chain stops seeing them. A session then survives two
# things that are supposed to end it:
#
#   - `ndsctl deauth` (the module's only deauthorization mechanism) stops
#     taking effect on the ESTABLISHED flow — the client keeps its TCP/QUIC
#     sockets, which is what "Signal still works after the session ended" looks
#     like from the outside;
#   - the byte counters that drive `used >= allotment` stop advancing, so the
#     meter stops metering the very traffic it is selling.
#
# This package never declared either option, so it has always been an
# operator's LuCI toggle away from being unenforced. This guard is a
# DECLARATION, not a discovery: it names the two options and forces them off.
#
# No router, SDK or network needed: the function is extracted from the setup
# script by function boundary (the same style as the IPv6/NTP/band tests) and
# run against a fake uci.
#
# Usage: bash tests/uci-defaults-flow-offload_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
POSTINST="packaging/postinst"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---- extract setup_flow_offload_guard by function boundary ------------------
awk '/^setup_flow_offload_guard\(\) \{/,/^\}/' "$SCRIPT" > "$TMP/off_fragment.sh"
if ! grep -q "flow_offloading" "$TMP/off_fragment.sh"; then
    bad "extraction failed — setup_flow_offload_guard not found in $SCRIPT"
    echo
    echo "passed $PASS, failed $FAIL"
    exit 1
fi
printf '\nlog() { echo "log: $*"; }\nsetup_flow_offload_guard\n' >> "$TMP/off_fragment.sh"

mkdir -p "$TMP/bin"

# ---- fake uci: flat-file state, the subset the function uses ---------------
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
    get)
        key="${1:-}"
        if [ -f "$STATE/$key" ]; then cat "$STATE/$key"; exit 0; fi
        for f in "$STATE/$key".*; do [ -e "$f" ] && exit 0; done
        exit 1
        ;;
    set)
        arg="${1:-}"
        key="${arg%%=*}"
        val="${arg#*=}"
        mkdir -p "$STATE/$(dirname "$key")"
        printf '%s' "$val" > "$STATE/$key"
        ;;
    commit)
        exit 0
        ;;
    *)
        echo "uci-shim: unsupported command: $cmd" >&2
        exit 99
        ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

run_fragment() {  # run_fragment <state-dir> [script]
    UCI_STATE="$1" PATH="$TMP/bin:$PATH" sh "${2:-$TMP/off_fragment.sh}" 2>"$1/stderr.log"
}

state_has() { [ -f "$1/$2" ] && [ "$(cat "$1/$2")" = "$3" ]; }

# ---- case 1: both offload axes are declared off, and the function exits 0 --
S="$TMP/stock"; mkdir -p "$S"
for k in firewall.@defaults[0].flow_offloading firewall.@defaults[0].flow_offloading_hw; do
    mkdir -p "$S/$(dirname "$k")"; echo "1" > "$S/$k"
done
run_fragment "$S"; rc=$?
[ $rc -eq 0 ] && ok "guard exits 0 on a stock router" || bad "guard exited $rc"
state_has "$S" "firewall.@defaults[0].flow_offloading" 0 \
    && ok "software flow offloading is forced off" \
    || bad "software flow offloading not disabled"
state_has "$S" "firewall.@defaults[0].flow_offloading_hw" 0 \
    && ok "hardware flow offloading is forced off" \
    || bad "hardware flow offloading not disabled"

# ---- case 2: idempotence — a second run changes nothing --------------------
before="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
run_fragment "$S"
after="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
[ "$before" = "$after" ] && ok "idempotent: second run is a no-op" || bad "idempotent: second run changed state"

# ---- case 3: an operator-ENABLED fast path is re-disabled (policy) ---------
# The LuCI toggle is the exact threat model: someone turns it on for throughput
# and silently removes revocation. The guard must re-assert, not defer.
S3="$TMP/operator-enabled"; mkdir -p "$S3"
mkdir -p "$S3/firewall.@defaults[0]"
echo "1" > "$S3/firewall.@defaults[0].flow_offloading"
echo "1" > "$S3/firewall.@defaults[0].flow_offloading_hw"
run_fragment "$S3"
state_has "$S3" "firewall.@defaults[0].flow_offloading" 0 \
    && state_has "$S3" "firewall.@defaults[0].flow_offloading_hw" 0 \
    && ok "operator-enabled offloading is re-disabled (policy, not adoption)" \
    || bad "operator-enabled offloading survived"

# ---- case 4: the INSTALL-time twin in postinst converges the same axes -----
# Same reasoning as the IPv6 block: the pre-auth posture must be correct from
# the moment the package lands, not at the next full-setup pass.
awk '/^# Flow offloading is disabled at INSTALL time/,/^echo "Flow offloading disabled at install time/' \
    "$POSTINST" > "$TMP/postinst_fragment.sh"
if ! grep -q "flow_offloading" "$TMP/postinst_fragment.sh"; then
    bad "extraction failed — install-time flow-offloading block not found in $POSTINST"
else
    S4="$TMP/postinst-stock"; mkdir -p "$S4"
    mkdir -p "$S4/firewall.@defaults[0]"
    echo "1" > "$S4/firewall.@defaults[0].flow_offloading"
    echo "1" > "$S4/firewall.@defaults[0].flow_offloading_hw"
    UCI_STATE="$S4" PATH="$TMP/bin:$PATH" sh "$TMP/postinst_fragment.sh" >/dev/null 2>"$S4/stderr.log"; rc=$?
    [ $rc -eq 0 ] && ok "postinst: install-time block exits 0" || bad "postinst: install-time block exited $rc"
    state_has "$S4" "firewall.@defaults[0].flow_offloading" 0 \
        && ok "postinst: software offloading disabled" || bad "postinst: software offloading not disabled"
    state_has "$S4" "firewall.@defaults[0].flow_offloading_hw" 0 \
        && ok "postinst: hardware offloading disabled" || bad "postinst: hardware offloading not disabled"

    # ---- case 5: the two policy copies cannot drift apart ------------------
    setup_keys="$(grep -o "uci set [^=]*=" "$TMP/off_fragment.sh" | awk '{print $3}' | sort -u)"
    postinst_keys="$(grep -o "uci set [^=]*=" "$TMP/postinst_fragment.sh" | awk '{print $3}' | sort -u)"
    [ -n "$setup_keys" ] && [ "$setup_keys" = "$postinst_keys" ] \
        && ok "drift fence: setup and postinst copies write the same axes" \
        || bad "drift fence: policy copies diverge — setup writes [$setup_keys], postinst writes [$postinst_keys]"
fi

# ---- case 6: the guard is actually REACHED from the main setup path --------
# A correct function nobody calls is decoration. It must be a bare call in the
# top-level invocation block, like setup_disable_ipv6 is.
grep -qE '^setup_flow_offload_guard([[:space:]]|$)' "$SCRIPT" \
    && ok "the guard is invoked from the setup driver's call block" \
    || bad "the guard is defined but never called"

# ---- case 7: firewall is committed after the guard -------------------------
# The guard writes `firewall.*` keys; commit_all commits firewall, so a bare
# `uci set` with no commit would be lost on the postinst's network restart.
grep -qE '^[[:space:]]*uci commit firewall' "$SCRIPT" \
    && ok "firewall config is committed by the setup driver" \
    || bad "no uci commit firewall in the setup driver"

# ---- summary
echo
echo "passed $PASS, failed $FAIL"
[ $FAIL -eq 0 ]
