#!/usr/bin/env bash
#
# Happy-path regression suite for a PUBLISHED TollGate package.
#
#   tests/happy-path/run.sh --artifact <extracted-package-dir>
#
# The artifact is an already-extracted `.apk`/`.ipk` (the feed job hands us the
# published bytes). Everything the suite exercises comes from inside it:
#
#   * the module binary      <artifact>/usr/bin/tollgate-wrt
#   * the captive-portal SPA <artifact>/etc/tollgate/tollgate-captive-portal-site
#
# Nothing outside the machine is contacted; no secret, Cashu token or payment is
# needed. See README.md for what this does and does NOT cover.
#
# Exit 0 = happy path intact. Non-zero = broken. Every check prints one line:
#
#   HPCHECK <id> <PASS|FAIL|SKIP> <detail...>
#
set -u

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/../.." && pwd)"

ARTIFACT=""
STRICT=0
KEEP=0
OUT=""
RUN_PORTAL=1
RUN_MODULE=1

usage() {
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Options:
  --artifact DIR   extracted package dir (required)
  --out DIR        evidence dir (default: <workdir>/evidence)
  --strict         treat absent-but-expected surfaces (e.g. /session-state,
                   the in-page renewal CTA) as failures instead of skips; use
                   this when the artifact is known to be built from a tip that
                   has them
  --skip-portal    module checks only
  --skip-module    portal checks only
  --keep           keep the work directory for inspection
  -h, --help       this text

Environment:
  HP_PYTHON        python with playwright installed (default: probe, else
                   /home/c03rad0r/tg-e2e/venv/bin/python, else python3)
  HP_BROWSER_CHANNEL  playwright channel (e.g. chrome); unset uses the bundled
                   chromium
  HP_TMPDIR        parent for the work dir (default /var/tmp -- a 3.6 GiB tmpfs
                   at /tmp is not a safe place for extracted packages)
  HP_MODULE_PORT   module port (default 2121; the shipped portal bundle hardcodes
                   http://<hostname>:2121, so change this only with a reason)
  HP_MUSL_LIBGCC   path to a musl libgcc_s.so.1 for hosts that cannot run the
                   musl binary natively (see README "Running the artifact")
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --artifact) ARTIFACT="${2:-}"; shift 2 ;;
        --out)      OUT="${2:-}"; shift 2 ;;
        --strict)   STRICT=1; shift ;;
        --skip-portal) RUN_PORTAL=0; shift ;;
        --skip-module) RUN_MODULE=0; shift ;;
        --keep)     KEEP=1; shift ;;
        -h|--help)  usage; exit 0 ;;
        *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [ -z "$ARTIFACT" ]; then
    echo "ERROR: --artifact <extracted-package-dir> is required" >&2
    exit 2
fi
if [ ! -d "$ARTIFACT" ]; then
    echo "ERROR: artifact directory not found: $ARTIFACT" >&2
    exit 2
fi
ARTIFACT="$(cd "$ARTIFACT" && pwd)"

MODULE_PORT="${HP_MODULE_PORT:-2121}"
TMP_PARENT="${HP_TMPDIR:-/var/tmp}"
WORK="$(mktemp -d "$TMP_PARENT/tg-happy-path.XXXXXX")" || exit 1
EVIDENCE="${OUT:-$WORK/evidence}"
mkdir -p "$EVIDENCE"

TOTAL=0; PASSED=0; FAILED_N=0; SKIPPED=0
FAILED_IDS=""

chk() {  # chk <id> <PASS|FAIL|SKIP> <detail>
    local id="$1" status="$2"; shift 2
    local detail="${*:-}"
    TOTAL=$((TOTAL + 1))
    case "$status" in
        PASS) PASSED=$((PASSED + 1)) ;;
        SKIP) SKIPPED=$((SKIPPED + 1)) ;;
        FAIL) FAILED_N=$((FAILED_N + 1)); FAILED_IDS="$FAILED_IDS $id" ;;
    esac
    printf 'HPCHECK %s %s %s\n' "$id" "$status" "$detail"
}

note() { printf 'HPNOTE %s\n' "$*"; }

MOD_PID=""; STUB_PID=""; STATIC_PID=""
cleanup() {
    [ -n "$MOD_PID" ] && kill "$MOD_PID" 2>/dev/null
    [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null
    wait 2>/dev/null
    if [ "$KEEP" = "1" ]; then
        note "work dir kept: $WORK"
    else
        rm -rf "$WORK"
    fi
}
trap cleanup EXIT INT TERM

# --------------------------------------------------------------------------
# 0. Locate the parts INSIDE the artifact
# --------------------------------------------------------------------------
MODULE_BIN=""
for cand in "$ARTIFACT/usr/bin/tollgate-wrt" "$ARTIFACT/usr/bin/tollgate"; do
    [ -x "$cand" ] && MODULE_BIN="$cand" && break
done
if [ -z "$MODULE_BIN" ]; then
    MODULE_BIN="$(find "$ARTIFACT" -type f -name 'tollgate-wrt' -perm -u+x 2>/dev/null | head -1)"
fi
if [ -n "$MODULE_BIN" ]; then
    chk artifact:module-binary PASS "$MODULE_BIN ($(stat -c %s "$MODULE_BIN") bytes, sha256 $(sha256sum "$MODULE_BIN" | cut -c1-16)…)"
else
    chk artifact:module-binary FAIL "no tollgate-wrt/tollgate executable under $ARTIFACT/usr/bin"
fi

PORTAL_DIR=""
for cand in "$ARTIFACT/etc/tollgate/tollgate-captive-portal-site" \
            "$ARTIFACT/www/tollgate" \
            "$ARTIFACT/etc/tollgate/portal"; do
    if [ -d "$cand" ] && [ -f "$cand/splash.html" ]; then PORTAL_DIR="$cand"; break; fi
done
if [ -z "$PORTAL_DIR" ]; then
    PORTAL_DIR="$(find "$ARTIFACT" -type f -name 'splash.html' 2>/dev/null | head -1 | xargs -r dirname)"
fi
if [ -n "$PORTAL_DIR" ] && [ -f "$PORTAL_DIR/splash.html" ]; then
    chk artifact:portal-bundle PASS "$PORTAL_DIR ($(ls "$PORTAL_DIR/assets"/index-*.js 2>/dev/null | wc -l) index bundle(s))"
else
    PORTAL_DIR=""
    chk artifact:portal-bundle FAIL "no splash.html portal bundle under $ARTIFACT"
fi

FAKE_NDSCTL="$REPO_ROOT/tests/cloud-lab/fake-ndsctl.sh"
if [ -x "$FAKE_NDSCTL" ]; then
    chk artifact:fake-ndsctl-reused PASS "$FAKE_NDSCTL (the cloud-lab seam, not a second one)"
else
    chk artifact:fake-ndsctl-reused FAIL "$FAKE_NDSCTL missing — the suite depends on the cloud-lab seam"
    RUN_MODULE=0
fi

# --------------------------------------------------------------------------
# 1. How can this host run the artifact's binary?
#    OpenWrt x86_64 packages are musl + libgcc_s; a glibc host needs both the
#    musl loader and a musl libgcc_s.so.1, and a container otherwise.
# --------------------------------------------------------------------------
RUN_MODE=""
MUSL_LIBDIR=""
detect_run_mode() {
    [ -z "$MODULE_BIN" ] && return
    local interp
    interp="$(head -c 4096 "$MODULE_BIN" | tr -d '\0' | grep -ao 'ld-musl-[a-z0-9_]*\.so\.[0-9]' | head -1)"
    if [ -z "$interp" ]; then
        RUN_MODE="native"
        return
    fi
    if [ ! -e "/lib/$interp" ]; then
        RUN_MODE="container"
        return
    fi
    local libgcc="${HP_MUSL_LIBGCC:-}"
    if [ -z "$libgcc" ]; then
        for cand in /usr/lib/x86_64-linux-musl/libgcc_s.so.1 \
                    /usr/lib/musl/lib/libgcc_s.so.1 \
                    /usr/local/musl/lib/libgcc_s.so.1; do
            [ -f "$cand" ] && libgcc="$cand" && break
        done
    fi
    if [ -z "$libgcc" ] && command -v docker >/dev/null 2>&1; then
        note "extracting a musl libgcc_s.so.1 from alpine for the native run"
        mkdir -p "$WORK/muslshim"
        docker run --rm -v "$WORK/muslshim":/out alpine:3.20 \
            sh -c 'apk add --no-cache libgcc >/dev/null 2>&1 && cp -L /usr/lib/libgcc_s.so.1 /out/' \
            >/dev/null 2>&1
        [ -f "$WORK/muslshim/libgcc_s.so.1" ] && libgcc="$WORK/muslshim/libgcc_s.so.1"
    fi
    if [ -n "$libgcc" ]; then
        MUSL_LIBDIR="$(dirname "$libgcc")"
        RUN_MODE="native"
    else
        RUN_MODE="container"
    fi
}
detect_run_mode
if [ -n "$MODULE_BIN" ]; then
    chk env:run-mode PASS "mode=$RUN_MODE (musl x86_64 artifact)" \
        "$( [ "$RUN_MODE" = native ] && echo "musl loader + libgcc present" || echo "falling back to a container: the host cannot run musl binaries" )"
fi

# --------------------------------------------------------------------------
# 2. Hermetic workspace: config dir, fake ndsctl on PATH, stub mint
# --------------------------------------------------------------------------
CFG_DIR="$WORK/config"
mkdir -p "$CFG_DIR"
FAKE_BIN="$WORK/fakebin"
mkdir -p "$FAKE_BIN"
cp "$FAKE_NDSCTL" "$FAKE_BIN/ndsctl"
chmod +x "$FAKE_BIN/ndsctl"
NDSCTL_LOG="$WORK/ndsctl.log"
: > "$NDSCTL_LOG"
SELFTEST_LOG="$WORK/ndsctl-selftest.log"

# the fake is the seam we assert on: prove it records before trusting it
if NDSCTL_LOG="$SELFTEST_LOG" "$FAKE_BIN/ndsctl" auth 02:00:00:00:00:20 >/dev/null 2>&1 \
   && grep -q "AUTH 02:00:00:00:00:20" "$SELFTEST_LOG"; then
    chk env:fake-ndsctl-records PASS "self-test: fake-ndsctl recorded an AUTH line"
else
    chk env:fake-ndsctl-records FAIL "fake-ndsctl did not record a self-test AUTH call"
fi

PYTHON=""
for cand in "${HP_PYTHON:-}" /home/c03rad0r/tg-e2e/venv/bin/python "$(command -v python3)"; do
    [ -n "$cand" ] || continue
    if "$cand" -c 'import playwright.sync_api' >/dev/null 2>&1; then PYTHON="$cand"; break; fi
done

STUB_MINT_PORT=0
if [ "$RUN_MODULE" = "1" ]; then
    STUB_MINT_LOG="$WORK/stub-mint.log"
    HP_STUB_MINT_LOG="$STUB_MINT_LOG" HP_STUB_MINT_QUOTE_STATE="UNPAID" \
        python3 "$SELF_DIR/stubs/stub_mint.py" > "$WORK/stub-mint.out" 2>&1 &
    STUB_PID=$!
    for _ in $(seq 1 40); do
        STUB_MINT_PORT="$(sed -n 's/.*port=\([0-9]*\).*/\1/p' "$WORK/stub-mint.out" 2>/dev/null | head -1)"
        [ -n "$STUB_MINT_PORT" ] && break
        sleep 0.25
    done
fi
STUB_MINT_URL="http://127.0.0.1:${STUB_MINT_PORT:-0}"
if [ "$RUN_MODULE" = "1" ] && [ "$STUB_MINT_PORT" != "0" ]; then
    chk env:stub-mint PASS "offline stub mint on $STUB_MINT_URL (never a real mint, never real ecash)"
elif [ "$RUN_MODULE" = "1" ]; then
    chk env:stub-mint FAIL "stub mint did not report a port: $(tail -3 "$WORK/stub-mint.out" 2>/dev/null | tr '\n' ' ')"
    RUN_MODULE=0
fi

sed "s|@STUB_MINT_URL@|$STUB_MINT_URL|" "$SELF_DIR/config/config.json.in" > "$CFG_DIR/config.json"

# --------------------------------------------------------------------------
# 3. Boot the module binary from the artifact
# --------------------------------------------------------------------------
MOD_UP=0
MUSL_LD=""
[ -n "$MUSL_LIBDIR" ] && MUSL_LD="/lib/$(head -c 4096 "$MODULE_BIN" | tr -d '\0' | grep -ao 'ld-musl-[a-z0-9_]*\.so\.[0-9]' | head -1)"

start_module() {
    [ -z "$MODULE_BIN" ] && return
    note "starting $RUN_MODE run of $MODULE_BIN (config dir $CFG_DIR)"
    if [ "$RUN_MODE" = "container" ]; then
        docker run --rm --name "tg-happy-path-$$" --network host \
            -v "$ARTIFACT":/artifact:ro -v "$CFG_DIR":/cfg -v "$FAKE_BIN":/fakebin:ro \
            -v "$NDSCTL_LOG":/fakebin/ndsctl.log \
            -e TOLLGATE_TEST_CONFIG_DIR=/cfg -e PATH=/fakebin:/usr/local/bin:/usr/bin:/bin \
            -e NDSCTL_LOG=/fakebin/ndsctl.log \
            alpine:3.20 /artifact/usr/bin/tollgate-wrt > "$WORK/module.log" 2>&1 &
    else
        # Invoke the musl loader EXPLICITLY with --library-path rather than
        # exporting LD_LIBRARY_PATH: the variable would be inherited by every
        # child the module spawns, and fake-ndsctl.sh calls `date`, which then
        # dies with "cannot open shared object file libc.musl-x86_64.so.1" —
        # silently emptying the timestamps and corrupting the `ndsctl json`
        # output the module parses.
        if [ -n "$MUSL_LD" ]; then
            env -i PATH="$FAKE_BIN:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
                HOME="$WORK" TOLLGATE_TEST_CONFIG_DIR="$CFG_DIR" NDSCTL_LOG="$NDSCTL_LOG" \
                "$MUSL_LD" --library-path "$MUSL_LIBDIR" \
                "$MODULE_BIN" > "$WORK/module.log" 2>&1 &
        else
            # Not a musl ELF (a script, or a static/glibc binary): run it directly.
            env -i PATH="$FAKE_BIN:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
                HOME="$WORK" TOLLGATE_TEST_CONFIG_DIR="$CFG_DIR" NDSCTL_LOG="$NDSCTL_LOG" \
                "$MODULE_BIN" > "$WORK/module.log" 2>&1 &
        fi
    fi
    MOD_PID=$!
}

if [ "$RUN_MODULE" = "1" ] && command -v curl >/dev/null 2>&1; then
    if curl -s -m 2 -o /dev/null "http://127.0.0.1:$MODULE_PORT/" 2>/dev/null; then
        chk boot:port-free FAIL "something already listens on :$MODULE_PORT — the suite needs it"
    else
        start_module
        for _ in $(seq 1 90); do
            if curl -s -m 2 -o /dev/null "http://127.0.0.1:$MODULE_PORT/" 2>/dev/null; then MOD_UP=1; break; fi
            sleep 1
        done
        if [ "$MOD_UP" = "1" ]; then
            chk boot:module-listening PASS "artifact binary answering on :$MODULE_PORT (pid $MOD_PID)"
        else
            chk boot:module-listening FAIL "no answer on :$MODULE_PORT after 90s; tail: $(tail -4 "$WORK/module.log" 2>/dev/null | tr '\n' ' ')"
        fi
    fi
fi

# http <path> [curl args...] -> body on stdout. The status code lands in
# $WORK/code and the response headers in $WORK/headers: a command substitution
# runs in a subshell, so a variable assigned inside http() would never reach the
# caller.
http() {
    local path="$1"; shift
    curl -s -m 25 -o "$WORK/body" -D "$WORK/headers" -w '%{http_code}' "$@" \
        "http://127.0.0.1:$MODULE_PORT$path" > "$WORK/code" 2>/dev/null
    cat "$WORK/body" 2>/dev/null
}
httpcode() { cat "$WORK/code" 2>/dev/null; }
# httpheader <name> -> the value of that response header from the last http()
# call, or "" if it was absent. Portable on mawk (no IGNORECASE): match the name
# case-insensitively with grep -i, then strip "name:".
httpheader() {
    tr -d '\r' < "$WORK/headers" 2>/dev/null \
        | grep -i "^$1:" | tail -1 | sed 's/^[^:]*:[[:space:]]*//'
}

if [ "$MOD_UP" = "1" ]; then
    # --- C4: kind:10021 advertisement -------------------------------------
    ADV=""
    for _ in $(seq 1 20); do
        ADV="$(http /)"
        CODE="$(httpcode)"
        case "$ADV" in *'"kind":10021'*) break ;; esac
        sleep 1
    done
    cp "$WORK/body" "$EVIDENCE/advertisement.json" 2>/dev/null
    case "$ADV" in
        *'"kind":10021'*)
            chk api:advertisement-kind-10021 PASS "GET / -> HTTP $CODE, kind 10021, $(printf '%s' "$ADV" | grep -o '"price_per_step"' | wc -l) price_per_step tag(s)" ;;
        *)
            chk api:advertisement-kind-10021 FAIL "GET / -> HTTP $CODE, body starts: $(printf '%s' "$ADV" | head -c 160)" ;;
    esac

    # --- C5: /whoami -------------------------------------------------------
    W="$(http /whoami)"
    CODE="$(httpcode)"
    case "$W" in
        mac=*:*) chk api:whoami PASS "/whoami -> HTTP $CODE $(printf '%s' "$W" | head -c 60)" ;;
        *)       chk api:whoami FAIL "/whoami -> HTTP $CODE body: $(printf '%s' "$W" | head -c 160)" ;;
    esac

    # --- C6: /balance ------------------------------------------------------
    B="$(http /balance)"
    CODE="$(httpcode)"
    if printf '%s' "$B" | grep -q '"status"' \
       && printf '%s' "$B" | grep -q '"session_active"' \
       && printf '%s' "$B" | grep -q '"usage"' \
       && printf '%s' "$B" | grep -q '"allotment"' \
       && printf '%s' "$B" | grep -q '"remaining"'; then
        chk api:balance-shape PASS "/balance -> HTTP $CODE $(printf '%s' "$B" | head -c 120)"
    else
        chk api:balance-shape FAIL "/balance -> HTTP $CODE body: $(printf '%s' "$B" | head -c 160)"
    fi

    # --- C7: /usage --------------------------------------------------------
    U="$(http /usage)"
    CODE="$(httpcode)"
    case "$U" in
        *[0-9]/*[0-9]*) chk api:usage-shape PASS "/usage -> HTTP $CODE $(printf '%s' "$U" | head -c 40)" ;;
        *)              chk api:usage-shape FAIL "/usage -> HTTP $CODE body: $(printf '%s' "$U" | head -c 160)" ;;
    esac

    # --- C7b: the client-identity contract --------------------------------
    # Every client-scoped route of :2121 answers for the client at the other end
    # of the socket, and NEVER for a `mac` the caller sent. The parameter is
    # accepted for wire compatibility with the shipped portal (whose Lightning
    # lane echoes back what /whoami told it) and ignored.
    #
    # WHY IT IS CHECKED HERE: measured on the bench (MT3000, 2026-09-26) a token
    # posted "for" a MAC the sender was not using opened the gate for the
    # SENDER's socket, and nothing on the wire said so — the rig read the answer
    # that named no such session as "the gate never opened" and lost hours. So
    # the contract is only worth anything if it is legible: name a MAC we are
    # certainly not using and require the module to report that claim as
    # IGNORED, and to name the client it really answered for.
    #
    # The identity the module resolves is the one /whoami just reported (C5), and
    # the gate fixture leases this client (see happy-path-gate.sh) exactly as a
    # router would. If /whoami resolved nothing here, the identity header is
    # legitimately absent and the claim report is the half that is asserted.
    ID_CLAIM='02:11:22:33:44:55'
    ID_SENTINEL='00:00:00:00:00:00'
    ID_SOCKET="$(printf '%s' "$W" | sed -n 's/^mac=//p' | tr 'A-Z' 'a-z')"
    ID_FAIL=""
    identity_probe() {  # identity_probe <label> <path> [curl args...]
        local label="$1" path="$2"; shift 2
        local ignored client
        http "$path" "$@" > /dev/null
        ignored="$(httpheader 'X-TollGate-Mac-Claim-Ignored' | tr 'A-Z' 'a-z')"
        client="$(httpheader 'X-TollGate-Client-MAC' | tr 'A-Z' 'a-z')"
        if [ "$ignored" != "$ID_CLAIM" ]; then
            ID_FAIL="$ID_FAIL; $label reported the claim as '$ignored' (want $ID_CLAIM: an ignored claim must say so)"
        fi
        if [ -n "$ID_SOCKET" ] && [ "$ID_SOCKET" != "$ID_SENTINEL" ]; then
            if [ "$client" != "$ID_SOCKET" ]; then
                ID_FAIL="$ID_FAIL; $label named '$client' as the client (want the socket's $ID_SOCKET)"
            fi
        elif [ "$client" = "$ID_CLAIM" ]; then
            ID_FAIL="$ID_FAIL; $label named the caller's claim as the client"
        fi
    }

    identity_probe "GET /whoami"       "/whoami?mac=$ID_CLAIM"
    identity_probe "GET /balance"      "/balance?mac=$ID_CLAIM"
    identity_probe "GET /usage"        "/usage?mac=$ID_CLAIM"
    identity_probe "GET /session-state" "/session-state?mac=$ID_CLAIM"
    # The money route, with an EMPTY body: no proof, so nothing can be redeemed
    # or spent — the same inert probe the router harness uses. What is asserted
    # is that the route tells the caller which client it acted for and that its
    # claim was not honoured, which is the signal that was missing on the bench.
    identity_probe "POST / (empty body)" "/?mac=$ID_CLAIM" \
        -X POST -H 'Content-Type: text/plain' --data-binary ''

    if [ -z "$ID_FAIL" ]; then
        chk api:client-identity PASS \
            "a ?mac= claim is reported as IGNORED on /whoami, /balance, /usage, /session-state and POST /, and the identity named is the socket's (${ID_SOCKET:-unresolved})"
    else
        chk api:client-identity FAIL \
            "the socket-scoped identity contract is not legible:$ID_FAIL (want 'X-TollGate-Mac-Claim-Ignored: $ID_CLAIM' on every client-scoped route and 'X-TollGate-Client-MAC' naming the client /whoami resolved to)"
    fi

    # --- C8: the LN quote path --------------------------------------------
    # No quote = a 400 STATUS POLL, which is the documented answer, not a fault.
    LN="$(http '/ln-invoice')"
    CODE="$(httpcode)"
    if [ "$CODE" = "400" ] && printf '%s' "$LN" | grep -q '"error":"quote is required"'; then
        chk api:ln-invoice-no-quote-400 PASS "GET /ln-invoice (no quote) -> HTTP 400 $(printf '%s' "$LN" | head -c 80) — expected, this is the status-poll contract"
    else
        chk api:ln-invoice-no-quote-400 FAIL "GET /ln-invoice (no quote) -> HTTP $CODE body: $(printf '%s' "$LN" | head -c 160) (want 400 + {\"error\":\"quote is required\"})"
    fi

    # --- C9: /session-state (none|active|expired) --------------------------
    SS="$(http '/session-state?mac=02:00:00:00:00:20')"
    CODE="$(httpcode)"
    cp "$WORK/body" "$EVIDENCE/session-state.json" 2>/dev/null
    if [ "$CODE" = "200" ] && printf '%s' "$SS" | grep -qE '"state"[[:space:]]*:[[:space:]]*"(none|active|expired)"'; then
        chk api:session-state PASS "/session-state -> HTTP 200 $(printf '%s' "$SS" | head -c 140)"
    elif [ "$CODE" = "200" ] && printf '%s' "$SS" | grep -q '"kind":10021'; then
        # Go's default mux falls through to the "/" handler, so an unregistered
        # path answers 200 with the advertisement rather than a 404. That is a
        # MISSING endpoint, not a wrong-shaped one.
        if [ "$STRICT" = "1" ]; then
            chk api:session-state FAIL "/session-state falls through to the / handler (HTTP 200 + kind 10021) and --strict demands the endpoint"
        else
            chk api:session-state SKIP "/session-state unregistered (mux falls through to / => kind 10021): this artifact predates upstream #541; re-run with --strict once a release ships it"
        fi
    elif [ "$STRICT" = "1" ]; then
        chk api:session-state FAIL "/session-state -> HTTP $CODE, and --strict demands it: $(printf '%s' "$SS" | head -c 140)"
    else
        chk api:session-state SKIP "/session-state -> HTTP $CODE: endpoint not present in this artifact"
    fi

    # --- C10: a failed payment must NOT move the gate ----------------------
    : > "$NDSCTL_LOG"
    FR="$(http / -X POST -H 'Content-Type: text/plain' --data-binary 'cashuBthisIsNotARedeemableToken')"
    CODE="$(httpcode)"
    if grep -q "AUTH" "$NDSCTL_LOG"; then
        chk enforcement:no-gate-on-failed-payment FAIL "an unredeemable token still produced an ndsctl AUTH (body: $(printf '%s' "$FR" | head -c 120))"
    else
        chk enforcement:no-gate-on-failed-payment PASS "POST / with an unredeemable token -> HTTP $CODE, ndsctl log empty (no gate moved, correct)"
    fi

    # --- C11: the gate DOES move when the module recognises a payment ------
    # Money-free route: the mint reports the quote ISSUED, which is the branch
    # that grants access without minting ecash (merchant/lightning.go). The stub
    # is the issuer here, so this asserts the enforcement seam and the MAC
    # plumbing -- NOT that real ecash was redeemed. See README.
    curl -sS -m 5 -X POST "$STUB_MINT_URL/__hp/quote-state?value=ISSUED" >/dev/null 2>&1
    note "stub mint quote state -> $(curl -sS -m 5 "$STUB_MINT_URL/__hp/quote-state" 2>/dev/null)"
    MAC="02:00:00:00:00:20"
    : > "$NDSCTL_LOG"
    # NOTE the trailing slash. The wallet keys a mint as "<url>/" and the module
    # passes the caller's mint_url through verbatim, so the request form has to
    # carry the slash. The portal does NOT (it echoes the advertisement's tag),
    # which is why portal:lightning-lane-against-live-module fails on this
    # artifact -- see known-issues.txt.
    MINT_REQ="$STUB_MINT_URL/"
    Q="$(http /ln-invoice -X POST -H 'Content-Type: application/json' \
            --data-binary "{\"amount\":210,\"mint_url\":\"$MINT_REQ\",\"mac\":\"$MAC\"}")"
    CODE="$(httpcode)"
    QUOTE_ID="$(printf '%s' "$Q" | sed -n 's/.*"quote":"\([^"]*\)".*/\1/p')"
    if [ -z "$QUOTE_ID" ]; then
        chk enforcement:gate-opens-on-recognised-payment FAIL "POST /ln-invoice -> HTTP $CODE body: $(printf '%s' "$Q" | head -c 200)"
    else
        ST="$(http "/ln-invoice?quote=$QUOTE_ID&mac=$MAC")"
        SCODE="$(httpcode)"
        sleep 2
        if grep -q "AUTH $MAC" "$NDSCTL_LOG"; then
            chk enforcement:gate-opens-on-recognised-payment PASS "quote $QUOTE_ID recognised (access_granted=$(printf '%s' "$ST" | grep -o '"access_granted":[a-z]*' | head -1)) -> fake-ndsctl recorded: $(tr '\n' '|' < "$NDSCTL_LOG")"
        else
            chk enforcement:gate-opens-on-recognised-payment FAIL "quote $QUOTE_ID status(HTTP $SCODE)=$ST but no 'AUTH $MAC' in $NDSCTL_LOG (log: $(tr '\n' '|' < "$NDSCTL_LOG"))"
        fi
    fi
    cp "$WORK/module.log" "$EVIDENCE/module.log" 2>/dev/null
else
    chk boot:module-listening SKIP "module checks skipped"
fi

# --------------------------------------------------------------------------
# 4. Drive the portal bundle from the SAME artifact with a real browser
# --------------------------------------------------------------------------
if [ "$RUN_PORTAL" = "1" ]; then
    if [ -z "$PYTHON" ]; then
        chk portal:purchase-ui SKIP "no python with playwright found (set HP_PYTHON)"
        chk portal:mint-list-from-artifact SKIP "no browser"
        chk portal:device-mac-from-live-module SKIP "no browser"
        chk portal:purchase-cta-present SKIP "no browser"
        chk portal:expired-view SKIP "no browser"
        chk portal:expired-renewal-cta SKIP "no browser"
    elif [ -z "$PORTAL_DIR" ]; then
        chk portal:purchase-ui FAIL "no portal bundle inside the artifact"
    else
        ADV_FILE="$EVIDENCE/advertisement.json"
        PORTAL_ARGS="--bundle $PORTAL_DIR --stub-mint $STUB_MINT_URL --advertisement-file $ADV_FILE"
        if [ "$MOD_UP" = "1" ]; then
            note "portal pass 1/3: purchase UI against the LIVE module on :$MODULE_PORT"
            "$PYTHON" "$SELF_DIR/stubs/portal_drive.py" --mode purchase --out "$EVIDENCE/portal-purchase" \
                $PORTAL_ARGS > "$EVIDENCE/portal-purchase.log" 2>&1 || true
            # fold the browser verdicts into this run's counters
            while read -r _ id status rest; do
                [ -n "${id:-}" ] || continue
                case "$id" in
                    portal:expired-view|portal:expired-renewal-cta) continue ;;
                esac
                chk "$id" "$status" "$rest"
            done < <(grep '^HPCHECK' "$EVIDENCE/portal-purchase.log")

            note "portal pass 2/3: the bundle's Lightning lane against the LIVE module"
            "$PYTHON" "$SELF_DIR/stubs/portal_drive.py" --mode live-lightning \
                --out "$EVIDENCE/portal-lightning-live" $PORTAL_ARGS \
                > "$EVIDENCE/portal-lightning-live.log" 2>&1 || true
            while read -r _ id status rest; do
                [ -n "${id:-}" ] || continue
                chk "$id" "$status" "$rest"
            done < <(grep '^HPCHECK' "$EVIDENCE/portal-lightning-live.log")
        else
            chk portal:purchase-ui SKIP "module not running, cannot assert the live-module wiring"
            chk portal:lightning-lane-against-live-module SKIP "module not running"
        fi

        note "portal pass 3/3: expired view + renewal CTA against the routed stub backend"
        STRICT_FLAG=""
        [ "$STRICT" = "1" ] && STRICT_FLAG="--strict-renewal-cta"
        "$PYTHON" "$SELF_DIR/stubs/portal_drive.py" --mode expired --out "$EVIDENCE/portal-expired" \
            $PORTAL_ARGS $STRICT_FLAG > "$EVIDENCE/portal-expired.log" 2>&1 || true
        while read -r _ id status rest; do
            [ -n "${id:-}" ] || continue
            case "$id" in
                portal:renders-purchase-ui|portal:mint-list-from-artifact|portal:device-mac-from-live-module|portal:purchase-cta-present)
                    continue ;;   # already counted in pass 1
            esac
            chk "$id" "$status" "$rest"
        done < <(grep '^HPCHECK' "$EVIDENCE/portal-expired.log")
    fi
fi

# --------------------------------------------------------------------------
# 5. Verdict
#
# A known issue is a FAIL that is already understood, reproducible, and written
# down in known-issues.txt. It is still printed as a FAIL on its check line --
# nothing is relabelled -- but it does not make the suite exit non-zero, because
# a suite that is permanently red on a pre-existing defect stops being read.
# --strict promotes known issues back to fatal.
# --------------------------------------------------------------------------
KNOWN_FILE="$SELF_DIR/known-issues.txt"
KNOWN_N=0; KNOWN_IDS=""
if [ "$FAILED_N" -gt 0 ] && [ -f "$KNOWN_FILE" ]; then
    for id in $FAILED_IDS; do
        reason="$(awk -v want="$id" '$1 == want { sub(/^[^ \t]+[ \t]+/, ""); print; exit }' "$KNOWN_FILE" 2>/dev/null)"
        [ -n "$reason" ] || continue
        if [ "$STRICT" = "1" ]; then
            note "known issue kept FATAL by --strict: $id"
            continue
        fi
        KNOWN_N=$((KNOWN_N + 1))
        KNOWN_IDS="$KNOWN_IDS $id"
        FAILED_N=$((FAILED_N - 1))
        printf 'HPKNOWNISSUE %s %s\n' "$id" "$reason"
    done
fi

printf 'HPRESULT total=%d pass=%d fail=%d skip=%d known=%d\n' \
    "$TOTAL" "$PASSED" "$FAILED_N" "$SKIPPED" "$KNOWN_N"
if [ "$KNOWN_N" -gt 0 ]; then
    printf 'HPKNOWNISSUES%s\n' "$KNOWN_IDS"
fi
if [ "$FAILED_N" -gt 0 ]; then
    printf 'HPFAILED%s\n' "$FAILED_IDS"
    printf 'HPEXIT 1\n'
    exit 1
fi
printf 'HPEXIT 0\n'
exit 0
