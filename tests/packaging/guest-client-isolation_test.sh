#!/usr/bin/env bash
# Drift guard for guest client isolation in
# packaging/files/etc/uci-defaults/99-tollgate-setup. No router, no SDK, no
# network.
#
# The gap this pins: the open guest SSID is the one layer-2 domain on a
# TollGate that strangers share, and until this test existed nothing armed
# client isolation for it — the portal, the installer and the module all left
# it off, so one guest could ARP-scan its neighbours on the open SSID, answer
# their DHCP, advertise mDNS/SSDP services to them, or relay through one of
# them. The writer now sets `isolate=1` on each guest wifi-iface, which hostapd
# renders as `ap_isolate=1` (the option is read in lib/netifd/hostapd.sh —
# `if [ "$isolate" -gt 0 ]` — and the schema at
# /usr/share/schema/wireless.wifi-iface.json aliases `isolate` to
# `ap_isolate`). Both guest APs are SEPARATE BSSes, so both need their own
# copy: one is not enough.
#
# What this test does NOT claim: ap_isolate is an intra-BSS FORWARDING policy,
# not encryption. A monitor-mode neighbour still reads every frame in the
# clear, an authorised MAC is still harvestable from 802.11 headers, and
# traffic between the 2.4 GHz and 5 GHz guest BSSes is still bridged. The wired
# ports that share br-lan are deliberately NOT isolated, and this test pins
# that absence too — see the "deliberate omission" section below for the
# measured reason. Drift in either direction fails here.
#
# How it checks: the shipped driver is copied into a temp dir (only its
# SETUP_FLAG and LOGFILE absolute paths are rewritten) and RUN against a fake
# `uci`, `apk` and /etc/shadow — the same harness as
# tests/uci-defaults-same-version-allowlist_test.sh — so the assertions are
# about the config the shipped code actually generates, not about a grep of
# the source. A second pass mutates the isolate line out of a copy of the
# script and requires every assertion to FAIL against it, so the guard is
# proven to bite rather than assumed to.
#
# Usage: bash tests/packaging/guest-client-isolation_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 1
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ---------------------------------------------------------------- fake apk
# The shipped script carries the __TOLLGATE_VERSION__ placeholder, so a source
# run resolves the setup version from the package manager.
FAKE_VERSION="0.6.0_alpha4-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option. Enough for this script
# because the isolation write is a plain `uci set` — no list handling needed.
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
case "$cmd" in
    get)
        vals="$(values "$1")"
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
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi

# The same driver also runs the admin-credential gate, which reads the live
# /etc/shadow (and would invoke `passwd root` if the hash were empty). Pin both
# files to fixtures — a router whose credential is already set.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
# …and the plain-HTTP entry point's docroot (setup_uhttpd_trusted_entry writes a
# stub document into it), or this test writes into the LIVE
# /etc/tollgate/router-home of whatever host runs it.
export ROUTER_HOME_DIR="$TMP/router-home"

# ------------------------------------------------------------- the fixtures
# A stock-router wireless/network seed: two radios (2.4 + 5 GHz), the stock
# guest APs, the owner's private APs and the upstream STA. This is what the
# shipped writer starts from on a real device.
seed_stock() {
    : > "$UCI_STATE"
    {
        printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g'
        printf '%s\n' 'wireless.radio0.channel=1' 'wireless.radio0.disabled=0'
        printf '%s\n' 'wireless.radio1=wifi-device' 'wireless.radio1.band=5g'
        printf '%s\n' 'wireless.radio1.channel=36' 'wireless.radio1.disabled=0'
        printf '%s\n' 'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0'
        printf '%s\n' 'wireless.default_radio0.mode=ap' 'wireless.default_radio0.network=lan'
        printf '%s\n' 'wireless.default_radio0.ssid=OpenWrt'
        printf '%s\n' 'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1'
        printf '%s\n' 'wireless.default_radio1.mode=ap' 'wireless.default_radio1.network=lan'
        printf '%s\n' 'wireless.default_radio1.ssid=OpenWrt'
        # The owner's private APs: an operator's own devices must keep seeing
        # each other, so these must never gain isolate.
        printf '%s\n' 'wireless.private_radio0=wifi-iface' 'wireless.private_radio0.device=radio0'
        printf '%s\n' 'wireless.private_radio0.mode=ap' 'wireless.private_radio0.network=private'
        printf '%s\n' 'wireless.private_radio0.encryption=psk2+ccmp'
        printf '%s\n' 'wireless.private_radio1=wifi-iface' 'wireless.private_radio1.device=radio1'
        printf '%s\n' 'wireless.private_radio1.mode=ap' 'wireless.private_radio1.network=private'
        printf '%s\n' 'wireless.private_radio1.encryption=psk2+ccmp'
        # The upstream STA (repeater mode): never isolated.
        printf '%s\n' 'wireless.tollgate_uplink=wifi-iface' 'wireless.tollgate_uplink.device=radio1'
        printf '%s\n' 'wireless.tollgate_uplink.mode=sta' 'wireless.tollgate_uplink.network=wwan'
        printf '%s\n' 'wireless.tollgate_uplink.ssid=UpstreamAP'
        printf '%s\n' 'network.lan=interface' 'network.lan.device=br-lan'
        printf '%s\n' 'network.lan.proto=static' 'network.lan.ipaddr=192.168.1.1/24'
        printf '%s\n' 'network.@device[0]=device' 'network.@device[0].name=br-lan'
        printf '%s\n' 'network.@device[0].type=bridge' 'network.@device[0].ports=eth1'
        printf '%s\n' 'network.wan=interface' 'network.wan.device=eth0' 'network.wan.proto=dhcp'
        printf '%s\n' 'system.@system[0]=system' 'system.@system[0].hostname=OpenWrt'
        printf '%s\n' 'dhcp.@dnsmasq[0]=dnsmasq'
    } >> "$UCI_STATE"
}

count() { grep -F -c "$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has()   { grep -F -q "$1" "$UCI_STATE"; }

run_same_version() { # the reinstall/upgrade path: flag already at SETUP_VERSION
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

# --------------------------------------------------------------- assertions
# The four invariants. Kept in functions so the RED pass at the bottom can run
# the exact same checks against a deliberately broken copy of the script.
GUEST_IFACES="tollgate_2g_open tollgate_5g_open"

check_guest_isolated() { # <label>
    local label="$1" iface n
    for iface in $GUEST_IFACES; do
        if has "wireless.$iface.isolate=1"; then
            n=$(count "wireless.$iface.isolate=1")
            [ "$n" = 1 ] && ok "$label: wireless.$iface.isolate=1 (once)" \
                         || bad "$label: wireless.$iface.isolate=1 written $n times (want 1)"
        else
            bad "$label: wireless.$iface has no isolate=1 — clients on the open SSID can reach each other (ap_isolate off)"
        fi
    done
}

check_never_isolated() { # <label> — the paths that must stay untouched
    local label="$1" key iface
    for iface in tollgate_uplink private_radio0 private_radio1; do
        for key in "wireless.$iface.isolate" "wireless.$iface.isolate=0"; do
            if has "$key"; then
                bad "$label: $key present — the uplink/owner network must not be isolated"
            fi
        done
    done
    # Any interface that is not a guest AP must be untouched: the isolation
    # write is only reachable from configure_radio_ap(), which the private and
    # STA writers never call.
    local stray
    stray=$(grep -E '^wireless\.[a-z0-9_]+\.isolate=' "$UCI_STATE" 2>/dev/null |
            grep -v -E "^wireless\.($(echo $GUEST_IFACES | tr ' ' '|'))\.isolate=" || true)
    if [ -z "$stray" ]; then
        ok "$label: no wifi-iface outside the two guest APs was isolated"
    else
        bad "$label: unexpected isolate write(s): $(printf '%s ' $stray)"
    fi
    # The deliberate omission: no bridge-port / device isolate anywhere in the
    # network config. See the source comment (and the PR body) for the
    # measurement: Linux bridge port isolation is bilateral
    # (br_private.h br_skb_isolated), the guest VAP's bridge port cannot be
    # marked isolated from uci, and on the bench the wired port's flag blocked
    # nothing — so writing it would report protection it does not provide.
    if grep -E '^network\.[^=]*isolate=' "$UCI_STATE" >/dev/null 2>&1; then
        bad "$label: the writer set a bridge-port isolate — measured to block nothing on the bench (see the source comment); do not ship it as the wired control"
    else
        ok "$label: no bridge-port isolate written (deliberate omission, measured)"
    fi
}

check_guest_isolation_shape() { # <label> — both bands, one shared writer
    local label="$1"
    local n
    n=$(grep -c -E '^[[:space:]]*uci set "wireless\.\$iface\.isolate=1"' "$SCRIPT_UNDER_TEST" 2>/dev/null)
    [ "$n" = 1 ] && ok "$label: one writer of the guest isolate option (configure_radio_ap)" \
                 || bad "$label: expected exactly one \`wireless.\$iface.isolate=1\` write, found $n"
    if grep -q "setup_band_ap \"\$R2G\" 'tollgate_2g_open'" "$SCRIPT_UNDER_TEST" &&
       grep -q "setup_band_ap \"\$R5G\" 'tollgate_5g_open'" "$SCRIPT_UNDER_TEST"; then
        ok "$label: both guest bands are configured through setup_band_ap -> configure_radio_ap"
    else
        bad "$label: a guest band does not flow through configure_radio_ap — one BSS would stay unisolated"
    fi
    # The APs are separate BSSes: the option must not be attached to a radio
    # (wifi-device) section, where it would do nothing.
    if grep -q -E 'uci set "wireless\.\$radio[^"]*isolate' "$SCRIPT_UNDER_TEST"; then
        bad "$label: isolate is written on a radio section — ap_isolate is a BSS (wifi-iface) option"
    else
        ok "$label: isolate is written per wifi-iface, not per radio"
    fi
}

# ------------------------------------------------- 1. same-version repo path
# The branch the driver took, as the driver itself logged it:
#   `2026-09-25 01:00:00 - Setup branch VERIFY — marker matches: recorded=… expected=…`
# The anchor is the verdict TOKEN followed by a boundary. A bare
# `Setup branch VERIFY` also matches `Setup branch VERIFY_REPAIR` (the driver
# logs both with the same prefix); the control below pins that the distinction
# is real, so the assertion cannot be silently weakened.
BRANCH_VERIFY_ANCHOR='Setup branch VERIFY([[:space:]]|$)'
branch_is_verify() { grep -Eq "$BRANCH_VERIFY_ANCHOR" "$1" 2>/dev/null; }

echo "== guest APs are isolated on the same-version reinstall path"
seed_stock
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
printf '%s\n' '2026-01-01 00:00:00 - Setup branch VERIFY_REPAIR — not an orderable release marker: recorded=unsubstituted expected=v0.6.0-alpha4 (relation UNORDERABLE)' > "$TMP/anchor-probe.log"
if branch_is_verify "$TMP/anchor-probe.log"; then
    bad "anchor control: a 'Setup branch VERIFY_REPAIR' line satisfies the VERIFY anchor"
else
    ok "anchor control: a 'Setup branch VERIFY_REPAIR' line does not satisfy the VERIFY anchor"
fi
if branch_is_verify "$LOGFILE"; then
    ok "same-version branch was the path taken"
else
    bad "same-version branch not taken"
fi
check_guest_isolated "same-version install"
check_never_isolated "same-version install"
check_guest_isolation_shape "same-version install"

echo "== a second same-version run does not duplicate the option"
run_same_version
for iface in $GUEST_IFACES; do
    n=$(count "wireless.$iface.isolate=1")
    [ "$n" = 1 ] && ok "idempotent: wireless.$iface.isolate=1 still present exactly once" \
                 || bad "idempotent: wireless.$iface.isolate present $n times (want 1)"
done

echo "== an AP section adopted from an older install still gains the option"
seed_stock
# The pre-fix writer created the guest APs without isolate; the upgraded router
# still carries that section, and setup_band_ap adopts it by label.
printf '%s\n' 'wireless.tollgate_2g_open=wifi-iface' 'wireless.tollgate_2g_open.device=radio0' \
              'wireless.tollgate_2g_open.mode=ap' 'wireless.tollgate_2g_open.network=lan' \
              'wireless.tollgate_2g_open.ssid=TollGate-ABCD' >> "$UCI_STATE"
run_same_version
check_guest_isolated "adopted section"
check_never_isolated "adopted section"

# ------------------------------------------------- 2. full-setup writer path
# The full path cannot be driven end to end here: it writes /etc/profile and
# /proc/sys/kernel/hostname, i.e. the host, not the fake uci. Its wireless
# writer is therefore exercised directly through the same LIB_ONLY seam the
# other 99-tollgate-setup tests use — the functions the full setup calls.
echo "== the full-setup wireless writer isolates both guest BSSes"
seed_stock
GATEWAY_NAME="TollGate-TEST"
export GATEWAY_NAME
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
if command -v configure_radio_ap >/dev/null 2>&1 && command -v setup_band_ap >/dev/null 2>&1; then
    ok "script exposes configure_radio_ap() and setup_band_ap()"
    setup_band_ap radio0 'tollgate_2g_open' >/dev/null 2>&1
    setup_band_ap radio1 'tollgate_5g_open' >/dev/null 2>&1
    check_guest_isolated "full-setup writer"
    check_never_isolated "full-setup writer"
else
    bad "script does not expose the guest-AP writers — the full setup path has no isolation to apply"
fi

echo "== the STA (uplink) section is preserved, never isolated"
seed_stock
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
: > "$LOGFILE"
configure_radio_ap 'tollgate_uplink' 'tollgate_uplink' 'radio1' >/dev/null 2>&1
if has "wireless.tollgate_uplink.isolate=1"; then
    bad "configure_radio_ap isolated the uplink — that would cut the router off from its upstream AP"
else
    ok "the STA section is left unisolated"
fi
grep -q "is STA, preserving upstream connection" "$LOGFILE" 2>/dev/null \
    && ok "the STA section was recognised as the upstream and skipped" \
    || bad "the STA branch did not run for the uplink section"

echo "== a single-band device isolates the one guest AP it has"
: > "$UCI_STATE"
printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' >> "$UCI_STATE"
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
setup_band_ap radio0 'tollgate_2g_open' >/dev/null 2>&1
has "wireless.tollgate_2g_open.isolate=1" \
    && ok "single-band: the 2.4 GHz guest AP is isolated" \
    || bad "single-band: the 2.4 GHz guest AP was not isolated"
grep -q '^wireless\.tollgate_5g_open' "$UCI_STATE" 2>/dev/null \
    && bad "single-band: a 5 GHz section was created for a device without that band" \
    || ok "single-band: no 5 GHz section was invented"

# ------------------------------------------------------------- 3. RED control
# A guard that has never failed is decoration. Strip the isolate write from a
# copy of the shipped script and require the same assertions to fail against
# it: that proves the checks read the generated config rather than passing by
# construction.
echo "== RED control: remove the option from a mutated copy and require failure"
MUTANT="$TMP/99-tollgate-setup.mutant"
grep -v 'uci set "wireless\.\$iface\.isolate' "$ROOT/$SCRIPT" > "$MUTANT"
if grep -q 'uci set "wireless\.\$iface\.isolate' "$MUTANT"; then
    bad "RED control: could not strip the isolate write from the mutant copy — the control is vacuous"
else
    ok "RED control: mutant copy has no guest isolate write"
fi
SCRIPT_UNDER_TEST="$MUTANT"
seed_stock
run_same_version
# The mutant MUST fail these assertions; run them in a subshell so the
# expected failures do not reach this run's tally, and report only whether
# they fired.
before_fail=$FAIL
red_fails=$( ( check_guest_isolated "RED control" >/dev/null 2>&1; echo "$((FAIL - before_fail))" ) )
if [ "${red_fails:-0}" -gt 0 ]; then
    ok "RED control: the guard fails without the option (${red_fails} assertions red as required)"
else
    bad "RED control: the guard passed against a script that never sets isolate — it is not testing the generated config"
fi
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"

# -------------------------------------------------------- 4. it actually ships
echo "== the writer under test is the one that ships"
grep -q 'files/etc/uci-defaults/99-tollgate-setup' packaging/Makefile \
    && ok "packaging/Makefile installs etc/uci-defaults/99-tollgate-setup" \
    || bad "packaging/Makefile no longer installs the setup script — the isolation would never reach a router"
grep -q 'packaging/files/etc/uci-defaults/99-tollgate-setup' packaging/local-build-ipk.sh \
    && ok "packaging/local-build-ipk.sh stages 99-tollgate-setup into the payload" \
    || bad "packaging/local-build-ipk.sh no longer stages the setup script"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
