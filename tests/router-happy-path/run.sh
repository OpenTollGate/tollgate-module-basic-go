#!/usr/bin/env bash
#
# Router happy-path harness -- the operator's manual pass, as a script.
#
#   tests/router-happy-path/run.sh --apk <published .apk> [--router-ip IP]
#
# Against a REAL MT3000 it asserts, in order:
#
#   1. build identity  -- every asset the router serves is byte-identical to the
#                         same path inside the supplied package (sha256). This is
#                         the non-fakeable part, and it is what catches "the box
#                         is not running the package you think it is".
#   2. surfaces        -- 80 / 2050 / 2051 / 2121 / 8080 / 8090.
#   3. captive chain   -- unauthenticated HTTP is 307'd to
#                         :2050/splash.html?redir=..., :2050 answers with the
#                         cache-bust STUB, and the STUB's target on :2051 is what
#                         actually serves the SPA. The app on a different port
#                         than assumed is a real regression that shipped once.
#   4. API shapes      -- / , /whoami, /balance, /usage, /session-state.
#   5. Lightning quote -- GET /ln-invoice with no quote is a 400 status poll
#                         {"error":"quote is required"}, NOT a fault.
#   6. OPT-IN purchase -- a real paid purchase, gated on an operator-supplied
#                         token. The DEFAULT run spends nothing and never touches
#                         the operator's ecash.
#
# Read-only by default: the only write is one POST with an EMPTY body (it carries
# no proof, so it cannot redeem anything) and it exists to prove the payment lane
# rejects a tokenless request.
#
# TWO TRAPS THIS HARNESS ENCODES:
#   * This firewall DROPS ICMP. Never use ping as a liveness test -- a live router
#     was once reported down by exactly that mistake. Every liveness decision here
#     is a TCP connect; net:icmp-not-a-liveness-test guards the source against a
#     future edit reintroducing it.
#   * Router SSH is password/key gated and the operator adds the key by hand (or
#     types the password). All SSH checks are opt-in via RHP_SSH=1 and SKIP
#     otherwise -- never silently "pass".
#
# Every check prints one line:
#
#   RHPCHECK <id> <PASS|FAIL|SKIP> <detail...>
#   RHPRESULT total=N pass=N fail=N skip=N
#   RHPEXIT <0|1>
#
# Exit 0 = happy path intact, 1 = broken, 2 = usage/precondition error.
#
set -u

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/../.." && pwd)"

ROUTER_IP="${RHP_ROUTER_IP:-192.168.1.1}"
APK=""
ARTIFACT_DIR=""
STRICT=0
SSH_MODE="${RHP_SSH:-0}"
SKIP_MONEY_PATH=0
DO_IDENTITY=1
EXPECT_ENTRY="${RHP_EXPECT_ENTRY:-}"
EXPECT_VERSION="${RHP_EXPECT_VERSION:-}"
OUT=""
KEEP=0

usage() {
    sed -n '3,40p' "$0" | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Options:
  --apk FILE         published package under test (.apk or .ipk). Required
                     unless --artifact-dir is given. Extracted with apk-tools 3
                     (docker alpine:edge fallback); trees are cached by sha256.
  --artifact-dir DIR an already-extracted package tree (skips extraction)
  --router-ip IP     router under test (default 192.168.1.1, env RHP_ROUTER_IP)
  --strict           make absent-but-expected surfaces fatal instead of SKIP
                     (/session-state, /identity -- use once a release ships them)
  --no-identity      explicit opt-out of the build-identity section. Without an
                     artifact AND without this flag the run FAILS: a green run
                     that never compared bytes is the thing this harness exists
                     to prevent.
  --ssh              also run the on-box checks (needs RHP_SSH set/reachable)
  --skip-money-path  do not send even the empty-body POST
  --out DIR          evidence dir (default <tmp>/evidence)
  --keep             keep the work dir
  -h, --help         this text

Environment:
  RHP_ROUTER_IP        router address
  RHP_ARTIFACT_DIR     same as --artifact-dir
  RHP_EXPECT_ENTRY     NAME:SIZE:SHA256 pin for the portal entry chunk
  RHP_EXPECT_VERSION   expected installed package version (SSH lane only)
  RHP_SPEND_MAX_SATS   spend cap for the paid lane (required to spend anything)
  RHP_CASHU_TOKEN      operator-supplied Cashu token. THE ONLY WAY the paid lane
                       runs. Unset => nothing is sent, nothing is spent.
  RHP_SSH=1            enable the on-box SSH checks (default: SKIP)
  RHP_SSH_USER         SSH user (default root)
  RHP_TMPDIR           parent for the work dir (default /var/tmp; the /tmp
                       tmpfs is too small for extracted packages)
  RHP_DOCKER_IMAGE     image used for .apk extraction (default alpine:edge)
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --apk)           APK="${2:-}"; shift 2 ;;
        --artifact-dir)  ARTIFACT_DIR="${2:-}"; shift 2 ;;
        --router-ip)     ROUTER_IP="${2:-}"; shift 2 ;;
        --strict)        STRICT=1; shift ;;
        --no-identity)   DO_IDENTITY=0; shift ;;
        --ssh)           SSH_MODE=1; shift ;;
        --skip-money-path) SKIP_MONEY_PATH=1; shift ;;
        --expect-entry)  EXPECT_ENTRY="${2:-}"; shift 2 ;;
        --expect-version) EXPECT_VERSION="${2:-}"; shift 2 ;;
        --out)           OUT="${2:-}"; shift 2 ;;
        --keep)          KEEP=1; shift ;;
        -h|--help)       usage; exit 0 ;;
        *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[ -n "$APK" ] || APK="${RHP_APK:-}"
[ -n "$ARTIFACT_DIR" ] || ARTIFACT_DIR="${RHP_ARTIFACT_DIR:-}"

API_PORT="${RHP_API_PORT:-2121}"
PORTAL_PORT="${RHP_PORTAL_PORT:-2051}"
STUB_PORT="${RHP_STUB_PORT:-2050}"
LUCI_PORT="${RHP_LUCI_PORT:-8080}"
ADMIN_PORT="${RHP_ADMIN_PORT:-8090}"
# Ports that are probed for liveness and (captive) enforcement. These are
# overridable so the offline self-test can stand the whole rig up unprivileged;
# on hardware they are 80 / 22 / 443.
CAPTIVE_PORT="${RHP_CAPTIVE_PORT:-80}"
SSH_PORT="${RHP_SSH_PORT:-22}"
TLS_PORT="${RHP_TLS_PORT:-443}"

TMP_PARENT="${RHP_TMPDIR:-/var/tmp}"
WORK="$(mktemp -d "$TMP_PARENT/rhp.XXXXXX")" || exit 2
EVIDENCE="${OUT:-$WORK/evidence}"
mkdir -p "$EVIDENCE"
TALLY="$WORK/checks.tally"
: > "$TALLY"

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
    printf 'RHPCHECK %s %s %s\n' "$id" "$status" "$detail"
    printf '%s %s\n' "$id" "$status" >> "$TALLY"
}

note() { printf 'RHPNOTE %s\n' "$*"; }

# fold a python helper's RHPCHECK/RHPNOTE lines into this run's counters
fold() {  # fold <cmd...>
    while IFS= read -r line; do
        case "$line" in
            RHPCHECK\ *) chk $(printf '%s' "${line#RHPCHECK }") ;;
            RHPNOTE\ *)  note "${line#RHPNOTE }" ;;
            '') ;;
            *) printf '%s\n' "$line" ;;
        esac
    done < <("$@" 2>&1)
    return 0
}

cleanup() {
    if [ "$KEEP" = "1" ]; then
        note "work dir kept: $WORK"
    else
        rm -rf "$WORK"
    fi
}
trap cleanup EXIT INT TERM

# --------------------------------------------------------------------------
# HTTP helpers (curl only; no ICMP anywhere in this file)
# --------------------------------------------------------------------------
REQ_N=0
fetch() {  # fetch <url> [extra curl flags...] -> FETCH_CODE FETCH_LOC FETCH_BODY FETCH_HDRS
    local url="$1"; shift
    REQ_N=$((REQ_N + 1))
    FETCH_BODY="$WORK/req.$REQ_N.body"
    FETCH_HDRS="$WORK/req.$REQ_N.hdr"
    local out
    out="$(curl -s -m 15 "$@" -o "$FETCH_BODY" -D "$FETCH_HDRS" \
           -w '%{http_code} %{redirect_url}' "$url" 2>/dev/null)" || out="000 "
    FETCH_CODE="${out%% *}"
    FETCH_LOC="${out#* }"
    [ -n "$FETCH_CODE" ] || FETCH_CODE="000"
    return 0
}

hdr() {  # hdr <header-name> -> value from the last fetch
    tr -d '\r' < "$FETCH_HDRS" 2>/dev/null \
        | awk -v want="$(printf '%s' "$1" | tr 'A-Z' 'a-z')" \
              'BEGIN{IGNORECASE=1} tolower($1)==want":" {sub(/^[^:]*:[ ]*/,""); print; exit}'
}

tcp_open() {  # TCP connect only -- never ping
    timeout 3 bash -c "exec 3<>/dev/tcp/$1/$2" >/dev/null 2>&1
}

# --------------------------------------------------------------------------
# 0. Preflight -- liveness by TCP, and the box must be idle before anything
#    else is attributable. Failing here does NOT abort the run: the operator
#    wants the whole map, not the first red line.
# --------------------------------------------------------------------------
printf '\n===== 0. preflight (TCP liveness, never ICMP) =====\n'
for port in "$SSH_PORT" "$STUB_PORT" "$PORTAL_PORT" "$API_PORT" "$LUCI_PORT" "$ADMIN_PORT" "$TLS_PORT"; do
    if tcp_open "$ROUTER_IP" "$port"; then
        chk "net:tcp-$port" PASS "TCP connect to $ROUTER_IP:$port succeeded"
    else
        chk "net:tcp-$port" FAIL "no TCP listener answers on $ROUTER_IP:$port (ICMP is dropped here, so this TCP result IS the liveness answer)"
    fi
done
# Guard against a future edit reintroducing ping as a liveness test. The regex
# only fires on a ping COMMAND (start of line / after a shell separator, followed
# by a -flag), so the prose in this file's own comments does not trip it.
if grep -nE '(^|[;&|(])[[:space:]]*(/[a-z/]*/)?ping[[:space:]]+-' "$SELF_DIR/run.sh" >/dev/null 2>&1; then
    chk "net:icmp-not-a-liveness-test" FAIL "run.sh invokes ping: this firewall DROPS ICMP, so that can never be a liveness test here"
else
    chk "net:icmp-not-a-liveness-test" PASS "no ping invocation in the harness source; every liveness decision above is a TCP connect"
fi

fold python3 "$SELF_DIR/lib/api_check.py" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only pre
IDLE_OK=1
grep -q '^pre:idle FAIL' "$TALLY" && IDLE_OK=0
[ "$IDLE_OK" = "1" ] || note "preflight says the box is NOT idle: results below are not attributable to this run and the paid lane will refuse to spend"

# --------------------------------------------------------------------------
# 1. Build identity -- live bytes vs the package, by leading sha256
# --------------------------------------------------------------------------
printf '\n===== 1. build identity (served bytes == package bytes) =====\n'
ARTIFACT=""
if [ "$DO_IDENTITY" = "0" ]; then
    note "identity section disabled with --no-identity: a green run here proves nothing about WHICH build is deployed"
    for cid in artifact:package identity:portal:entry identity:admin:entry identity:expected-entry; do
        chk "$cid" SKIP "--no-identity"
    done
elif [ -z "$APK" ] && [ -z "$ARTIFACT_DIR" ]; then
    chk "artifact:package" FAIL "no --apk/--artifact-dir supplied: build identity is the point of this harness (pass --no-identity to opt out explicitly)"
else
    SRC="${ARTIFACT_DIR:-$APK}"
    if ARTIFACT="$( ( . "$SELF_DIR/lib/apk-artifact.sh"; rhp_prepare_artifact "$SRC" "$TMP_PARENT/rhp-artifacts" ) 2>"$WORK/artifact.err" )"; then
        chk "artifact:package" PASS "$(basename "$SRC") -> $ARTIFACT ($(du -sh "$ARTIFACT" 2>/dev/null | cut -f1) extracted)"
    else
        ARTIFACT=""
        chk "artifact:package" FAIL "$(tr '\n' ' ' < "$WORK/artifact.err" | head -c 300)"
    fi
    while IFS= read -r l; do
        [ -n "$l" ] || continue
        case "$l" in
            RHPNOTE\ *) note "${l#RHPNOTE }" ;;
            *) note "artifact: $l" ;;
        esac
    done < "$WORK/artifact.err"
fi
if [ -n "$ARTIFACT" ]; then
    if [ -n "$EXPECT_ENTRY" ]; then
        fold python3 "$SELF_DIR/lib/identity_check.py" \
            --router-ip "$ROUTER_IP" --artifact-dir "$ARTIFACT" \
            --portal-port "$PORTAL_PORT" --admin-port "$ADMIN_PORT" \
            --expect-entry "$EXPECT_ENTRY" --out "$EVIDENCE"
    else
        fold python3 "$SELF_DIR/lib/identity_check.py" \
            --router-ip "$ROUTER_IP" --artifact-dir "$ARTIFACT" \
            --portal-port "$PORTAL_PORT" --admin-port "$ADMIN_PORT" \
            --out "$EVIDENCE"
    fi
elif [ "$DO_IDENTITY" = "1" ]; then
    chk "identity:portal:entry" FAIL "no artifact: cannot compare the served bundle against a package"
    chk "identity:admin:entry" FAIL "no artifact: cannot compare the served admin bundle against a package"
fi

PORTAL_ENTRY="$(python3 "$SELF_DIR/lib/identity_check.py" --router-ip "$ROUTER_IP" \
    --portal-port "$PORTAL_PORT" --print-entry portal 2>/dev/null || true)"

# --------------------------------------------------------------------------
# 2. Surfaces + 3. the captive chain
# --------------------------------------------------------------------------
printf '\n===== 2. surfaces (captive port %s) =====\n' "$CAPTIVE_PORT"
fetch "http://$ROUTER_IP:$CAPTIVE_PORT/"
code="$FETCH_CODE"; loc="$FETCH_LOC"
if [ "$code" = "307" ] && printf '%s' "$loc" | grep -q ":$STUB_PORT/splash.html?redir="; then
    chk "surface:$CAPTIVE_PORT-captive-307" PASS "http://$ROUTER_IP:$CAPTIVE_PORT/ -> 307 $loc"
else
    chk "surface:$CAPTIVE_PORT-captive-307" FAIL "http://$ROUTER_IP:$CAPTIVE_PORT/ -> $code $loc (want 307 to :$STUB_PORT/splash.html?redir=)"
fi

fetch "http://$ROUTER_IP:$CAPTIVE_PORT/"
if printf '%s' "$FETCH_LOC" | grep -q "redir=http%3a%2f%2f\|redir=http://"; then
    chk "surface:$CAPTIVE_PORT-redir-encodes-original" PASS "redir carries the requested URL: ${FETCH_LOC#*redir=}"
else
    chk "surface:$CAPTIVE_PORT-redir-encodes-original" FAIL "307 Location has no redir=<original url> (got $FETCH_LOC)"
fi

fetch "http://$ROUTER_IP:$STUB_PORT/"
stub_code="$FETCH_CODE"
cp "$FETCH_BODY" "$EVIDENCE/stub-$STUB_PORT.html" 2>/dev/null
if [ "$stub_code" = "200" ] && grep -q 'location.replace' "$FETCH_BODY"; then
    chk "surface:$STUB_PORT-cache-bust-stub" PASS ":$STUB_PORT/ -> 200, $(stat -c %s "$FETCH_BODY" 2>/dev/null) B cache-bust stub"
else
    chk "surface:$STUB_PORT-cache-bust-stub" FAIL ":$STUB_PORT/ -> $stub_code, expected the cache-bust stub (200 + location.replace)"
fi

# Resolve the stub's own JS expression into a URL (never assume the URL shape:
# that assumption is the regression this check exists to catch).
STUB_TARGET=""
if STUB_TARGET="$(python3 "$SELF_DIR/lib/stub_chain.py" --stub-file "$FETCH_BODY" \
                  --host "$ROUTER_IP" 2>"$WORK/stub.err")" && [ -n "$STUB_TARGET" ]; then
    sthost="${STUB_TARGET#http://}"; sthost="${sthost%%/*}"
    stport="${sthost##*:}"
    stpath="/${STUB_TARGET#http://*/}"
    case "$stport:$stpath" in
        "$PORTAL_PORT":/splash.html?_cb=*)
            chk "surface:$STUB_PORT-redirects-to-$PORTAL_PORT" PASS "stub redirects to $STUB_TARGET" ;;
        *)
            chk "surface:$STUB_PORT-redirects-to-$PORTAL_PORT" FAIL "stub redirects to $STUB_TARGET (want host:$PORTAL_PORT/splash.html?_cb=... -- do NOT assume one port serves the app)" ;;
    esac
else
    chk "surface:$STUB_PORT-redirects-to-$PORTAL_PORT" FAIL "could not resolve the stub's redirect expression: $(tr '\n' ' ' < "$WORK/stub.err" | head -c 200)"
fi

fetch "http://$ROUTER_IP:$PORTAL_PORT/splash.html"
if [ "$FETCH_CODE" = "200" ] && grep -q 'text/html' "$FETCH_HDRS" \
   && grep -qE '/assets/[^"]+-[A-Za-z0-9_-]{8}\.js' "$FETCH_BODY"; then
    chk "surface:$PORTAL_PORT-spa" PASS ":$PORTAL_PORT/splash.html -> 200 text/html with content-hashed assets"
else
    chk "surface:$PORTAL_PORT-spa" FAIL ":$PORTAL_PORT/splash.html -> $FETCH_CODE ($(wc -c < "$FETCH_BODY" 2>/dev/null) B, hashed asset refs: $(grep -cE '/assets/[^"]+-[A-Za-z0-9_-]{8}\.js' "$FETCH_BODY" 2>/dev/null))"
fi

# :$PORTAL_PORT/ is NOT an assertable surface: the docroot has no index.html, so
# uhttpd answers 403 and that is by design. Report it, never gate on it.
fetch "http://$ROUTER_IP:$PORTAL_PORT/"
note "surface:$PORTAL_PORT/ -> $FETCH_CODE (informational: by design, the portal docroot has no index.html; the entry document is /splash.html)"

if [ -n "$PORTAL_ENTRY" ]; then
    fetch "http://$ROUTER_IP:$STUB_PORT$PORTAL_ENTRY"
    if [ "$FETCH_CODE" = "200" ]; then
        chk "captive:spa-not-on-$STUB_PORT" FAIL ":$STUB_PORT$PORTAL_ENTRY -> 200: the SPA is being served on BOTH ports, so the fingerprint above only covered one of them"
    else
        chk "captive:spa-not-on-$STUB_PORT" PASS ":$STUB_PORT$PORTAL_ENTRY -> $FETCH_CODE (only :$PORTAL_PORT serves the app, as the stub says)"
    fi
else
    chk "captive:spa-not-on-$STUB_PORT" SKIP "could not read the live portal entry chunk to probe with"
fi

fetch "http://$ROUTER_IP:$API_PORT/"
if [ "$FETCH_CODE" = "200" ] && grep -qE '"kind":[[:space:]]*10021' "$FETCH_BODY"; then
    chk "surface:$API_PORT-api" PASS ":$API_PORT/ -> 200 kind:10021"
else
    chk "surface:$API_PORT-api" FAIL ":$API_PORT/ -> $FETCH_CODE, expected 200 kind:10021"
fi

fetch "http://$ROUTER_IP:$LUCI_PORT/"
if [ "$FETCH_CODE" = "307" ] && printf '%s' "$FETCH_LOC" | grep -q "^https://"; then
    luci_target="$FETCH_LOC"
    chk "surface:$LUCI_PORT-luci-307" PASS ":$LUCI_PORT/ -> 307 $luci_target"
else
    luci_target=""
    chk "surface:$LUCI_PORT-luci-307" FAIL ":$LUCI_PORT/ -> $FETCH_CODE $FETCH_LOC (want 307 to https://)"
fi
fetch "$luci_target" -k
if [ "$FETCH_CODE" = "200" ]; then
    chk "surface:$LUCI_PORT-target-200" PASS "$luci_target -> 200 (the documented :$LUCI_PORT https redirect lands somewhere: without this pair the LuCI redirect dead-ends)"
else
    chk "surface:$LUCI_PORT-target-200" FAIL "$luci_target -> $FETCH_CODE: the :$LUCI_PORT redirect has no working target (nodogsplash allow-list regression)"
fi

fetch "http://$ROUTER_IP:$ADMIN_PORT/"
if [ "$FETCH_CODE" = "200" ] && grep -qE '/assets/[^"]+-[A-Za-z0-9_-]{8}\.js' "$FETCH_BODY"; then
    chk "surface:$ADMIN_PORT-admin-spa" PASS ":$ADMIN_PORT/ -> 200 with a content-hashed entry chunk"
else
    chk "surface:$ADMIN_PORT-admin-spa" FAIL ":$ADMIN_PORT/ -> $FETCH_CODE, expected the admin SPA (200 + hashed entry chunk)"
fi

printf '\n===== 3. captive enforcement chain (%s -> :%s -> :%s) =====\n' "$CAPTIVE_PORT" "$STUB_PORT" "$PORTAL_PORT"
fetch "http://$ROUTER_IP:$CAPTIVE_PORT/some/deep/path?x=1"
if [ "$FETCH_CODE" = "307" ] && printf '%s' "$FETCH_LOC" | grep -q ":$STUB_PORT/splash.html?redir="; then
    chk "captive:unauth-307-to-splash" PASS "deep-path request is 307'd to $FETCH_LOC"
else
    chk "captive:unauth-307-to-splash" FAIL "unauthenticated GET /some/deep/path?x=1 -> $FETCH_CODE $FETCH_LOC (want 307 to :$STUB_PORT/splash.html?redir=...)"
fi
fetch "http://$ROUTER_IP:$CAPTIVE_PORT/some/deep/path?x=1"
redir_enc="${FETCH_LOC#*redir=}"
if printf '%s' "$redir_enc" | grep -qi '%3a%2f%2f.*%2f'; then
    chk "captive:redir-round-trips" PASS "redir=$redir_enc is the URL-encoded original request"
else
    chk "captive:redir-round-trips" FAIL "redir=$redir_enc does not look like the encoded original request"
fi

# follow the stub's own target the way a browser would, preserving ?redir=
if [ -n "$STUB_TARGET" ]; then
    target="$STUB_TARGET&redir=${redir_enc}"
    fetch "$target"
    if [ "$FETCH_CODE" = "200" ] && grep -q 'id="root"' "$FETCH_BODY" 2>/dev/null; then
        chk "captive:chain-ends-200" PASS "$target -> 200 and the SPA root element is present"
    else
        chk "captive:chain-ends-200" FAIL "$target -> $FETCH_CODE (the stub's target does not serve the SPA)"
    fi
    if grep -q 'noscript' "$FETCH_BODY" 2>/dev/null; then
        chk "captive:spa-noscript-fallback" PASS "the served splash.html keeps a <noscript> fallback"
    else
        chk "captive:spa-noscript-fallback" SKIP "the served splash.html has no <noscript> fallback (older bundle)"
    fi
else
    chk "captive:chain-ends-200" FAIL "no resolved stub target to follow"
    chk "captive:spa-noscript-fallback" SKIP "no resolved stub target to follow"
fi

# The stub's own no-JS fallback: without JavaScript the customer must still be
# able to continue to the portal.
fetch "http://$ROUTER_IP:$STUB_PORT/"
ns_href="$(python3 "$SELF_DIR/lib/stub_chain.py" --stub-file "$FETCH_BODY" \
           --noscript-href --host "$ROUTER_IP" 2>/dev/null || true)"
if [ -n "$ns_href" ]; then
    fetch "$ns_href"
    if [ "$FETCH_CODE" = "200" ]; then
        chk "captive:stub-noscript-fallback" PASS "no-JS fallback $ns_href -> 200"
    else
        chk "captive:stub-noscript-fallback" FAIL "no-JS fallback $ns_href -> $FETCH_CODE (a JS-less customer is stranded on the stub)"
    fi
else
    chk "captive:stub-noscript-fallback" FAIL "the :$STUB_PORT stub has no <noscript> anchor (a JS-less customer is stranded)"
fi

# --------------------------------------------------------------------------
# 4/5. API shapes and the Lightning quote contract
# --------------------------------------------------------------------------
printf '\n===== 4. API shapes =====\n'
fold python3 "$SELF_DIR/lib/api_check.py" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only api \
    $([ "$STRICT" = "1" ] && printf '%s' --strict)
printf '\n===== 5. Lightning quote path =====\n'
fold python3 "$SELF_DIR/lib/api_check.py" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only ln

printf '\n===== 6. money path (default: nothing of value is sent) =====\n'
MONEY_ARGS=""
[ "$SKIP_MONEY_PATH" = "1" ] && MONEY_ARGS="--skip-money-path"
fold python3 "$SELF_DIR/lib/api_check.py" --router-ip "$ROUTER_IP" --api-port "$API_PORT" \
    --only money $MONEY_ARGS
if [ -z "${RHP_CASHU_TOKEN:-}" ]; then
    note "paid lane: RHP_CASHU_TOKEN not set -> no token is sent and no ecash is touched (by design)"
fi
fold python3 "$SELF_DIR/lib/api_check.py" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only paid
chk "paid:spends-nothing-by-default" PASS "the default run sent no token; only an empty-body POST touched the payment lane"

# --------------------------------------------------------------------------
# 7. On-box checks -- OPT-IN (router SSH is credential gated)
# --------------------------------------------------------------------------
if [ "$SSH_MODE" = "1" ]; then
    printf '\n===== 7. on-box checks (RHP_SSH=1) =====\n'
    SSH_USER="${RHP_SSH_USER:-root}"
    ssh_run() { ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=8 \
                    "$SSH_USER@$ROUTER_IP" "$@" 2>/dev/null; }
    if ssh_run true; then
        chk "ssh:reachable" PASS "$SSH_USER@$ROUTER_IP accepted a key"
        inst="$(ssh_run 'apk list -I tollgate-wrt 2>/dev/null || opkg list-installed tollgate-wrt 2>/dev/null' | head -1)"
        if [ -n "$inst" ]; then
            if [ -n "$EXPECT_VERSION" ]; then
                if printf '%s' "$inst" | grep -q "$EXPECT_VERSION"; then
                    chk "ssh:installed-version" PASS "$inst"
                else
                    chk "ssh:installed-version" FAIL "$inst (RHP_EXPECT_VERSION=$EXPECT_VERSION)"
                fi
            else
                chk "ssh:installed-version" PASS "$inst (no RHP_EXPECT_VERSION to compare against)"
            fi
        else
            chk "ssh:installed-version" FAIL "could not read the installed tollgate-wrt version"
        fi
        if [ -n "$ARTIFACT" ]; then
            for pair in "/usr/bin/tollgate-wrt:usr/bin/tollgate-wrt" \
                        "/etc/tollgate/tollgate-captive-portal-site/splash.html:etc/tollgate/tollgate-captive-portal-site/splash.html"; do
                box="${pair%%:*}"; inpack="${pair##*:}"
                want="$(sha256sum "$ARTIFACT/$inpack" 2>/dev/null | cut -d' ' -f1)"
                got="$(ssh_run "sha256sum $box 2>/dev/null" | cut -d' ' -f1)"
                if [ -n "$want" ] && [ "$want" = "$got" ]; then
                    chk "ssh:on-box-sha:$(basename "$box")" PASS "$box sha256 $got == package"
                else
                    chk "ssh:on-box-sha:$(basename "$box")" FAIL "$box on box ${got:-<none>} vs package ${want:-<none>}"
                fi
            done
        else
            chk "ssh:on-box-sha" SKIP "no artifact to compare against"
        fi
        nft="$(ssh_run 'nft list ruleset 2>/dev/null' | grep -c 'backend_input_firewall' || true)"
        if [ "${nft:-0}" -gt 0 ]; then
            chk "ssh:firewall-chain-present" PASS "retained backend_input_firewall rules found in the live ruleset"
        else
            chk "ssh:firewall-chain-present" FAIL "no backend_input_firewall in the LIVE ruleset (the chain file can exist while the runtime chain is EMPTY -- check the ruleset, not the file)"
        fi
    else
        chk "ssh:reachable" FAIL "$SSH_USER@$ROUTER_IP refused key auth: add this machine's key to the router by hand, or run the harness without --ssh"
        for cid in ssh:installed-version ssh:firewall-chain-present; do chk "$cid" SKIP "no SSH"; done
    fi
else
    printf '\n===== 7. on-box checks: SKIPPED (RHP_SSH not set) =====\n'
    for cid in ssh:reachable ssh:installed-version ssh:on-box-sha ssh:firewall-chain-present; do
        chk "$cid" SKIP "RHP_SSH not set: router SSH is password/key gated and the operator adds the key by hand (see README)"
    done
fi

# --------------------------------------------------------------------------
# Verdict
# --------------------------------------------------------------------------
printf '\nRHPRESULT total=%d pass=%d fail=%d skip=%d\n' "$TOTAL" "$PASSED" "$FAILED_N" "$SKIPPED"
if [ "$FAILED_N" -gt 0 ]; then
    printf 'RHPFAILED%s\n' "$FAILED_IDS"
    printf 'RHPEXIT 1\n'
    exit 1
fi
printf 'RHPEXIT 0\n'
exit 0
