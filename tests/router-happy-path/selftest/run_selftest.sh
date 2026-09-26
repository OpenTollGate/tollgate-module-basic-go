#!/usr/bin/env bash
#
# Offline self-test for the router happy-path harness.
#
#   bash tests/router-happy-path/selftest/run_selftest.sh [--keep]
#
# No hardware, no router, no network: a stub stands up every surface the harness
# asserts on localhost, and the harness is then run once clean (must be GREEN)
# and once per mutation, with exactly one thing broken (the matching check id
# must go RED and the run must exit 1).
#
# WHY THIS EXISTS: a check that has never been seen failing is decoration, not
# evidence. A hardware-only suite rots precisely because nobody can see it go red
# on demand.
#
# COVERAGE, MEASURED (not asserted). A live run can emit 76 distinct check ids and
# these cases drive 51 of them red at least once. The ones that DO NOT go red here
# are the ones this rig cannot break -- named, so nobody has to guess:
#   * paid:* (6)          the paid lane is opt-in behind RHP_CASHU_TOKEN; no case
#                         redeems, spends, or touches ecash
#   * ssh:* (4)           needs a real router; opt-in behind RHP_SSH
#   * net:tcp-<port> (6 of 7)  the stub answers the burst on every port in every
#                         other case; only the TLS port is driven red, by
#                         tcp-dead-port (the one port a run truly cannot do without)
#   * vantage:mode        reported, never fatal by construction (either lane is
#                         supported, so there is no state that makes it FAIL)
#   * net:icmp-not-a-liveness-test  the source guard: it can only go red if
#                         somebody reintroduces `ping`, which is the edit it forbids
#   * api:whoami-shape, api:identity-shape (SKIPs on 404), artifact:package,
#     captive:spa-noscript-fallback, identity:admin:refs-in-package,
#     ln:no-quote-not-granted, surface:<luci>-luci-307
#                         shape assertions whose red path is a variant the stub
#                         does not produce yet -- a known, listed gap, not a claim
# Re-derive the list with `--keep`: every per-case transcript is left on disk, and
# the ids that never appear as FAIL across them are the uncovered set.
#
# Prints one line per case:
#   SELFTEST <case> OK|BAD <expectation>
# Exit 0 = the harness detects every mutation (and stays green when clean).
#
set -u

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
HARNESS="$(cd "$SELF_DIR/.." && pwd)"
TMP_PARENT="${RHP_TMPDIR:-/var/tmp}"
WORK="$(mktemp -d "$TMP_PARENT/rhp-selftest.XXXXXX")" || exit 2
KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1

STUB_PID=""
cleanup() {
    if [ -n "$STUB_PID" ]; then kill "$STUB_PID" 2>/dev/null; wait "$STUB_PID" 2>/dev/null; fi
    if [ "$KEEP" = "1" ]; then echo "kept: $WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT INT TERM

TOTAL=0; OK=0; BAD=0
st() {  # st <case> <OK|BAD> <expectation>
    TOTAL=$((TOTAL + 1))
    case "$2" in OK) OK=$((OK + 1)) ;; *) BAD=$((BAD + 1)) ;; esac
    printf 'SELFTEST %s %s %s\n' "$1" "$2" "$3"
}

# --------------------------------------------------------------------------
# Ports: high, unprivileged and free
# --------------------------------------------------------------------------
tcp_busy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
free_port() { local p="$1"; while tcp_busy "$p"; do p=$((p + 1)); done; printf '%s\n' "$p"; }
BASE=$((40000 + RANDOM % 1500))
SSH_PORT="$(free_port "$BASE")"
STUB_PORT="$(free_port $((SSH_PORT + 1)))"
PORTAL_PORT="$(free_port $((STUB_PORT + 1)))"
API_PORT="$(free_port $((PORTAL_PORT + 1)))"
ADMIN_PORT="$(free_port $((API_PORT + 1)))"
CAPTIVE_PORT="$(free_port $((ADMIN_PORT + 1)))"
LUCI_PORT="$(free_port $((CAPTIVE_PORT + 1)))"
TLS_PORT="$(free_port $((LUCI_PORT + 1)))"
# A second LIVE https listener that the harness does not know about: the decoy for
# the TCP-credit rule (case 9d-bis). It is never in the section-0 sweep, so the
# only thing that can reach it is the :LUCI 307 Location.
ALT_PORT="$(free_port $((TLS_PORT + 1)))"
echo "selftest ports: ssh=$SSH_PORT stub=$STUB_PORT portal=$PORTAL_PORT api=$API_PORT" \
     "admin=$ADMIN_PORT captive=$CAPTIVE_PORT luci=$LUCI_PORT tls=$TLS_PORT alt=$ALT_PORT (decoy)"

# --------------------------------------------------------------------------
# Fixtures: a fake extracted package whose docroots the stub serves verbatim
# --------------------------------------------------------------------------
ART="$WORK/artifact"
PDOC="$ART/etc/tollgate/tollgate-captive-portal-site"
ADOC="$ART/www/tollgate"
mkdir -p "$PDOC/assets" "$PDOC/locales" "$ADOC/assets"

cat > "$PDOC/splash.html" <<'HTML'
<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8" />
<title>Tollgate Captive Portal</title>
<link rel="manifest" href="/manifest.json" />
<script type="module" crossorigin src="/assets/index-deadbeef.js"></script>
<script type="module" crossorigin src="/assets/portal-cafe1234.js"></script>
<link rel="stylesheet" crossorigin href="/assets/index-abcdef12.css">
<link rel="icon" href="/favicon.ico" />
</head><body><div id="root"></div>
<noscript><p>JavaScript is required.</p></noscript>
</body></html>
HTML
cp "$PDOC/splash.html" "$PDOC/balance.html"
printf '%s\n' '{"name":"stub portal"}' > "$PDOC/manifest.json"
printf '%s\n' 'locale-stub' > "$PDOC/locales/en.json"
head -c 512 /dev/zero | tr '\0' 'F' > "$PDOC/favicon.ico"
printf '%s\n' '/* css stub */'   > "$PDOC/assets/index-abcdef12.css"
printf '%s\n' '/* entry stub */' > "$PDOC/assets/index-deadbeef.js"
printf '%s\n' '/* chunk stub */' > "$PDOC/assets/portal-cafe1234.js"
printf '%s\n' '/* qr stub */'    > "$PDOC/assets/qr-scanner.min-1234abcd.js"

cat > "$ADOC/index.html" <<'HTML'
<!doctype html>
<html lang="en"><head><meta charset="UTF-8" />
<title>TollGate Admin</title>
<script type="module" crossorigin src="/assets/index-abcdef01.js"></script>
<link rel="stylesheet" crossorigin href="/assets/index-87654321.css">
<link rel="manifest" href="manifest.json" />
</head><body><div id="app"></div></body></html>
HTML
printf '%s\n' '{"name":"stub admin"}' > "$ADOC/manifest.json"
printf '%s\n' '/* admin entry */' > "$ADOC/assets/index-abcdef01.js"
printf '%s\n' '/* admin css */'   > "$ADOC/assets/index-87654321.css"

CERT=""; KEY=""
if command -v openssl >/dev/null 2>&1; then
    if openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=rhp-stub \
        -keyout "$WORK/key.pem" -out "$WORK/cert.pem" >/dev/null 2>&1; then
        CERT="$WORK/cert.pem"; KEY="$WORK/key.pem"
    fi
fi
[ -z "$CERT" ] && echo "selftest: no openssl -> the :$LUCI_PORT https target is served WITHOUT TLS (curl -k still reaches it)"

# --------------------------------------------------------------------------
# Stub lifecycle
# --------------------------------------------------------------------------
start_stub() {  # start_stub [scenario file]
    if [ -n "$STUB_PID" ]; then kill "$STUB_PID" 2>/dev/null; wait "$STUB_PID" 2>/dev/null; STUB_PID=""; fi
    local scenario_args=()
    [ -n "${1:-}" ] && scenario_args=(--scenario "$1")
    : > "$WORK/stub.log"
    python3 "$SELF_DIR/stub_router.py" \
        --host 127.0.0.1 \
        --portal-docroot "$PDOC" --admin-docroot "$ADOC" \
        --portal-port "$PORTAL_PORT" --stub-port "$STUB_PORT" --api-port "$API_PORT" \
        --admin-port "$ADMIN_PORT" --luci-port "$LUCI_PORT" --captive-port "$CAPTIVE_PORT" \
        --tls-port "$TLS_PORT" --ssh-port "$SSH_PORT" --alt-port "$ALT_PORT" \
        --cert "$CERT" --key "$KEY" "${scenario_args[@]}" \
        > "$WORK/stub.log" 2>&1 &
    STUB_PID=$!
    for _ in $(seq 1 60); do
        grep -q stub-ready "$WORK/stub.log" 2>/dev/null && return 0
        sleep 0.25
    done
    echo "stub failed to start:"; cat "$WORK/stub.log"
    return 1
}

harness_run() {  # harness_run <outfile> [harness args...]
    local out="$1"; shift
    # --vantage is PINNED in the rig (mgmt by default, RHP_HARNESS_VANTAGE to
    # override, and a case may pass its own --vantage last to win). The vantage a
    # live run gets is derived from the box; on a busy CI runner that derivation
    # can flip to guest simply because :8090 did not answer the probe in time, and
    # then every mgmt-lane case below would assert against a lane that never ran.
    # The rig is for deterministic RED/GREEN, so it pins the lane -- and the
    # derivation itself is asserted by its own case (vantage-auto-mgmt).
    ( cd "$HARNESS" && RHP_TMPDIR="$TMP_PARENT" \
        RHP_PORTAL_PORT="$PORTAL_PORT" RHP_STUB_PORT="$STUB_PORT" RHP_API_PORT="$API_PORT" \
        RHP_ADMIN_PORT="$ADMIN_PORT" RHP_LUCI_PORT="$LUCI_PORT" RHP_CAPTIVE_PORT="$CAPTIVE_PORT" \
        RHP_SSH_PORT="$SSH_PORT" RHP_TLS_PORT="$TLS_PORT" \
        RHP_429_PACE="${RHP_429_PACE:-1}" \
        RHP_TCP_TRIES="${RHP_TCP_TRIES:-3}" \
        RHP_TCP_BACKOFF="${RHP_TCP_BACKOFF:-1}" \
        RHP_API_HELPER="${RHP_API_HELPER:-}" \
        bash run.sh \
        --artifact-dir "$ART" --router-ip 127.0.0.1 --out "$WORK/evidence" \
        --vantage "${RHP_HARNESS_VANTAGE:-mgmt}" "$@" ) \
        >"$out" 2>"$WORK/err"
}

# check_case <case> <OK|BAD-expectation> ...
check_case() {  # check_case <case> <expect PASS|FAIL|WARN> <check id> <outfile> <rc>
    local name="$1" expect="$2" id="$3" out="$4" rc="$5"
    local line n
    # The TERMINAL status is the run's answer. A section-0 liveness note is
    # RHPPROVISIONAL -- deliberately not a RHPCHECK line at all, because the
    # verdict has to resolve it (to FAIL, or to WARN when a later check reached
    # the port). Judging the first matching line instead would read the
    # pre-verdict state and call a refuted port red; judging the LAST line of a
    # terminal status asserts what the run actually reports. Exactly one RHPCHECK
    # line per id, asserted here: an id that is both red and green in one
    # transcript is the confusion this whole rig exists to prevent, and a
    # grep-based gate reading `^RHPCHECK <id> ` cannot see a PROVISIONAL note at
    # all.
    n="$(grep -cE "^RHPCHECK $id (PASS|FAIL|SKIP|WARN) " "$out")"
    if [ "$n" = "0" ]; then
        if grep -qE "^RHPPROVISIONAL $id " "$out"; then
            st "$name" BAD "$id has a PROVISIONAL note but NO terminal resolution: the verdict never resolved the section-0 burst"
        else
            st "$name" BAD "check id '$id' never ran"
        fi
        return
    fi
    if [ "$n" != "1" ]; then
        st "$name" BAD "$id has $n terminal lines, want exactly 1: $(grep -E "^RHPCHECK $id (PASS|FAIL|SKIP|WARN) " "$out" | cut -d' ' -f3 | tr '\n' ' ')"
        return
    fi
    line="$(grep -E "^RHPCHECK $id (PASS|FAIL|SKIP|WARN) " "$out")"
    local got
    got="$(printf '%s' "$line" | cut -d' ' -f3)"
    if [ "$got" != "$expect" ]; then
        st "$name" BAD "$id is '$got', expected $expect"
        return
    fi
    if [ "$expect" = "FAIL" ] && [ "$rc" != "1" ]; then
        st "$name" BAD "$id went red but the run exited $rc (want 1)"
        return
    fi
    if [ "$expect" = "PASS" ] && [ "$rc" != "0" ]; then
        st "$name" BAD "$id stayed green but the run exited $rc"
        return
    fi
    if [ "$expect" = "WARN" ] && [ "$rc" != "0" ]; then
        st "$name" BAD "$id was demoted to WARNING but the run still exited $rc (a warning is not fatal, by definition)"
        return
    fi
    st "$name" OK "$id $expect: $(printf '%s' "$line" | cut -c10-130)"
}

mut_case() {  # mut_case <case> <expect> <id> <scenario json> [harness args...]
    local name="$1" expect="$2" id="$3" json="$4"; shift 4
    printf '%s\n' "$json" > "$WORK/scenario.json"
    start_stub "$WORK/scenario.json" || { st "$name" BAD "stub did not restart"; return; }
    local out="$WORK/out.$name.txt"
    harness_run "$out" "$@"
    check_case "$name" "$expect" "$id" "$out" "$?"
}

# --------------------------------------------------------------------------
# Baseline
# --------------------------------------------------------------------------
echo "--- baseline (clean rig must be GREEN)"
start_stub "" || { st baseline BAD "stub did not start"; exit 1; }
harness_run "$WORK/out.baseline.txt"
rc=$?
if [ "$rc" = "0" ]; then
    st baseline OK "$(grep '^RHPRESULT' "$WORK/out.baseline.txt")"
else
    st baseline BAD "exit=$rc failing=$(grep '^RHPFAILED' "$WORK/out.baseline.txt")"
    sed -n '1,200p' "$WORK/out.baseline.txt"
fi

echo "--- mutations (each must flip exactly the check it targets)"
# 1. build identity
mut_case identity-asset-byte  FAIL identity:portal:assets                 '{"portal_asset_byte": true}'
mut_case entry-chunk-missing  FAIL identity:portal:assets                 '{"portal_entry_missing": true}'
mut_case entry-not-hashed     FAIL identity:portal:content-hashed-entry   '{"portal_no_hash": true}'
# 2. surfaces
mut_case stub-no-redirect     FAIL "surface:$STUB_PORT-cache-bust-stub"            '{"stub_no_redirect": true}'
mut_case stub-wrong-port      FAIL "surface:$STUB_PORT-redirects-to-$PORTAL_PORT" '{"stub_wrong_port": true}'
mut_case luci-dead-target     FAIL "surface:$LUCI_PORT-target-200"                '{"luci_307_no_target": true}'
mut_case admin-spa-missing    FAIL "surface:$ADMIN_PORT-admin-spa"                '{"admin_entry_missing": true}'
# 3. captive chain
mut_case captive-no-307       FAIL "surface:$CAPTIVE_PORT-captive-307"     '{"captive_200": true}'
mut_case captive-no-redir     FAIL "surface:$CAPTIVE_PORT-redir-encodes-original" '{"captive_no_redir": true}'
mut_case stub-no-noscript     FAIL captive:stub-noscript-fallback          '{"stub_no_noscript": true}'
mut_case spa-on-stub-port     FAIL "captive:spa-not-on-$STUB_PORT"         '{"spa_on_stub_port": true}'
mut_case spa-no-root-el       FAIL captive:chain-ends-200                  '{"portal_no_root_el": true}'
# 4. API shapes
mut_case root-wrong-kind      FAIL api:root-kind10021                      '{"root_kind": 9999}'
mut_case root-degraded        FAIL api:root-full-mode                      '{"root_degraded": true}'
mut_case whoami-sentinel      FAIL api:whoami-not-sentinel                 '{"whoami_sentinel": true}'
mut_case balance-malformed    FAIL api:balance-shape                       '{"balance_malformed": true}'
mut_case balance-active       FAIL pre:idle                               '{"balance_active": true}'
mut_case usage-bad            FAIL api:usage-shape                         '{"usage_bad": true}'
mut_case cors-no-methods      FAIL api:cors-preflight                      '{"no_cors_preflight": true}'
mut_case session-state-shipped PASS api:session-state                      '{"session_state": true}'
# 5. Lightning quote contract
mut_case ln-200               FAIL ln:no-quote-status-poll                 '{"ln_200": true}'
mut_case ln-wrong-error       FAIL ln:no-quote-status-poll                 '{"ln_wrong_error": true}'
# 6. money path
mut_case empty-token-accepted FAIL money:empty-token-rejected              '{"empty_token_ok": true}'
# 7. the module's rate limiter (root handler, per client IP). A throttle must not
#    read as a regression: a 429 that is retried away leaves the run GREEN, only a
#    429 that survives every attempt is red, and the transcript has to say which.
#    Three cases, because the harness retries in TWO places -- run.sh's fetch()
#    and request() in lib/api_check.py.
mut_case http-429-recovered   PASS api:root-kind10021 '{"api_429_first": 3}'
if grep -q 'RHPNOTE http: HTTP 429 from' "$WORK/out.http-429-recovered.txt"; then
    st http-429-recovered-note OK "the transcript names the throttle and the retry instead of failing it"
else
    st http-429-recovered-note BAD "no Retry-After retry note in the transcript"
fi
# deeper throttle: run.sh's bounded fetch() uses up its three retries on the
# surface check, but request() must still retry its own 429 and answer PASS --
# proving the python path is not relying on the shell one.
printf '%s\n' '{"api_429_first": 5}' > "$WORK/scenario.json"
start_stub "$WORK/scenario.json" || st http-429-api-retry BAD "stub did not restart"
harness_run "$WORK/out.http-429-api-retry.txt"
rc=$?
rootline="$(grep -E '^RHPCHECK api:root-kind10021 ' "$WORK/out.http-429-api-retry.txt" | head -1 | cut -d' ' -f3)"
surfline="$(grep -E "^RHPCHECK surface:$API_PORT-api " "$WORK/out.http-429-api-retry.txt" | head -1 | cut -d' ' -f3)"
if [ "$rootline" = "PASS" ] && [ "$surfline" = "FAIL" ] && [ "$rc" = "1" ]; then
    st http-429-api-retry OK "lib/api_check.py retried its own 429 to PASS while run.sh's bounded fetch() exhausted its tries (surface FAIL, exit 1)"
else
    st http-429-api-retry BAD "root=$rootline surface=$surfline rc=$rc (want root PASS, surface FAIL, rc 1)"
fi
mut_case http-429-always      FAIL api:root-kind10021 '{"api_429_always": true, "api_429_retry_after": 0}'

# the optional content-hash pin must be falsifiable too (no mutation needed)
mut_case pin-mismatch         FAIL identity:expected-entry                 '{}' \
    --expect-entry index-deadbeef.js:999:0000000000000000000000000000000000000000000000000000000000000000

# 8. the fold() reconciliation (run.sh). A helper that DIES, or that exits 0
#    having emitted nothing, used to remove its own checks from the tally without
#    a single FAIL -- a green run that never ran the phase. Both halves are
#    exercised through the RHP_API_HELPER seam: the run must go red and name the
#    phase in `helper:<label>`.
RHP_API_HELPER=/bin/false
export RHP_API_HELPER
mut_case helper-dies          FAIL helper:api                               '{}'
RHP_API_HELPER=/bin/true
export RHP_API_HELPER
mut_case helper-silent        FAIL helper:api                               '{}'
unset RHP_API_HELPER

# 9. vantage + the section-0 TCP liveness burst.
#
# A human tester (and the review club) has exactly one vantage: a client on the
# guest network. From there :8090 is firewall-blocked BY DESIGN
# (31-admin-board-not-guest-reachable.nft), and the harness's first connect burst
# races the box's own convergence. Neither may paint a fatal FAIL, or a red line
# stops meaning "a real defect" -- the whole point of the harness.
#
# These cases drive the three outcomes the card names: guest-vantage 000 => PASS,
# a port that fails once and answers on retry => not fatal, a port that answers
# nowhere in the run => still fatal. Two more cover the negative control (the
# guard must be provably inert when :8090 answers) and the "answers later in the
# same run" demotion.
echo "--- vantage + the TCP liveness burst"
assert_in() {  # assert_in <case> <file> <ERE> <what>
    local name="$1" file="$2" re="$3" what="$4"
    if grep -qE "$re" "$file"; then
        st "$name" OK "$what"
    else
        st "$name" BAD "$what -- no line matching /$re/ in $(basename "$file")"
    fi
}

assert_not_in() {  # assert_not_in <case> <file> <ERE> <what>
    local name="$1" file="$2" re="$3" what="$4"
    if grep -qE "$re" "$file"; then
        st "$name" BAD "$what -- but a line matching /$re/ IS in $(basename "$file"): $(grep -m1 -E "$re" "$file" | cut -c1-120)"
    else
        st "$name" OK "$what"
    fi
}

assert_after() {  # assert_after <case> <file> <marker ERE> <ERE> <what>
    # the terminal line must come from the verdict, not from the burst: a port can
    # only be called dead once the WHOLE run has failed to reach it.
    local name="$1" file="$2" marker="$3" re="$4" what="$5"
    local m l
    m="$(grep -nE "$marker" "$file" | head -1 | cut -d: -f1)"
    l="$(grep -nE "$re" "$file" | head -1 | cut -d: -f1)"
    if [ -z "$l" ]; then
        st "$name" BAD "$what -- no line matching /$re/ in $(basename "$file")"
    elif [ -n "$m" ] && [ "$l" -gt "$m" ]; then
        st "$name" OK "$what"
    else
        st "$name" BAD "$what -- the line is at $l, NOT after the verdict marker (line ${m:-missing})"
    fi
}

# 9a-pre. the baseline ran with --vantage mgmt PINNED, and it says so: the rig
#        never leaves the lane it is asserting to a derivation (that derivation
#        has its own case below). This is also what keeps `admin-spa-missing`
#        meaningful: it asserts a check id that only runs in the mgmt lane.
assert_in baseline-vantage "$WORK/out.baseline.txt" \
    "^RHPCHECK vantage:mode PASS 'mgmt'" \
    "the rig's baseline pins the mgmt lane and the transcript names it"

# 9a-bis. the derivation itself: with :$ADMIN_PORT answering, --vantage auto must
#         resolve mgmt and run the mgmt admin assertion. Slow-probe tolerant on
#         purpose (a busy runner must not flip the lane).
RHP_HARNESS_VANTAGE=auto
RHP_TCP_TRIES=8
mut_case vantage-auto-mgmt PASS "surface:$ADMIN_PORT-admin-spa" '{}' --vantage auto
RHP_HARNESS_VANTAGE=mgmt
RHP_TCP_TRIES=3
assert_in vantage-auto-mgmt-mode "$WORK/out.vantage-auto-mgmt.txt" \
    "^RHPCHECK vantage:mode PASS .*auto-detected 'mgmt'" \
    "auto-detection resolved mgmt from :$ADMIN_PORT answering"

# 9a. guest vantage, the #566 guard working as designed: :8090 answers nothing.
#     The dedicated check PASSES on 000, the preflight line is a WARNING (never a
#     fatal FAIL), and the admin board is still spoken for -- by the mgmt/on-box
#     lane, named in the transcript, not silently dropped.
RHP_TCP_TRIES="${RHP_TCP_TRIES:-3}"
RHP_TCP_BACKOFF=1
mut_case guest-8090-guard PASS "surface:$ADMIN_PORT-admin-spa-not-guest-reachable" \
    '{"unbound_ports": ["admin"]}' --vantage guest
guest_out="$WORK/out.guest-8090-guard.txt"
assert_in guest-8090-guard-tcp-warn "$guest_out" \
    "^RHPCHECK net:tcp-$ADMIN_PORT WARN " \
    "the section-0 line for the blocked :$ADMIN_PORT is a WARNING, not a fatal preflight FAIL"
assert_in guest-8090-guard-identity-skip "$guest_out" \
    "^RHPCHECK identity:admin:entry SKIP .*(mgmt|on-box)" \
    "the admin build-identity lane is a named SKIP that points at the mgmt/on-box lane (never a silent skip)"
assert_in guest-8090-guard-green "$guest_out" \
    "^RHPEXIT 0$" \
    "the whole guest-vantage run is GREEN (exit 0) on a box whose admin board is correctly blocked -- the point of the vantage notion"
assert_in guest-8090-guard-no-fatal "$guest_out" \
    "^RHPRESULT total=[0-9]+ pass=[0-9]+ fail=0 skip=[0-9]+ warn=[0-9]+" \
    "the guest-vantage run reports fail=0: no check can only pass from a management vantage"
assert_not_in guest-8090-guard-no-dangling-fail "$guest_out" \
    "^RHPCHECK net:tcp-$ADMIN_PORT FAIL " \
    "no FAIL line is printed for the port the guest lane cannot reach (a WARNING, not a red line)"

# 9b. negative control for the same check: the guard is INERT (:8090 answers a
#     br-lan client) -> the guest-vantage check must go red. Without this, 9a
#     would pass on a harness that always prints the same line.
mut_case guest-8090-guard-inert FAIL "surface:$ADMIN_PORT-admin-spa-not-guest-reachable" \
    '{}' --vantage guest

# 9c. a port that fails the first burst and answers on the retry is NOT fatal: the
#     retry recovers it and the line goes green with the attempt named. The 3 s
#     backoff against the stub's 1 s late bind is what makes "attempt 2" certain
#     rather than a race.
RHP_TCP_BACKOFF=3
mut_case tcp-retry-recovers PASS "net:tcp-$SSH_PORT" \
    '{"ssh_late_bind_s": 1.0}'
RHP_TCP_BACKOFF=1
assert_in tcp-retry-recovers-note "$WORK/out.tcp-retry-recovers.txt" \
    "^RHPCHECK net:tcp-$SSH_PORT PASS .*attempt 2/$RHP_TCP_TRIES" \
    "the recovered line names the attempt that answered, so a retried connect is visibly not the first one"

# 9d. a port that answers nowhere in the run is STILL fatal (the TLS port is not
#     decoration: the :$LUCI_PORT https redirect is asserted against it). The
#     section-0 line for it is RHPPROVISIONAL -- the run has not looked at the
#     rest of the box yet -- and the FAIL comes from the verdict, where it is
#     final.
mut_case tcp-dead-port FAIL "net:tcp-$TLS_PORT" '{"unbound_ports": ["tls"]}'
dead_out="$WORK/out.tcp-dead-port.txt"
assert_in tcp-dead-port-provisional "$dead_out" \
    "^RHPPROVISIONAL net:tcp-$TLS_PORT " \
    "the section-0 line says PROVISIONAL, i.e. explicitly not yet a verdict (and it is not a RHPCHECK line, so a grep for the id's verdict sees exactly one)"
assert_after tcp-dead-port-final "$dead_out" \
    "^===== verdict: resolving the section-0 liveness burst" \
    "^RHPCHECK net:tcp-$TLS_PORT FAIL " \
    "the FATAL line for a port nothing in the run reaches is printed by the verdict, after the reconciliation"
assert_in tcp-dead-port-named "$dead_out" \
    "^RHPFAILED .*net:tcp-$TLS_PORT" \
    "the dead port is named in RHPFAILED (the summary a gate reads)"

# 9d-bis. the DECOY: the credit rule must credit the port a check actually
#         REACHED, not an id that merely mentions it. Here :$TLS_PORT is dead
#         while the :$LUCI_PORT redirect lands on a DIFFERENT, live https port
#         (:$ALT_PORT, which the harness never sweeps). So
#         surface:$LUCI_PORT-target-200 genuinely PASSes -- against the other
#         port -- and a rule of the shape "some PASS id names this port" demotes
#         the dead :$TLS_PORT to a WARNING and exits 0. This is the round-1
#         review's finding F1 in one scenario; the run must keep it FAIL.
mut_case tcp-dead-tls-not-credited FAIL "net:tcp-$TLS_PORT" \
    '{"luci_307_to_alt": true, "unbound_ports": ["tls"]}'
decoy_out="$WORK/out.tcp-dead-tls-not-credited.txt"
assert_in tcp-dead-tls-decoy-passes "$decoy_out" \
    "^RHPCHECK surface:$LUCI_PORT-target-200 PASS " \
    "the decoy really does PASS: the :$LUCI_PORT redirect answered 200 on a live https port"
assert_not_in tcp-dead-tls-not-demoted "$decoy_out" \
    "^RHPCHECK net:tcp-$TLS_PORT WARN " \
    "the dead :$TLS_PORT is NOT demoted by a PASS that reached a different port"
assert_in tcp-dead-tls-still-fatal "$decoy_out" \
    "^RHPFAILED .*net:tcp-$TLS_PORT" \
    "the dead :$TLS_PORT stays in RHPFAILED even with a live sibling https port in the run"

# 9e. a port that fails the burst but answers LATER in the run: the PROVISIONAL
#     section-0 line is resolved to a WARNING that quotes the later PASS -- the
#     transcript says which line refuted it, and it never contains a FAIL for a
#     port the run itself went on to use.
mut_case tcp-refuted-later WARN "net:tcp-$ADMIN_PORT" \
    '{"admin_bind_on_first_http": true}' --vantage mgmt
refuted_out="$WORK/out.tcp-refuted-later.txt"
assert_in tcp-refuted-later-quotes "$refuted_out" \
    "^RHPCHECK net:tcp-$ADMIN_PORT WARN .*surface:$ADMIN_PORT-admin-spa PASS" \
    "the demoted WARNING quotes the later PASS that refuted the burst"
assert_in tcp-refuted-later-provisional "$refuted_out" \
    "^RHPPROVISIONAL net:tcp-$ADMIN_PORT " \
    "the same id's section-0 line is the PROVISIONAL note: one id, one RHPCHECK verdict, plus the pre-verdict note"
assert_not_in tcp-refuted-later-no-fail "$refuted_out" \
    "^RHPCHECK net:tcp-$ADMIN_PORT FAIL " \
    "the transcript contains NO FAIL line for the port the run itself reached later"
assert_in tcp-refuted-later-warned "$refuted_out" \
    "^RHPWARNED .*net:tcp-$ADMIN_PORT" \
    "the demotion lands in RHPWARNED (greppable, and never fatal)"
assert_not_in tcp-refuted-later-not-fatal "$refuted_out" \
    "^RHPFAILED .*net:tcp-$ADMIN_PORT" \
    "the demoted port is NOT in RHPFAILED"

printf '\nSELFTESTRESULT total=%d ok=%d bad=%d\n' "$TOTAL" "$OK" "$BAD"
if [ "$BAD" -gt 0 ]; then
    printf 'SELFTESTEXIT 1\n'
    exit 1
fi
printf 'SELFTESTEXIT 0\n'
exit 0
