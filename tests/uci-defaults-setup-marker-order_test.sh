#!/usr/bin/env bash
# Offline tests for the SETUP-MARKER ORDERING of
# packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The bug this guards (observed on the bench MT3000, 2026-09-24, three times):
# `/etc/tollgate-setup-done` stores the setup version and the driver compared it
# with the shipped version for EQUALITY. A different version therefore counted
# as a NEW version, and a DOWNGRADE is a different version — so rolling alpha5
# back to alpha4 re-ran FULL setup, re-randomised the guest SSID and recreated
# the nodogsplash / :8090-guard state, silently clobbering operator-chosen state
# mid-test. The fix is an ORDER-aware comparison:
#
#   marker absent                  -> full setup        (first boot)
#   marker equal to shipped        -> verify/repair
#   marker OLDER than shipped      -> full setup        (a real upgrade)
#   marker NEWER than shipped      -> verify/repair ONLY (a roll-back)
#   marker not orderable           -> verify/repair + re-stamp the marker
#
# Three layers of test, so a revert of any one part is caught:
#
#   1. A case table over the comparison itself (`version_relation`,
#      `setup_marker_decision`), sourced from the real script in lib-only mode.
#   2. End-to-end runs of the real driver against a fake uci/apk/shadow, one per
#      case, asserting WHICH BRANCH ran (the log line the driver writes), that
#      the guest SSID and the operator's own choices survived every roll-back,
#      that the policy list was still re-asserted, and what the marker file
#      holds afterwards.
#   3. The FAILING NEGATIVE CONTROL: the same roll-back fixture run against a
#      copy of the same script whose decision call is replaced by the pre-change
#      equality predicate. That run must take the FULL-setup branch and
#      re-randomise the guest SSID — i.e. the test is proven to fail when the
#      comparison is equality-based.
#
# The end-to-end harness copies the shipped script into a temp dir and rewrites
# the absolute paths into the live system (flag file, log, /etc/profile,
# /proc/sys/kernel/hostname, /etc/config/nodogsplash) plus the two credential
# paths the script already exposes as SHADOW_FILE/PASSWD_FILE. Nothing else is
# touched: the driver under test — version resolution, the ordering decision,
# the verify/repair branch and the full-setup branch — is the shipped code, and
# the host's own hostname, /etc/profile and /etc/shadow are never written.
#
# Usage: bash tests/uci-defaults-setup-marker-order_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ------------------------------------------------------- 1. the case table
# Each row: <label>|<recorded marker>|<marker file present 0/1>|<shipped>|
#           <expected verdict>|<expected relation>
#
# The table is the spec. `absent`/`malformed`/`suffixed` rows are the ones the
# old equality test got wrong or never expressed at all.
CASE_TABLE='
absent marker (first boot)|x|0|v0.6.0-alpha4|FULL|UNORDERABLE
same version|v0.6.0-alpha4|1|v0.6.0-alpha4|VERIFY|SAME
same version, apk spelling|0.6.0_alpha4-r0|1|v0.6.0-alpha4|VERIFY|SAME
same version, git-describe suffix|v0.6.0-alpha4-g2796d96|1|v0.6.0-alpha4|VERIFY|SAME
same version, suffix on the shipped side|v0.6.0-alpha4|1|v0.6.0-alpha4-g2796d96|VERIFY|SAME
older marker (upgrade)|v0.6.0-alpha4|1|v0.6.0-alpha5|FULL|OLDER
older core (upgrade)|v0.5.9|1|v0.6.0-alpha4|FULL|OLDER
newer marker (roll-back)|v0.6.0-alpha5|1|v0.6.0-alpha4|VERIFY|NEWER
newer core (roll-back)|v0.6.1|1|v0.6.0-alpha4|VERIFY|NEWER
newer pre-release rung (roll-back)|v0.6.0-beta1|1|v0.6.0-alpha9|VERIFY|NEWER
release outranks its own pre-release|v0.6.0|1|v0.6.0-alpha4|VERIFY|NEWER
malformed marker: unsubstituted|unsubstituted|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
malformed marker: quoted|"v0.6.0-alpha4"|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
malformed marker: dev branch build|main.574.2796d96|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
malformed marker: four-part version|v0.6.0.1|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
malformed marker: trailing junk|v0.6.0-alpha4-extra|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
empty marker file|__EMPTY__|1|v0.6.0-alpha4|VERIFY_REPAIR|UNORDERABLE
'

echo "== comparison case table (functions sourced from the shipped script)"
TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT" 2>/dev/null
if ! command -v setup_marker_decision >/dev/null 2>&1; then
    echo "FATAL: could not source the setup script's functions" >&2
    exit 1
fi
ok "setup script sources in lib-only mode"

while IFS='|' read -r label recorded present shipped want_verdict want_relation; do
    [ -n "$label" ] || continue
    case "$recorded" in
        __EMPTY__) recorded="" ;;
    esac
    # A quoted marker arrives with its quotes intact: that is the point of the
    # "quoted" row — the file can hold quotes and they are not a version.
    got_verdict=$(setup_marker_decision "$recorded" "$shipped" "$present")
    got_verdict=${got_verdict%% *}
    if [ "$got_verdict" = "$want_verdict" ]; then
        ok "table: $label -> $want_verdict"
    else
        bad "table: $label -> got $got_verdict, want $want_verdict"
    fi
    if [ "$want_relation" = "UNORDERABLE" ]; then
        got_relation=$(version_relation "$recorded" "$shipped")
        [ "$got_relation" = "UNORDERABLE" ] \
            && ok "table: $label -> relation UNORDERABLE" \
            || bad "table: $label -> relation $got_relation, want UNORDERABLE"
    else
        got_relation=$(version_relation "$recorded" "$shipped")
        [ "$got_relation" = "$want_relation" ] \
            && ok "table: $label -> relation $want_relation" \
            || bad "table: $label -> relation $got_relation, want $want_relation"
    fi
done <<EOF
$CASE_TABLE
EOF

# The table above is only meaningful if the OLD rule disagrees with it for the
# roll-back rows. Equality says "different version -> full setup", so every
# NEWER row is a disagreement: this asserts the table actually discriminates.
echo "== the table discriminates against the pre-change equality rule"
rollback_rows=0
while IFS='|' read -r label recorded present shipped want_verdict want_relation; do
    [ -n "$label" ] || continue
    [ "$want_relation" = "NEWER" ] || continue
    rollback_rows=$((rollback_rows + 1))
    case "$recorded" in
        __EMPTY__) recorded="" ;;
    esac
    if [ "$recorded" = "$shipped" ]; then
        bad "discriminating row '$label' is not actually a version change"
    fi
done <<EOF
$CASE_TABLE
EOF
[ "$rollback_rows" -ge 3 ] \
    && ok "table carries $rollback_rows roll-back rows, all version changes under equality" \
    || bad "table carries only $rollback_rows roll-back rows"

# SAME-vs-UNORDERABLE is the other half: the suffixed rows must compare EQUAL,
# which equality-based comparison cannot express (a suffix is a different
# string). If normalisation regressed, these rows would report UNORDERABLE.
echo "== suffixed markers normalise to their release"
for pair in "v0.6.0-alpha4-g2796d96:v0.6.0-alpha4" "0.6.0_alpha4-r0:v0.6.0-alpha4" \
            "v0.6.0-alpha4:v0.6.0-alpha4-g2796d96"; do
    a="${pair%%:*}"; b="${pair#*:}"
    [ "$(version_relation "$a" "$b")" = "SAME" ] \
        && ok "normalisation: $a == $b" \
        || bad "normalisation: $a vs $b -> $(version_relation "$a" "$b"), want SAME"
done

# Whitespace cannot be written inside the pipe-delimited table, so it gets its
# own rows: a marker file that was written with a trailing newline/CR (or read
# back with one) is still the same version.
echo "== stray whitespace and CR in a marker"
[ "$(version_relation "v0.6.0-alpha4 " "v0.6.0-alpha4")" = "SAME" ] \
    && ok "normalisation: trailing space is SAME" \
    || bad "normalisation: trailing space -> $(version_relation "v0.6.0-alpha4 " "v0.6.0-alpha4")"
[ "$(version_relation "$(printf 'v0.6.0-alpha4\r')" "v0.6.0-alpha4")" = "SAME" ] \
    && ok "normalisation: trailing CR is SAME" \
    || bad "normalisation: trailing CR -> $(version_relation "$(printf 'v0.6.0-alpha4\r')" "v0.6.0-alpha4")"
[ "$(setup_marker_decision "$(printf '  v0.6.0-alpha4\n')" v0.6.0-alpha4 1)" != "FULL" ] \
    && ok "normalisation: padded marker does not force full setup" \
    || bad "normalisation: padded marker forced full setup"

# ------------------------------------------------- 2. the end-to-end harness
# A fake system: flat-file uci, a fake apk, a pinned shadow fixture. The state
# seeded is a CONFIGURED router — a guest SSID, the operator's own private WiFi
# credentials, an allow list missing the :443 rule, and a drifted
# uhttpd.main.redirect_https — so every run can be checked for (a) whether it
# re-randomised the guest SSID and (b) whether it still re-asserted the policy.
export UCI_STATE="$TMP/uci.state"
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"

cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
# fake uci — one "key=value" line per option, list entries one per line.
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"; shift || true
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
        [ "$key" = "$1" ] && exit 1
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    del_list)
        # whole-line match: list entries carry spaces
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show)
        [ "${1:-}" = "wireless" ] || exit 0
        while IFS= read -r line; do
            case "${line%%=*}" in
                wireless.*.*) printf "%s='%s'\n" "${line%%=*}" "${line#*=}" ;;
                wireless.*)   printf '%s\n' "$line" ;;
            esac
        done < "$state"
        ;;
    export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
# fake apk — the setup script only consults `apk list --installed`.
[ "${1:-}" = "list" ] && printf '%s\n' "tollgate-wrt-0.6.0_alpha4-r0 aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# The guest SSID is generated with hexdump, which is not present in every test
# environment (Ubuntu's default runner image has no bsdmainutils/bsdextrautils).
# The suite only ever asks "did the SSID change?", so a stand-in that returns a
# fresh 4-hex string per call is enough — and it keeps the suite hermetic.
# MARKER_TEST_FAKE_HEXDUMP=1 forces the stand-in, so the fallback path itself is
# exercised on machines that do have hexdump.
if [ "${MARKER_TEST_FAKE_HEXDUMP:-0}" = "1" ] || ! command -v hexdump >/dev/null 2>&1; then
    # The counter the shim below reads and bumps. It is NOT `$RANDOM`: this
    # shim's shebang is /bin/sh, and POSIX sh has no RANDOM (dash, the /bin/sh
    # on Debian/Ubuntu, leaves it unset), so a RANDOM-based stand-in returned
    # `0000` on every single call there — the fallback was dead on exactly the
    # machines it exists for, and the "full setup did not regenerate the SSID"
    # assertion could never pass. A counter is dash/busybox-ash/bash-safe and
    # still differs on every call, which is all the suite asks of it.
    export HEXDUMP_SEQ_FILE="$TMP/hexdump.seq"
    printf '0\n' > "$HEXDUMP_SEQ_FILE"
    cat > "$TMP/bin/hexdump" <<'SHIM'
#!/bin/sh
# fake hexdump: -n 3 -e '4/1 "%02X"' -> a fresh 4-hex string.
seq="${HEXDUMP_SEQ_FILE:?fake hexdump: HEXDUMP_SEQ_FILE is not set}"
n=$(cat "$seq" 2>/dev/null || echo 0)
n=$((n + 1))
printf '%s\n' "$n" > "$seq"
printf '%04X\n' "$n"
SHIM
    chmod +x "$TMP/bin/hexdump"

    # The stand-in is only useful if it VARIES, under the /bin/sh that will
    # invoke it. Assert that here, at the shim, instead of letting a constant
    # shim surface 400 assertions later as "full setup did not regenerate the
    # SSID" — a message that points at the driver rather than at the harness.
    # (The original RANDOM-based version of this shim is exactly that bug: POSIX
    # sh has no RANDOM, so under dash — the /bin/sh here — it returned 0000
    # every time.)
    h1="$("$TMP/bin/hexdump" -n 3 -e '4/1 "%02X"' /dev/urandom)"
    h2="$("$TMP/bin/hexdump" -n 3 -e '4/1 "%02X"' /dev/urandom)"
    if [ -n "$h1" ] && [ "$h1" != "$h2" ]; then
        ok "fake hexdump varies between calls under sh ($h1 != $h2)"
    else
        bad "fake hexdump is constant under sh (got '$h1' then '$h2') — the fallback cannot drive a re-randomisation assertion"
    fi
fi
export PATH="$TMP/bin:$PATH"

FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
HOSTNAME_FILE="$TMP/kernel-hostname"
PROFILE_FILE="$TMP/profile"
NDS_CONFIG="$TMP/nodogsplash.config"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
export LOGFILE

GUEST_SSID="TollGate-SEED"
GUEST_SSID_5G="TollGate-SEED"
OPERATOR_KEY="Operator-Chosen-Key-01"
OPERATOR_HOSTNAME="OperatorBox"

seed_state() {
    : > "$UCI_STATE"
    {
        printf '%s\n' 'network.lan=interface' 'network.lan.ipaddr=192.168.1.1' 'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'system.@system[0]=system' "system.@system[0].hostname=$OPERATOR_HOSTNAME"
        printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' \
                      'wireless.radio1=wifi-device' 'wireless.radio1.band=5g'
        printf '%s\n' 'wireless.tollgate_2g_open=wifi-iface' 'wireless.tollgate_2g_open.device=radio0' \
                      'wireless.tollgate_2g_open.mode=ap' "wireless.tollgate_2g_open.ssid=$GUEST_SSID" \
                      'wireless.tollgate_2g_open.encryption=none'
        printf '%s\n' 'wireless.tollgate_5g_open=wifi-iface' 'wireless.tollgate_5g_open.device=radio1' \
                      'wireless.tollgate_5g_open.mode=ap' "wireless.tollgate_5g_open.ssid=$GUEST_SSID_5G" \
                      'wireless.tollgate_5g_open.encryption=none'
        printf '%s\n' 'wireless.private_radio0=wifi-iface' 'wireless.private_radio0.device=radio0' \
                      'wireless.private_radio0.mode=ap' 'wireless.private_radio0.ssid=mgmt-op' \
                      "wireless.private_radio0.key=$OPERATOR_KEY"
        # A drifted uhttpd contract: redirect_https=1 pointing :8080 at a :443
        # listener this router does not have. The verify/repair branch owns this
        # and must put it back to 0 (the 2026-09-21 pre13 LuCI lockout).
        printf '%s\n' 'uhttpd.main=uhttpd' 'uhttpd.main.redirect_https=1' \
                      'uhttpd.main.listen_http=0.0.0.0:8080'
        # An allow list written by an older install: no :443 pre-auth rule.
        printf '%s\n' 'nodogsplash.@nodogsplash[0]=nodogsplash' \
                      'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2121' \
                      'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 8080' \
                      'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2050' \
                      'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2051'
    } >> "$UCI_STATE"
    : > "$PROFILE_FILE"
    : > "$HOSTNAME_FILE"
}

# build_script <shipped version> [legacy-equality]
#   Copies the shipped script, redirects the absolute paths into the live
#   system, and pins SETUP_VERSION to the version under test (a real packaged
#   install is substituted, so the placeholder resolution never runs).
#   The optional second argument prepends the pre-change decision rule and
#   points the decision call at it: the negative control.
build_script() {
    local shipped="$1" variant="${2:-shipped}"
    sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
        -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
        -e "s|> */proc/sys/kernel/hostname|> $HOSTNAME_FILE|" \
        -e "s|/etc/profile|$PROFILE_FILE|g" \
        -e "s|/etc/config/nodogsplash|$NDS_CONFIG|g" \
        "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
    sed -i "s|^SETUP_VERSION=\"__TOLLGATE_VERSION__\"\$|SETUP_VERSION=\"$shipped\"|" "$SCRIPT_UNDER_TEST"
    if [ "$variant" = "legacy-equality" ]; then
        cat > "$SCRIPT_UNDER_TEST.shim" <<'SHIM'
# NEGATIVE CONTROL. The pre-change decision rule, verbatim: the marker was
# compared for EQUALITY, so any different version — including an OLDER one, a
# roll-back — counted as a new version and re-ran full setup. Invoked with
# `sh`, so prepending ahead of the (now non-first) shebang is harmless.
legacy_marker_decision() {
    if [ "${1:-}" = "${2:-}" ]; then
        printf 'VERIFY legacy equality: recorded=%s expected=%s\n' "$1" "$2"
        return 1
    fi
    printf 'FULL legacy equality: recorded=%s expected=%s\n' "${1:-<absent>}" "$2"
    return 0
}
SHIM
        cat "$SCRIPT_UNDER_TEST" >> "$SCRIPT_UNDER_TEST.shim"
        mv "$SCRIPT_UNDER_TEST.shim" "$SCRIPT_UNDER_TEST"
        sed -i 's|^DECISION=$(setup_marker_decision .*)$|DECISION=$(legacy_marker_decision "$EXISTING_VERSION" "$SETUP_VERSION" "$MARKER_PRESENT")|' \
            "$SCRIPT_UNDER_TEST"
    fi
    grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
    grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST" &&
    grep -q "> $HOSTNAME_FILE\$" "$SCRIPT_UNDER_TEST" &&
    grep -q -F -- "$PROFILE_FILE" "$SCRIPT_UNDER_TEST" &&
    grep -q -F -- "$NDS_CONFIG" "$SCRIPT_UNDER_TEST"
}

# run_setup_chain <shipped version> [variant]
#   Runs the script WITHOUT re-seeding: the uci state and the marker file left
#   by the previous run carry forward, which is how a router actually
#   experiences a sequence of installs (the marker a run writes is what the next
#   run reads back).
run_setup_chain() {
    build_script "$1" "${2:-shipped}" || {
        bad "harness could not build the script under test"
        return 99
    }
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >"$TMP/run.out" 2>"$TMP/run.err"
    return $?
}

# run_setup <marker|__ABSENT__> <shipped version> <variant>
#   A fresh fixture: the seeded operator state, then the given marker.
run_setup() {
    local marker="$1" shipped="$2" variant="${3:-shipped}"
    seed_state
    case "$marker" in
        __ABSENT__) rm -f "$FLAG" ;;
        __EMPTY__)  : > "$FLAG" ;;
        *)          printf '%s\n' "$marker" > "$FLAG" ;;
    esac
    run_setup_chain "$shipped" "$variant"
}

marker_now()   { [ -f "$FLAG" ] && cat "$FLAG"; }
ssid_now()     { grep -F 'wireless.tollgate_2g_open.ssid=' "$UCI_STATE" | head -n1 | cut -d= -f2-; }
key_now()      { grep -F 'wireless.private_radio0.key=' "$UCI_STATE" | head -n1 | cut -d= -f2-; }
hostname_now() { grep -F 'system.@system[0].hostname=' "$UCI_STATE" | head -n1 | cut -d= -f2-; }
branch_now()   { sed -n 's/^.*Setup branch \([A-Z_]*\) .*$/\1/p' "$LOGFILE" | head -n1; }
full_ran()     { grep -q '^.*Running full setup' "$LOGFILE"; }
ipv6_off()     { grep -Fq 'dhcp.lan.ra=disabled' "$UCI_STATE"; }
redirect_now() { grep -F 'uhttpd.main.redirect_https=' "$UCI_STATE" | head -n1 | cut -d= -f2-; }
allow443()     { grep -F -c 'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 443' "$UCI_STATE"; }

# --------------------------------------------------- 2a. absent marker (first boot)
echo "== absent marker: full setup, and the marker is written"
run_setup __ABSENT__ v0.6.0-alpha4
rc=$?
[ "$rc" = 0 ] && ok "absent: run exits 0" \
              || bad "absent: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(branch_now)" = "FULL" ] && ok "absent: FULL branch taken" \
                             || bad "absent: branch $(branch_now) (log: $(head -n 2 "$LOGFILE" | tr '\n' ' '))"
full_ran && ok "absent: full setup ran" || bad "absent: full setup did not run"
[ "$(ssid_now)" != "$GUEST_SSID" ] && ok "absent: guest SSID was generated" \
                                   || bad "absent: guest SSID not regenerated on first boot"
[ "$(marker_now)" = "v0.6.0-alpha4" ] && ok "absent: marker written as v0.6.0-alpha4" \
                                      || bad "absent: marker is '$(marker_now)'"
[ "$(key_now)" = "$OPERATOR_KEY" ] && ok "absent: existing private key preserved" \
                                   || bad "absent: private key lost"
[ "$(hostname_now)" = "$OPERATOR_HOSTNAME" ] && ok "absent: operator hostname preserved" \
                                            || bad "absent: hostname clobbered"

# ------------------------------------------- 2b. same / older / newer markers
echo "== same version: verify/repair only, nothing re-randomised"
run_setup v0.6.0-alpha4 v0.6.0-alpha4
rc=$?
SSID_BEFORE="$GUEST_SSID"
[ "$rc" = 0 ] && ok "same: run exits 0" \
              || bad "same: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(branch_now)" = "VERIFY" ] && ok "same: verify branch taken" \
                               || bad "same: branch $(branch_now)"
full_ran && bad "same: full setup ran on an equal marker" || ok "same: full setup did not run"
[ "$(ssid_now)" = "$SSID_BEFORE" ] && ok "same: guest SSID untouched" \
                                   || bad "same: guest SSID changed to $(ssid_now)"
[ "$(allow443)" = 1 ] && ok "same: the :443 pre-auth rule was re-asserted exactly once" \
                      || bad "same: :443 rule present $(allow443) times"
[ "$(redirect_now)" = 0 ] && ok "same: uhttpd.main.redirect_https repaired to 0" \
                          || bad "same: redirect_https is $(redirect_now)"
[ "$(marker_now)" = "v0.6.0-alpha4" ] && ok "same: marker left alone" \
                                      || bad "same: marker is '$(marker_now)'"

echo "== older marker (real upgrade): full setup"
run_setup v0.6.0-alpha4 v0.6.0-alpha5
[ "$(branch_now)" = "FULL" ] && ok "older: FULL branch taken" \
                             || bad "older: branch $(branch_now)"
full_ran && ok "older: full setup ran" || bad "older: full setup did not run"
ipv6_off && ok "older: full-setup-only step ran (IPv6 disabled on LAN)" \
         || bad "older: full-setup step did not run"
[ "$(marker_now)" = "v0.6.0-alpha5" ] && ok "older: marker advanced to v0.6.0-alpha5" \
                                      || bad "older: marker is '$(marker_now)'"

echo "== newer marker (ROLL-BACK): verify/repair only, operator state preserved"
# The bench case: alpha5 is installed, the operator has chosen state, alpha4 is
# (re)installed over it. Before the fix this re-ran full setup and re-randomised
# the guest SSID.
run_setup v0.6.0-alpha5 v0.6.0-alpha4
rc=$?
[ "$rc" = 0 ] && ok "newer: run exits 0" \
              || bad "newer: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(branch_now)" = "VERIFY" ] && ok "newer: verify branch taken (roll-back)" \
                               || bad "newer: branch $(branch_now) (log: $(head -n 2 "$LOGFILE" | tr '\n' ' '))"
full_ran && bad "newer: full setup ran on a roll-back — the defect" \
          || ok "newer: full setup did NOT run"
[ "$(ssid_now)" = "$SSID_BEFORE" ] && ok "newer: seeded operator SSID survived the roll-back (still $GUEST_SSID)" \
                                   || bad "newer: guest SSID re-randomised to $(ssid_now)"
[ "$(key_now)" = "$OPERATOR_KEY" ] && ok "newer: operator private key survived" \
                                   || bad "newer: private key lost"
[ "$(hostname_now)" = "$OPERATOR_HOSTNAME" ] && ok "newer: operator hostname survived" \
                                            || bad "newer: hostname clobbered"
[ "$(allow443)" = 1 ] && ok "newer: policy list still re-asserted (:443 present once)" \
                      || bad "newer: :443 rule present $(allow443) times"
[ "$(redirect_now)" = 0 ] && ok "newer: uhttpd contract still repaired" \
                          || bad "newer: redirect_https is $(redirect_now)"
[ "$(marker_now)" = "v0.6.0-alpha5" ] \
    && ok "newer: marker NOT rewritten (so the return leg cannot look like an upgrade)" \
    || bad "newer: marker was rewritten to '$(marker_now)'"

# ------------------------------------------------- 2c. malformed and suffixed
echo "== malformed marker: verify/repair, never full setup, marker re-stamped"
run_setup unsubstituted v0.6.0-alpha4
rc=$?
[ "$rc" = 0 ] && ok "malformed: run exits 0" \
              || bad "malformed: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(branch_now)" = "VERIFY_REPAIR" ] && ok "malformed: VERIFY_REPAIR branch taken" \
                                      || bad "malformed: branch $(branch_now)"
full_ran && bad "malformed: full setup ran — it must fail toward verify" \
          || ok "malformed: full setup did NOT run"
[ "$(ssid_now)" = "$SSID_BEFORE" ] && ok "malformed: guest SSID survived" \
                                   || bad "malformed: guest SSID re-randomised to $(ssid_now)"
[ "$(marker_now)" = "v0.6.0-alpha4" ] && ok "malformed: marker re-stamped with the shipped version" \
                                      || bad "malformed: marker is '$(marker_now)'"
# The re-stamp must converge: the next run is a plain SAME, not another repair.
run_setup v0.6.0-alpha4 v0.6.0-alpha4
[ "$(branch_now)" = "VERIFY" ] && ok "malformed: the re-stamped marker converges to SAME" \
                               || bad "malformed: next run took $(branch_now)"

echo "== empty marker file: verify/repair, never full setup"
run_setup __EMPTY__ v0.6.0-alpha4
[ "$(branch_now)" = "VERIFY_REPAIR" ] && ok "empty: VERIFY_REPAIR branch taken" \
                                      || bad "empty: branch $(branch_now)"
full_ran && bad "empty: full setup ran" || ok "empty: full setup did NOT run"
[ "$(ssid_now)" = "$SSID_BEFORE" ] && ok "empty: guest SSID survived" \
                                   || bad "empty: guest SSID re-randomised"

echo "== git-describe-suffixed marker for the same release: verify/repair"
run_setup "v0.6.0-alpha4-g2796d96" v0.6.0-alpha4
[ "$(branch_now)" = "VERIFY" ] && ok "suffixed: verify branch taken (SAME release)" \
                               || bad "suffixed: branch $(branch_now)"
full_ran && bad "suffixed: full setup ran for a build suffix" \
          || ok "suffixed: full setup did NOT run"
[ "$(ssid_now)" = "$SSID_BEFORE" ] && ok "suffixed: guest SSID survived" \
                                   || bad "suffixed: guest SSID re-randomised"
[ "$(marker_now)" = "v0.6.0-alpha4-g2796d96" ] && ok "suffixed: marker left alone" \
                                             || bad "suffixed: marker is '$(marker_now)'"

# ---------------------------------------------- 2d. the alpha4->alpha5->alpha4 round trip
# State carries across these runs, exactly like the bench: the marker a run
# writes is the marker the next run reads.
echo "== round trip: alpha4 -> alpha5 -> alpha4 -> alpha5 (the bench sequence)"
run_setup __ABSENT__ v0.6.0-alpha4          # flash + install alpha4
SSID_A="$(ssid_now)"
[ -n "$SSID_A" ] && ok "round trip: alpha4 first boot generated an SSID ($SSID_A)" \
                 || bad "round trip: no SSID after alpha4 first boot"
[ "$(marker_now)" = "v0.6.0-alpha4" ] && ok "round trip: alpha4 marker written" \
                                      || bad "round trip: marker is '$(marker_now)'"

run_setup_chain v0.6.0-alpha5               # upgrade to alpha5
[ "$(branch_now)" = "FULL" ] && ok "round trip: alpha5 is an upgrade -> FULL" \
                             || bad "round trip: alpha5 took $(branch_now)"
SSID_B="$(ssid_now)"
[ "$SSID_A" != "$SSID_B" ] && ok "round trip: the upgrade re-ran full setup (SSID $SSID_A -> $SSID_B)" \
                           || bad "round trip: full setup did not regenerate the SSID"
[ "$(marker_now)" = "v0.6.0-alpha5" ] && ok "round trip: marker advanced to alpha5" \
                                      || bad "round trip: marker is '$(marker_now)'"

run_setup_chain v0.6.0-alpha4               # DOWNGRADE back to alpha4
[ "$(branch_now)" = "VERIFY" ] && ok "round trip: downgrade -> VERIFY" \
                               || bad "round trip: downgrade took $(branch_now)"
[ "$(ssid_now)" = "$SSID_B" ] && ok "round trip: SSID survived the downgrade ($SSID_B)" \
                             || bad "round trip: SSID became $(ssid_now) (was $SSID_B)"
[ "$(marker_now)" = "v0.6.0-alpha5" ] && ok "round trip: marker still alpha5 after the downgrade" \
                                      || bad "round trip: marker is '$(marker_now)'"
[ "$(allow443)" = 1 ] && ok "round trip: policy list re-asserted on the downgrade leg" \
                      || bad "round trip: :443 rule present $(allow443) times"

run_setup_chain v0.6.0-alpha5               # reinstall alpha5 (return leg)
[ "$(branch_now)" = "VERIFY" ] && ok "round trip: return leg -> VERIFY" \
                               || bad "round trip: return leg took $(branch_now)"
[ "$(ssid_now)" = "$SSID_B" ] && ok "round trip: SSID survived the return leg" \
                             || bad "round trip: SSID became $(ssid_now) on the return leg"
[ "$(hostname_now)" = "$OPERATOR_HOSTNAME" ] && ok "round trip: operator hostname intact" \
                                            || bad "round trip: hostname clobbered"
[ "$(key_now)" = "$OPERATOR_KEY" ] && ok "round trip: operator private key intact" \
                                   || bad "round trip: private key lost"

# ---------------------------------------------------- 3. negative control
echo "== NEGATIVE CONTROL: the pre-change equality rule re-runs full setup on a downgrade"
# One fixture, defined once: a configured router (seeded operator SSID) whose
# marker says alpha5, being reinstalled with alpha4 — the bench roll-back.
# The control run differs from the shipped run by exactly one line: the decision
# call, replaced by the pre-change equality predicate.
CONTROL_MARKER="v0.6.0-alpha5"
CONTROL_SHIPPED="v0.6.0-alpha4"

run_setup "$CONTROL_MARKER" "$CONTROL_SHIPPED" legacy-equality
rc=$?
# The control is expected to take the FULL branch — that IS the pre-change
# behaviour, and (on a fake uci) it exits non-zero whenever a full-setup step
# needs something a fake system cannot provide. Only the branch it takes and the
# state it leaves behind are asserted; the exit code is reported for context.
if full_ran; then
    ok "control: equality-based decision took the FULL-setup branch (exit $rc)"
else
    bad "control: equality-based decision did NOT re-run full setup — the negative control is not reproducing the defect"
fi
if [ "$(ssid_now)" != "$GUEST_SSID" ]; then
    ok "control: equality-based decision re-randomised the guest SSID ($GUEST_SSID -> $(ssid_now))"
else
    bad "control: equality-based decision left the guest SSID alone — the negative control no longer reproduces the clobber"
fi
ipv6_off && ok "control: full-setup-only state was rewritten (IPv6 disabled on LAN)" \
         || bad "control: full-setup-only state was not rewritten"
if [ "$(marker_now)" = "$CONTROL_SHIPPED" ]; then
    ok "control: equality-based decision rewrote the marker to the older version ($CONTROL_SHIPPED)"
else
    bad "control: marker is '$(marker_now)' after the control run"
fi

# The identical fixture under the shipped rule must do none of that. This pair
# is what makes the control a control.
run_setup "$CONTROL_MARKER" "$CONTROL_SHIPPED"
full_ran && bad "paired shipped run: full setup ran" \
          || ok "paired shipped run: no full setup (the one-line difference is what fixes it)"
[ "$(branch_now)" = "VERIFY" ] && ok "paired shipped run: verify branch taken" \
                               || bad "paired shipped run: branch $(branch_now)"
[ "$(ssid_now)" = "$GUEST_SSID" ] && ok "paired shipped run: operator SSID untouched ($GUEST_SSID)" \
                                 || bad "paired shipped run: SSID became $(ssid_now)"
[ "$(marker_now)" = "$CONTROL_MARKER" ] && ok "paired shipped run: marker not rewritten" \
                                        || bad "paired shipped run: marker is '$(marker_now)'"

# ------------------------------------------------------------------ summary
echo
printf 'setup-marker-order: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
