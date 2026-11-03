#!/usr/bin/env bash
# selftest.sh — offline planted-regression controls for the check logic in
# checks.sh (no VM, no network; bash only). Proves the lane detects the
# defect classes it exists to catch before you spend a bench window on it:
#
#   - the #899 tear: a listenerless uhttpd section, including the variant
#     where another instance claims the ports so netstat liveness alone
#     would read green;
#   - the #911 shape: apk-tools completing the transaction (package
#     registered, service started, exit 1, guidance suppressed) despite
#     the failing preinst;
#   - crash-loop log signatures.
#
# Usage: bash tests/installer-contract/selftest.sh
# Output idiom: SELFTESTRESULT total=N ok=N bad=M / SELFTESTEXIT 0|1

set -u
SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks.sh
. "$SELF_DIR/checks.sh"

OK=0; BAD=0

expect() { # expect <name> <expected-status> <verdict-line>
    local name="$1" want="$2" line="$3"
    local got; got="$(printf '%s' "$line" | awk '{print $1}')"
    if [ "$got" = "$want" ]; then
        OK=$((OK + 1)); printf 'SELFCASE %-28s ok   (%s)\n' "$name" "$want"
    else
        BAD=$((BAD + 1)); printf 'SELFCASE %-28s BAD  want=%s got=%s: %s\n' "$name" "$want" "$got" "$line"
    fi
}

expect_detail() { # expect_detail <name> <needle> <verdict-line>
    local name="$1" needle="$2" line="$3"
    if printf '%s' "$line" | grep -qF "$needle"; then
        OK=$((OK + 1)); printf 'SELFCASE %-28s ok   (detail names it)\n' "$name"
    else
        BAD=$((BAD + 1)); printf 'SELFCASE %-28s BAD  detail lacks %q: %s\n' "$name" "$needle" "$line"
    fi
}

# --- fixtures ---------------------------------------------------------------

UCI_HEALTHY_BOARD="uhttpd.main='uhttpd'
uhttpd.main.listen_http='0.0.0.0:8090' '[::]:8090'
uhttpd.main.listen_https='0.0.0.0:8443' '[::]:8443'
uhttpd.main.home='/www'
uhttpd.admin='uhttpd'
uhttpd.admin.listen_http='0.0.0.0:8080' '[::]:8080'
uhttpd.admin.listen_https='0.0.0.0:443' '[::]:443'
uhttpd.admin.home='/www/board'"

UCI_HEALTHY_LUCI="uhttpd.main='uhttpd'
uhttpd.main.listen_http='0.0.0.0:8080'
uhttpd.main.listen_https='0.0.0.0:443'
uhttpd.admin='uhttpd'
uhttpd.admin.listen_http='0.0.0.0:8090'
uhttpd.admin.listen_https='0.0.0.0:8443'"

# #899 shape 1: main stripped of both pairs; nobody re-writes 8090/8443.
UCI_TORN_899="uhttpd.main='uhttpd'
uhttpd.main.home='/www'
uhttpd.admin='uhttpd'
uhttpd.admin.listen_http='0.0.0.0:8080'
uhttpd.admin.listen_https='0.0.0.0:443'"

# #899 shape 2 (the stealth variant): main is bare AND the admin instance
# has claimed both pairs — every port answers, so a netstat-liveness check
# would read green while uhttpd.main crash-loops with 'No sockets bound'.
UCI_TORN_899_STEALTH="uhttpd.main='uhttpd'
uhttpd.main.home='/www'
uhttpd.admin='uhttpd'
uhttpd.admin.listen_http='0.0.0.0:8080' '0.0.0.0:8090'
uhttpd.admin.listen_https='0.0.0.0:443' '0.0.0.0:8443'"

LOG_CLEAN="Mon Oct 13 10:00:00 2026 daemon.info procd: Instance uhttpd::instance1 s in a crash loop a
Mon Oct 13 10:00:01 2026 user.info : tollgate-wrt started"

LOG_CRASH="Mon Oct 13 10:00:00 2026 daemon.err uhttpd[1234]: No sockets bound, unable to continue
Mon Oct 13 10:00:03 2026 daemon.err uhttpd[1240]: No sockets bound, unable to continue
Mon Oct 13 10:00:06 2026 kern.warn kernel: Segmentation fault detected in tollgate-wrt[1301]"

REFUSE_CLEAN_LOG='ERROR: root has no password — refusing to install.
Run: passwd root  (then reinstall)'
REFUSE_911_LOG='1 error; 22 packages installed, 6 packages removed'

# --- controls ---------------------------------------------------------------

expect "sections/healthy-board"            PASS "$(ic_uhttpd_sections "$UCI_HEALTHY_BOARD")"
expect "sections/healthy-luci"             PASS "$(ic_uhttpd_sections "$UCI_HEALTHY_LUCI")"
expect "sections/torn-899-bare-main"       FAIL "$(ic_uhttpd_sections "$UCI_TORN_899")"
expect "sections/torn-899-stealth"         FAIL "$(ic_uhttpd_sections "$UCI_TORN_899_STEALTH")"
expect_detail "sections/torn-names-main"   "main" "$(ic_uhttpd_sections "$UCI_TORN_899")"

expect "ownership/healthy-board"           PASS "$(ic_port_ownership board "$UCI_HEALTHY_BOARD")"
expect "ownership/healthy-luci"            PASS "$(ic_port_ownership luci "$UCI_HEALTHY_LUCI")"
expect "ownership/board-on-luci-layout"    FAIL "$(ic_port_ownership board "$UCI_HEALTHY_LUCI")"
expect "ownership/torn-899"                FAIL "$(ic_port_ownership board "$UCI_TORN_899")"
expect "ownership/torn-899-stealth"        FAIL "$(ic_port_ownership board "$UCI_TORN_899_STEALTH")"
expect "ownership/unknown-mapping"         FAIL "$(ic_port_ownership sideways "$UCI_HEALTHY_BOARD")"

expect "crash/clean-log"                   PASS "$(ic_crash_logs "$LOG_CLEAN")"
expect "crash/no-sockets-bound"            FAIL "$(ic_crash_logs "$LOG_CRASH")"

expect "preinst/clean-refusal"             PASS "$(ic_preinst_refusal 1 "$REFUSE_CLEAN_LOG" 0 0)"
expect "preinst/911-completed-transaction" FAIL "$(ic_preinst_refusal 1 "$REFUSE_911_LOG" 1 1)"
expect_detail "preinst/911-names-the-shape" "#911" "$(ic_preinst_refusal 1 "$REFUSE_911_LOG" 1 1)"
expect "preinst/exit-zero-not-gated"       FAIL "$(ic_preinst_refusal 0 "$REFUSE_CLEAN_LOG" 0 0)"
expect "preinst/refused-but-registered"    FAIL "$(ic_preinst_refusal 1 "$REFUSE_CLEAN_LOG" 1 1)"

printf 'SELFTESTRESULT total=%d ok=%d bad=%d\n' "$((OK + BAD))" "$OK" "$BAD"
if [ "$BAD" -eq 0 ]; then printf 'SELFTESTEXIT 0\n'; exit 0; fi
printf 'SELFTESTEXIT 1\n'; exit 1
