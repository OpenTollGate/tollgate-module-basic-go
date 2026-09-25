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
# COVERAGE, MEASURED (not asserted). A live run can emit 83 distinct check ids and
# these 42 cases drive 53 of them red at least once. The ones that DO NOT go red
# here are the ones this rig cannot break -- named, so nobody has to guess:
#   * ssh:* (4)           needs a real router; opt-in behind RHP_SSH
#   * net:tcp-<port> (7)  a stub that stops listening is not a state one rig run
#                         can hold; the port sweep is GREEN in every case
#   * net:icmp-not-a-liveness-test  the source guard: it can only go red if
#                         somebody reintroduces `ping`, which is the edit it forbids
#   * paid:* / paid2:*    the opt-in purchase lanes now RUN offline on a fixture
#                         token (paid-lane-fixture, paid-lane-rejected,
#                         renew-gate-opens, renew-gate-stuck) -- the control the
#                         paid lane never had, which is exactly how a decode bug
#                         that killed EVERY token survived in a merged harness.
#                         The ids whose red path is "the operator did not supply
#                         the thing" (paid:token-supplied, paid2:requested,
#                         paid2:spend-declaration, paid2:token-inspected,
#                         paid2:purchase-accepted, paid2:balance-restored,
#                         paid:session-flip) stay green: a missing token is not a
#                         defect to model, and the case that owns the lane's RED
#                         is the one that reads the customer's data path.
#   * api:whoami-shape, api:identity-shape (SKIPs on 404), artifact:package,
#     captive:spa-noscript-fallback, identity:admin:refs-in-package,
#     ln:no-quote-not-granted, surface:<luci>-luci-307
#                         shape assertions whose red path is a variant the stub
#                         does not produce yet -- a known, listed gap, not a claim
# The fixture token the purchase lanes are driven with is a v3 token built by
# selftest/cashtoken_selftest.py -- non-redeemable, accepted only by the stub, and
# the same file pins the decode it goes through. No real ecash exists in any case.
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
echo "selftest ports: ssh=$SSH_PORT stub=$STUB_PORT portal=$PORTAL_PORT api=$API_PORT" \
     "admin=$ADMIN_PORT captive=$CAPTIVE_PORT luci=$LUCI_PORT tls=$TLS_PORT"

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
        --tls-port "$TLS_PORT" --ssh-port "$SSH_PORT" \
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

harness_run() {  # harness_run <outfile> [VAR=VAL...] [harness args...]
    # Leading VAR=VAL words are handed to the run as environment. That is how the
    # opt-in lanes are driven offline (they are gated on operator env vars), so a
    # case can prove the lane's RED/GREEN behaviour with no hardware and no
    # secret: the stub accepts any non-empty POST body.
    local out="$1"; shift
    local envs=()
    while [ $# -gt 0 ] && printf '%s' "$1" | grep -q '='; do envs+=("$1"); shift; done
    ( cd "$HARNESS" && env ${envs[@]+"${envs[@]}"} \
        RHP_TMPDIR="$TMP_PARENT" \
        RHP_PORTAL_PORT="$PORTAL_PORT" RHP_STUB_PORT="$STUB_PORT" RHP_API_PORT="$API_PORT" \
        RHP_ADMIN_PORT="$ADMIN_PORT" RHP_LUCI_PORT="$LUCI_PORT" RHP_CAPTIVE_PORT="$CAPTIVE_PORT" \
        RHP_SSH_PORT="$SSH_PORT" RHP_TLS_PORT="$TLS_PORT" \
        RHP_429_PACE="${RHP_429_PACE:-1}" \
        RHP_API_HELPER="${RHP_API_HELPER:-}" \
        bash run.sh \
        --artifact-dir "$ART" --router-ip 127.0.0.1 --out "$WORK/evidence" "$@" ) \
        >"$out" 2>"$WORK/err"
}

# check_case <case> <OK|BAD-expectation> ...
check_case() {  # check_case <case> <expect PASS|FAIL> <check id> <outfile> <rc>
    local name="$1" expect="$2" id="$3" out="$4" rc="$5"
    local line
    line="$(grep -E "^RHPCHECK $id " "$out" | head -1)"
    if [ -z "$line" ]; then
        st "$name" BAD "check id '$id' never ran"
        return
    fi
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

env_case() {  # env_case <case> <expect> <id> <scenario json> [VAR=VAL...] [-- harness args...]
    # An OPT-IN lane cannot be driven by a mutation alone: it is gated on an
    # operator env var (a token, a ceiling, an explicit request). This case hands
    # the run those vars, so the lane's own RED/GREEN behaviour is provable with
    # no hardware, no secret and no spend -- the stub accepts any non-empty body.
    local name="$1" expect="$2" id="$3" json="$4"; shift 4
    local envs=() args=() after_sep=0 arg
    for arg in "$@"; do
        if [ "$arg" = "--" ]; then after_sep=1; continue; fi
        if [ "$after_sep" = "1" ]; then args+=("$arg"); else envs+=("$arg"); fi
    done
    printf '%s\n' "$json" > "$WORK/scenario.json"
    start_stub "$WORK/scenario.json" || { st "$name" BAD "stub did not restart"; return; }
    local out="$WORK/out.$name.txt"
    # NOTE: the `--` above is a LOCAL separator only. It is deliberately not
    # forwarded: run.sh rejects an unknown argument, so a stray `--` would abort
    # the run before a single check was emitted (which reads exactly like "the
    # check never ran").
    harness_run "$out" ${envs[@]+"${envs[@]}"} ${args[@]+"${args[@]}"}
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

# 6b. the SECOND purchase -- the club's main loop. Nothing here could see the
#     defect the operator hit on hardware (2026-09-25, pre17): the second
#     purchase restored the balance, the gate stayed shut, and the client's OS
#     was never shown a sign-in prompt. The paid lane above only ever buys ONCE,
#     so the whole re-purchase path was outside the suite. These cases drive it
#     through the stub's renew model with a FIXTURE token -- the stub accepts any
#     non-empty POST body, so no real ecash exists and none can move.
FAKE_TOKEN_2="$(python3 "$SELF_DIR/cashtoken_selftest.py" --emit-v3 210)"

# First: the two lanes' own controls. The paid lane never had one, and that is
# precisely why a decode bug that made EVERY token un-inspectable could sit in a
# merged harness: the default run SKIPs the lane, so nothing ever ran it.
env_case paid-lane-fixture     PASS paid:purchase-accepted '{"renew": "ok"}' \
    RHP_CASHU_TOKEN="$FAKE_TOKEN_2" RHP_SPEND_MAX_SATS=1000
env_case paid-lane-rejected    FAIL paid:purchase-accepted '{"post_reject_token": true}' \
    RHP_CASHU_TOKEN="$FAKE_TOKEN_2" RHP_SPEND_MAX_SATS=1000

PROBE_URL="http://127.0.0.1:$CAPTIVE_PORT/generate_204"
env_case renew-gate-opens    PASS paid2:gate-open '{"renew": "ok"}' \
    RHP_SECOND_PURCHASE=1 RHP_CASHU_TOKEN_2="$FAKE_TOKEN_2" RHP_SPEND_MAX_SATS=1000 \
    RHP_EGRESS_PROBE_URL="$PROBE_URL"
env_case renew-gate-stuck    FAIL paid2:gate-open '{"renew": "stuck"}' \
    RHP_SECOND_PURCHASE=1 RHP_CASHU_TOKEN_2="$FAKE_TOKEN_2" RHP_SPEND_MAX_SATS=1000 \
    RHP_EGRESS_PROBE_URL="$PROBE_URL"
env_case renew-live-session  FAIL paid2:first-allotment-spent '{"renew": "ok", "active_first": true}' \
    RHP_SECOND_PURCHASE=1 RHP_CASHU_TOKEN_2="$FAKE_TOKEN_2" RHP_SPEND_MAX_SATS=1000 \
    RHP_EGRESS_PROBE_URL="$PROBE_URL"
# The two renew outcomes must be OPPOSITE on the same check -- that is the whole
# claim: it reads the gate, not the module's memory of the session.
if grep -q 'RHPCHECK paid2:gate-open PASS' "$WORK/out.renew-gate-opens.txt" \
   && grep -q 'RHPCHECK paid2:gate-open FAIL' "$WORK/out.renew-gate-stuck.txt" \
   && grep -q 'HTTP 307' "$WORK/out.renew-gate-stuck.txt"; then
    st renew-two-outcomes OK "the same check distinguishes an open gate (probe 204) from the reported defect (balance restored, probe still 307 to the splash)"
else
    st renew-two-outcomes BAD "the renew cases did not produce the two opposite probe outcomes"
fi
# ... and the balance is NOT what distinguishes them: in the stuck case the
# module's own /balance answers exactly like the healthy one.
if grep -q 'RHPCHECK paid2:balance-restored PASS' "$WORK/out.renew-gate-stuck.txt"; then
    st renew-balance-lies OK "in the stuck case paid2:balance-restored is PASS while paid2:gate-open is FAIL -- the balance was restored and the gate was not"
else
    st renew-balance-lies BAD "the stuck case did not restore the balance, so it does not model the reported defect"
fi

# The paid lane's spend gate: cashtoken.py's NUT-00 decode. Its off-by-one
# (version read at token[6], the first PAYLOAD character) was found on the paid
# lane's first hardware run and made EVERY token fail inspection -- so the lane
# never reached a purchase, and the default run's SKIP hid it. Pin both the good
# and the malformed path.
CT_OUT="$WORK/cashtoken.txt"
python3 "$SELF_DIR/cashtoken_selftest.py" > "$CT_OUT" 2>&1 || true
while read -r tag name verdict rest; do
    [ "${tag:-}" = "SELFTEST" ] || continue
    st "$name" "$verdict" "${rest:-}"
done < "$CT_OUT"
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

printf '\nSELFTESTRESULT total=%d ok=%d bad=%d\n' "$TOTAL" "$OK" "$BAD"
if [ "$BAD" -gt 0 ]; then
    printf 'SELFTESTEXIT 1\n'
    exit 1
fi
printf 'SELFTESTEXIT 0\n'
exit 0
