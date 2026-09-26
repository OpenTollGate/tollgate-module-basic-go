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
# THREE TRAPS THIS HARNESS ENCODES:
#   * This firewall DROPS ICMP. Never use ping as a liveness test -- a live router
#     was once reported down by exactly that mistake. Every liveness decision here
#     is a TCP connect; net:icmp-not-a-liveness-test guards the source against a
#     future edit reintroducing it.
#   * The module RATE-LIMITS its root handler per client IP (10 rpm by default;
#     TOLLGATE_RATE_LIMIT_RPM on the box). GET /, /session-state and the payment
#     POST share that budget, so back-to-back runs collect a 429. A 429 here is a
#     THROTTLE, not a regression: the harness honours Retry-After, retries, and
#     then paces itself, rather than painting ten red lines from one limit.
#   * Router SSH is credential gated, and this harness has NO interactive password
#     path: every on-box command runs with `-o BatchMode=yes`, so the operator must
#     add a key to the router by hand first. All SSH checks are opt-in via
#     RHP_SSH=1 and SKIP otherwise -- never silently "pass".
#
# VANTAGE. There are exactly two places a run can come from, and they cannot
# assert the same set of things:
#   * guest -- a client on br-lan. THIS IS THE DEFAULT A HUMAN TESTER HAS, and it
#     is the vantage the review club runs from. :8090 is firewall-blocked here BY
#     DESIGN (31-admin-board-not-guest-reachable.nft), so the admin-board checks
#     are lifted out of this lane and NAMED: the guest lane asserts that :8090 is
#     genuinely UNREACHABLE (surface:8090-admin-spa-not-guest-reachable, which
#     PASSES on 000 and FAILS if the board answers), a note says in as many words
#     that the admin SPA itself is the mgmt/on-box lane's assertion, and the
#     admin build-identity checks report a named SKIP. Never a silent skip.
#   * mgmt -- the private network or on-box, where :8090 must answer and the
#     admin SPA is asserted in full. That assertion is NOT weakened for the guest
#     lane; the mgmt lane is where the admin board is proven.
# --vantage auto (the default) derives the mode from whether :8090 answers.
#
# THE SECTION-0 TCP BURST IS RETRIED, and a port that answers on a retry -- or
# anywhere later in the same run -- is not a fatal preflight failure. Same class
# of confusion as the documented 429: a race with the box's own convergence must
# not read as a defect. So a port that fails every attempt there is printed as a
# PROVISIONAL note -- not a RHPCHECK line at all, so a reader that greps the id's
# verdict sees exactly one line -- and it is not counted: the verdict has to
# resolve it to exactly one terminal line, a WARNING (warn=N, RHPWARNED, never
# fatal) when some later check in the same run demonstrably reached the port, or
# the FAIL it always was when nothing anywhere in the run reached it. That is
# deliberate: a transcript must never contain a FAIL line for a port the run
# itself goes on to use, or a red line stops meaning a defect. A port whose lane
# is not running at all (the opt-in SSH lane) is a WARNING from the start, and the
# guest lane's firewall-blocked :8090 is its own PASS.
#
# Every check prints one line:
#
#   RHPCHECK <id> <PASS|FAIL|SKIP|WARN> <detail...>
#   RHPPROVISIONAL <id> <detail...>   (section 0 only; a pre-verdict NOTE, not a
#                                      check line and never terminal)
#   RHPRESULT total=N pass=N fail=N skip=N warn=N
#   RHPFAILED <ids...>   RHPWARNED <ids...>
#   RHPEXIT <0|1>
#
# total= counts the RHPCHECK lines only: a PROVISIONAL note is counted where the
# verdict resolves it, so every check contributes exactly one line to the totals,
# and every id has exactly one RHPCHECK line (the note sits outside that
# namespace: see the verdict block).
#
# Exit 0 = happy path intact (warnings are allowed), 1 = broken, 2 = usage error.
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
# Vantage: guest | mgmt | auto. See the header. RHP_VANTAGE / --vantage.
VANTAGE="${RHP_VANTAGE:-auto}"
VANTAGE_RESOLVED=""
EXPECT_ENTRY="${RHP_EXPECT_ENTRY:-}"
EXPECT_VERSION="${RHP_EXPECT_VERSION:-}"
OUT=""
KEEP=0

usage() {
    # print the whole leading comment block as the help text: `set -u` is the
    # last line of the header, so this cannot drift out of sync the way a fixed
    # line range does (it did, silently, whenever the header grew).
    sed -n '3,/^set -u$/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
    cat <<'EOF'

Options:
  --apk FILE         published package under test (.apk or .ipk). Required
                     unless --artifact-dir is given. Extracted with apk-tools 3
                     (docker alpine:edge fallback); trees are cached by sha256.
  --artifact-dir DIR an already-extracted package tree (skips extraction)
  --router-ip IP     router under test (default 192.168.1.1, env RHP_ROUTER_IP)
  --vantage MODE     guest | mgmt | auto (default auto, env RHP_VANTAGE).
                     guest : the br-lan vantage a human tester/club has. :8090 is
                             blocked by design, so the admin lane becomes the
                             explicit not-guest-reachable assertion + named SKIPs.
                     mgmt  : private network or on-box. The admin SPA is asserted
                             in full here (that assertion is never weakened).
                     auto  : derived from whether :8090 answers (guest otherwise).
                     A run from the guest network is the expected default.
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
  RHP_VANTAGE          guest | mgmt | auto (same as --vantage)
  RHP_ARTIFACT_DIR     same as --artifact-dir
  RHP_EXPECT_ENTRY     NAME:SIZE:SHA256 pin for the portal entry chunk
  RHP_EXPECT_VERSION   expected installed package version (SSH lane only)
  RHP_SPEND_MAX_SATS   spend cap for the paid lane (required to spend anything)
  RHP_CASHU_TOKEN      operator-supplied Cashu token. THE ONLY WAY the paid lane
                       runs. Unset => nothing is sent, nothing is spent.
  RHP_SSH=1            enable the on-box SSH checks (default: SKIP)
  RHP_SSH_USER         SSH user (default root)
  RHP_TCP_TRIES        connect attempts per port in the section-0 burst (default 3)
  RHP_TCP_BACKOFF      seconds between those attempts (default 1)
  RHP_TCP_PACE         seconds between ports in the burst (default 0.25; the
                       burst runs on connect, before anything else touches the box)
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
        --vantage)       VANTAGE="${2:-}"; shift 2 ;;
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

case "$VANTAGE" in
    guest|mgmt|auto) ;;
    *) echo "unknown --vantage '$VANTAGE' (want guest|mgmt|auto)" >&2; exit 2 ;;
esac

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
# The transcript is every RHPCHECK/RHPNOTE line exactly as printed. The verdict
# reads it to decide whether a provisional preflight failure was refuted later in
# the same run -- which is why it is a file and not just stdout.
TRANSCRIPT="$WORK/transcript.txt"
: > "$TALLY"
: > "$TRANSCRIPT"

# Which ports this run DEMONSTRABLY reached, and by which check id. The verdict
# resolves a PROVISIONAL preflight failure against THIS record -- never against
# an id whitelist, which credited whatever id merely *named* a port.
#
# Why the record exists (round-1 cross-family review, finding F1): the previous
# revision credited the TLS port from `surface:<luci>-target-200`, an id whose
# port component is the LUCI port while the fetch it names goes wherever the
# 307 Location points -- so a dead :443 could be demoted to a warning by a check
# that never touched it. A port is now credited only by an id that PROVES a
# request completed against that exact port: either a fixed id whose emission
# site is the request itself (the table below), or a dynamic id that records the
# port it actually reached out of the response it got.
REACHED="$WORK/reached.txt"
: > "$REACHED"

reach() {  # reach <port> <id> -- this check completed a live request to :port
    printf '%s\t%s\n' "$1" "$2" >> "$REACHED"
}

# The fixed half of that proof: these ids are emitted only after a request that
# landed on the named port, so a PASS on one of them is reach evidence for it.
# The dynamic half -- ids whose port comes from the response URL (a redirect
# target, the stub's chain) -- records itself with an explicit reach() call.
reach_port_for_id() {  # reach_port_for_id <id> -> port, or empty (no proof)
    case "$1" in
        "surface:$CAPTIVE_PORT-captive-307"|"surface:$CAPTIVE_PORT-redir-encodes-original"|\
        "captive:unauth-307-to-splash"|"captive:redir-round-trips")
            printf '%s\n' "$CAPTIVE_PORT" ;;
        "surface:$STUB_PORT-cache-bust-stub"|"surface:$STUB_PORT-redirects-to-$PORTAL_PORT")
            printf '%s\n' "$STUB_PORT" ;;
        "surface:$PORTAL_PORT-spa")      printf '%s\n' "$PORTAL_PORT" ;;
        "surface:$API_PORT-api")         printf '%s\n' "$API_PORT" ;;
        "surface:$LUCI_PORT-luci-307")   printf '%s\n' "$LUCI_PORT" ;;
        "surface:$ADMIN_PORT-admin-spa") printf '%s\n' "$ADMIN_PORT" ;;
        "ssh:reachable")                 printf '%s\n' "$SSH_PORT" ;;
        *) printf '' ;;
    esac
}

# Section-0 TCP liveness burst knobs. The burst is the FIRST thing that touches
# these ports, right after the previous section's work, so on a box that is still
# converging a single connect can miss a listener that answers seconds later.
TCP_TRIES="${RHP_TCP_TRIES:-3}"
TCP_BACKOFF="${RHP_TCP_BACKOFF:-1}"
TCP_PACE="${RHP_TCP_PACE:-0.25}"
TCP_ATTEMPTS=0
PENDING_TCP_PORTS=""

TOTAL=0; PASSED=0; FAILED_N=0; SKIPPED=0; WARNED=0
FAILED_IDS=""
WARNED_IDS=""

emit() {  # emit <id> <status> <detail...> -- print + record, counters untouched
    local line
    line="$(printf 'RHPCHECK %s %s %s' "$1" "$2" "${3:-}")"
    printf '%s\n' "$line"
    printf '%s %s\n' "$1" "$2" >> "$TALLY"
    printf '%s\n' "$line" >> "$TRANSCRIPT"
}

provisional() {  # provisional <id> <detail...> -- section 0's pre-verdict NOTE
    # Deliberately NOT a RHPCHECK line, and deliberately not counted. The verdict
    # resolves this id to exactly one terminal status (FAIL or WARN) and counts it
    # there, so a preflight result the run itself refutes is never counted as a
    # failure, and a port that is genuinely dead is counted once, not twice.
    # Keeping the note outside the RHPCHECK namespace is what makes "one verdict
    # per id" true for a grep-based reader as well: `grep '^RHPCHECK <id> '` sees
    # the verdict and nothing else, whichever end of the transcript it reads.
    local id="$1"; shift
    printf 'RHPPROVISIONAL %s %s\n' "$id" "$*" >> "$TRANSCRIPT"
    printf 'RHPPROVISIONAL %s %s\n' "$id" "$*"
}

chk() {  # chk <id> <PASS|FAIL|SKIP|WARN> <detail>
    local id="$1" status="$2"; shift 2
    local detail="${*:-}"
    # A PASS on an id that proves a request landed on a port is reach evidence
    # for that port (see reach_port_for_id). Only PASS records: a FAIL/SKIP
    # proves nothing about the port being up, and recording it would let a
    # broken check credit a dead port.
    if [ "$status" = "PASS" ]; then
        local rp; rp="$(reach_port_for_id "$id")"
        [ -n "$rp" ] && reach "$rp" "$id"
    fi
    TOTAL=$((TOTAL + 1))
    case "$status" in
        PASS) PASSED=$((PASSED + 1)) ;;
        SKIP) SKIPPED=$((SKIPPED + 1)) ;;
        WARN) WARNED=$((WARNED + 1)); WARNED_IDS="$WARNED_IDS $id" ;;
        FAIL) FAILED_N=$((FAILED_N + 1)); FAILED_IDS="$FAILED_IDS $id" ;;
    esac
    emit "$id" "$status" "$detail"
}

note() { printf 'RHPNOTE %s\n' "$*"; }

# fold a python helper's RHPCHECK/RHPNOTE lines into this run's counters.
#
# A helper's EXIT STATUS is not optional information. Process substitution hides
# it, so a helper that dies partway (bad interpreter, import error, OOM, a
# traceback into a closed stdout) used to take every check it never reached out
# of the tally WITHOUT a single FAIL -- a green run that never ran the phase, in
# a harness whose whole purpose is to make a green run mean something. So the
# helper writes to a file (which preserves $?) and each phase is reconciled
# afterwards: a helper that exits non-zero, or that exits 0 having emitted nothing
# at all, is a FAIL id of its own.
#
# Self-test seam: RHP_API_HELPER replaces the python helper so a case can prove
# this reconciliation fails loudly when a helper dies (selftest: helper-dies,
# helper-silent).
API_HELPER=(python3 "$SELF_DIR/lib/api_check.py")
[ -n "${RHP_API_HELPER:-}" ] && API_HELPER=("$RHP_API_HELPER")

fold() {  # fold <label> <cmd...>
    local label="$1"; shift
    local out="$WORK/fold.$label.out" rc=0 n=0 line=""
    "$@" > "$out" 2>&1 || rc=$?
    while IFS= read -r line; do
        case "$line" in
            RHPCHECK\ *) chk $(printf '%s' "${line#RHPCHECK }"); n=$((n + 1)) ;;
            RHPNOTE\ *)  note "${line#RHPNOTE }" ;;
            '') ;;
            *) printf '%s\n' "$line" ;;
        esac
    done < "$out"
    if [ "$rc" != "0" ]; then
        chk "helper:$label" FAIL "the $label helper exited $rc: the checks it never reached would otherwise vanish from the tally with no FAIL at all"
    elif [ "$n" = "0" ]; then
        chk "helper:$label" FAIL "the $label helper exited 0 but emitted no check and no note: this phase verified nothing"
    fi
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
    # The module rate-limits its ROOT handler per client IP (10 rpm by default),
    # so a 429 here is a THROTTLE and not the thing under test. Honour the
    # server's Retry-After and retry -- reporting a throttle as a red line is
    # exactly the confusion this harness exists to remove.
    local tries=0 ra
    while [ "$FETCH_CODE" = "429" ] && [ "$tries" -lt 3 ]; do
        tries=$((tries + 1))
        ra="$(hdr Retry-After)"
        case "$ra" in ''|*[!0-9]*) ra=6 ;; esac
        [ "$ra" -gt 20 ] && ra=20
        note "http: HTTP 429 from $url (Retry-After ${ra}s) -- retrying; a throttle is not a regression"
        sleep "$ra"
        out="$(curl -s -m 15 "$@" -o "$FETCH_BODY" -D "$FETCH_HDRS" \
               -w '%{http_code} %{redirect_url}' "$url" 2>/dev/null)" || out="000 "
        FETCH_CODE="${out%% *}"
        FETCH_LOC="${out#* }"
        [ -n "$FETCH_CODE" ] || FETCH_CODE="000"
    done
    return 0
}

hdr() {  # hdr <header-name> -> value from the last fetch
    tr -d '\r' < "$FETCH_HDRS" 2>/dev/null \
        | awk -v want="$(printf '%s' "$1" | tr 'A-Z' 'a-z')" \
              'BEGIN{IGNORECASE=1} tolower($1)==want":" {sub(/^[^:]*:[ ]*/,""); print; exit}'
}

url_port() {  # url_port <url> -> the port this URL actually contacts (80/443 default)
    # An id that carries a port NUMBER is not proof that this port was reached:
    # the response may have sent the request somewhere else (a 307 Location, the
    # stub's chain). The port is taken from the URL that was really fetched.
    local url="$1" scheme rest hostport
    case "$url" in
        http://*)  scheme=http ;;
        https://*) scheme=https ;;
        *) return 1 ;;
    esac
    rest="${url#*://}"; hostport="${rest%%/*}"
    case "$hostport" in
        *:*) printf '%s\n' "${hostport##*:}" ;;
        *)   [ "$scheme" = "https" ] && printf '443\n' || printf '80\n' ;;
    esac
}

tcp_open() {  # TCP connect only -- never ping
    timeout 3 bash -c "exec 3<>/dev/tcp/$1/$2" >/dev/null 2>&1
}

tcp_probe() {  # tcp_probe <host> <port> -> 0 if a listener answers; TCP_ATTEMPTS = which attempt did
    # Retried, because a single connect burst right after the previous section's
    # work races the box's own convergence: measured on the bench MT3000, :22 was
    # reported dead by the first burst during a run that held an SSH session to
    # that very port, and :443 was reported dead and then answered 200 later in
    # the same transcript. A race is not a defect.
    local host="$1" port="$2" i=1
    while [ "$i" -le "$TCP_TRIES" ]; do
        if tcp_open "$host" "$port"; then TCP_ATTEMPTS="$i"; return 0; fi
        [ "$i" -lt "$TCP_TRIES" ] && sleep "$TCP_BACKOFF"
        i=$((i + 1))
    done
    TCP_ATTEMPTS="$TCP_TRIES"
    return 1
}

tcp_nonfatal_reason() {  # why a failed port is reported WITHOUT being fatal
    local port="$1"
    if [ "$port" = "$ADMIN_PORT" ]; then
        printf 'expected from a guest vantage: the #566 guard (packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft) blocks :%s for br-lan clients, so a guest-side probe MUST get nothing here. The admin board itself is asserted by the mgmt/on-box lane, and surface:%s-admin-spa-not-guest-reachable below proves this is the guard and not merely an unmonitored port' "$port" "$port"
    else
        printf 'no phase of THIS run depends on :%s -- the on-box SSH lane is opt-in (add --ssh / RHP_SSH=1 to assert it), so this result is reported but not fatal (it would be a FAIL with --ssh)' "$port"
    fi
}

tcp_fatal_if_dead() {  # 0 = some phase of this run depends on :$1, so a dead port is fatal
    local port="$1"
    if [ "$port" = "$SSH_PORT" ] && [ "$SSH_MODE" != "1" ]; then
        return 1
    fi
    if [ "$port" = "$ADMIN_PORT" ] && [ "$VANTAGE_RESOLVED" = "guest" ]; then
        return 1
    fi
    return 0
}

# --------------------------------------------------------------------------
# 0. Preflight -- liveness by TCP, and the box must be idle before anything
#    else is attributable. Failing here does NOT abort the run: the operator
#    wants the whole map, not the first red line.
# --------------------------------------------------------------------------
printf '\n===== 0. preflight (TCP liveness, never ICMP) =====\n'

# --------------------------------------------------------------------------
# 0a. Vantage. What this run can assert depends on where it runs from, and the
#     guest network -- what a human tester and the review club actually have --
#     cannot see :8090 by design. Say so ONCE, up front, and say which checks
#     that moves: an unstated vantage is how a green run gets read as covering
#     something it never looked at.
# --------------------------------------------------------------------------
if [ "$VANTAGE" = "auto" ]; then
    VAN_WHY=""
    if tcp_probe "$ROUTER_IP" "$ADMIN_PORT"; then
        fetch "http://$ROUTER_IP:$ADMIN_PORT/"
        if [ "$FETCH_CODE" != "000" ]; then
            VANTAGE_RESOLVED=mgmt
            VAN_WHY=":$ADMIN_PORT answered an HTTP request ($FETCH_CODE), so the admin board is visible from here"
        else
            VANTAGE_RESOLVED=guest
            VAN_WHY=":$ADMIN_PORT accepted a TCP connect but answered nothing at the HTTP layer -- that is NOT what the br-lan guard looks like (the guard drops the connect), so this run cannot tell a half-up admin board from a blocked one. The lane below therefore stays guest (flipping it mid-run would end the run green with the admin identity never asserted), and the :$ADMIN_PORT lines below are to be read as ambiguous, not as the guard holding"
        fi
    else
        VANTAGE_RESOLVED=guest
        VAN_WHY="nothing answers :$ADMIN_PORT, which is exactly what a br-lan (guest) client sees when 31-admin-board-not-guest-reachable.nft holds"
    fi
    chk "vantage:mode" PASS "auto-detected '$VANTAGE_RESOLVED': $VAN_WHY. Either lane is supported; --vantage guest|mgmt overrides the derivation"
else
    VANTAGE_RESOLVED="$VANTAGE"
    chk "vantage:mode" PASS "'$VANTAGE_RESOLVED' (given with --vantage/RHP_VANTAGE; auto would have derived it from whether :$ADMIN_PORT answers)"
fi

# --------------------------------------------------------------------------
# 0b. The liveness burst itself.
# --------------------------------------------------------------------------
SWEPT=0
for port in "$SSH_PORT" "$STUB_PORT" "$PORTAL_PORT" "$API_PORT" "$LUCI_PORT" "$ADMIN_PORT" "$TLS_PORT"; do
    if [ "$SWEPT" -gt 0 ] && [ "$TCP_PACE" != "0" ]; then sleep "$TCP_PACE"; fi
    SWEPT=$((SWEPT + 1))
    if tcp_probe "$ROUTER_IP" "$port"; then
        retry_note=""
        if [ "$TCP_ATTEMPTS" -gt 1 ]; then
            retry_note=" (answered on attempt $TCP_ATTEMPTS/$TCP_TRIES: the first connect raced the box, which is why this is retried instead of reported dead)"
        fi
        chk "net:tcp-$port" PASS "TCP connect to $ROUTER_IP:$port succeeded$retry_note"
    elif ! tcp_fatal_if_dead "$port"; then
        chk "net:tcp-$port" WARN "no TCP listener answers on $ROUTER_IP:$port after $TCP_TRIES attempts -- $(tcp_nonfatal_reason "$port")"
    else
        provisional "net:tcp-$port" "no TCP listener answers on $ROUTER_IP:$port after $TCP_TRIES attempts (ICMP is dropped here, so this TCP result IS the liveness answer) -- NOT yet fatal: this line is PROVISIONAL and the verdict at the end of the run resolves it, either to a FAIL (if no check in this run reaches :$port) or to a WARNING (if one does, which means the burst raced the box rather than finding a dead port)"
        PENDING_TCP_PORTS="$PENDING_TCP_PORTS $port"
    fi
done
# Guard against a future edit reintroducing ping as a liveness test. The regex
# only fires on a ping COMMAND (start of line / after a shell separator, followed
# by a -flag), so the prose in these files' own comments and READMEs cannot trip
# it -- and the sweep now covers every source that could run a probe (run.sh,
# lib/, selftest/), not just this file. A guard that only audits the file it
# lives in is not a guard.
if grep -nE '(^|[;&|(])[[:space:]]*(/[a-z/]*/)?ping[[:space:]]+-' \
        "$SELF_DIR/run.sh" "$SELF_DIR"/lib/*.sh "$SELF_DIR"/lib/*.py \
        "$SELF_DIR"/selftest/*.sh "$SELF_DIR"/selftest/*.py >/dev/null 2>&1; then
    chk "net:icmp-not-a-liveness-test" FAIL "a harness source (run.sh, lib/ or selftest/) invokes ping: this firewall DROPS ICMP, so that can never be a liveness test here"
else
    chk "net:icmp-not-a-liveness-test" PASS "no ping invocation anywhere in run.sh, lib/ or selftest/; every liveness decision above is a TCP connect"
fi

fold pre "${API_HELPER[@]}" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only pre
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
        fold identity python3 "$SELF_DIR/lib/identity_check.py" \
            --router-ip "$ROUTER_IP" --artifact-dir "$ARTIFACT" \
            --portal-port "$PORTAL_PORT" --admin-port "$ADMIN_PORT" \
            --vantage "$VANTAGE_RESOLVED" \
            --expect-entry "$EXPECT_ENTRY" --out "$EVIDENCE"
    else
        fold identity python3 "$SELF_DIR/lib/identity_check.py" \
            --router-ip "$ROUTER_IP" --artifact-dir "$ARTIFACT" \
            --portal-port "$PORTAL_PORT" --admin-port "$ADMIN_PORT" \
            --vantage "$VANTAGE_RESOLVED" --out "$EVIDENCE"
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
    # Reach evidence for whichever port the redirect ACTUALLY landed on -- not
    # for $TLS_PORT merely because the id says "target".
    target_port="$(url_port "$luci_target" 2>/dev/null || true)"
    [ -n "$target_port" ] && reach "$target_port" "surface:$LUCI_PORT-target-200"
else
    chk "surface:$LUCI_PORT-target-200" FAIL "$luci_target -> $FETCH_CODE: the :$LUCI_PORT redirect has no working target (nodogsplash allow-list regression)"
fi

# :$ADMIN_PORT -- the admin board. WHICH assertion is correct depends on the
# vantage, and both are assertions (never a silent skip):
#   mgmt : the admin SPA must serve 200 with a content-hashed entry chunk.
#   guest: a br-lan client must get NOTHING (:8090 is blocked by design), so the
#          check PASSES on 000 and FAILS if the board answers at all. What the
#          admin SPA does when it IS reachable is the mgmt/on-box lane's job.
admin_spa_check() {  # the mgmt assertion: the admin SPA itself
    fetch "http://$ROUTER_IP:$ADMIN_PORT/"
    if [ "$FETCH_CODE" = "200" ] && grep -qE '/assets/[^"]+-[A-Za-z0-9_-]{8}\.js' "$FETCH_BODY"; then
        chk "surface:$ADMIN_PORT-admin-spa" PASS ":$ADMIN_PORT/ -> 200 with a content-hashed entry chunk"
    else
        chk "surface:$ADMIN_PORT-admin-spa" FAIL ":$ADMIN_PORT/ -> $FETCH_CODE, expected the admin SPA (200 + hashed entry chunk)"
    fi
}

if [ "$VANTAGE_RESOLVED" = "guest" ]; then
    fetch "http://$ROUTER_IP:$ADMIN_PORT/"
    if [ "$FETCH_CODE" = "000" ]; then
        chk "surface:$ADMIN_PORT-admin-spa-not-guest-reachable" PASS ":$ADMIN_PORT/ -> 000 from this guest vantage: the admin board is correctly unreachable for a br-lan client (31-admin-board-not-guest-reachable.nft). This is an assertion, not a skip -- the admin SPA itself is asserted by the mgmt/on-box lane (--vantage mgmt from the private network, or --ssh on-box). 000 is also what a dead path and a half-up board produce, so read it together with net:tcp-$ADMIN_PORT above: that TCP line is the liveness answer, and vantage:mode names how the lane was resolved. If net:tcp-$ADMIN_PORT says the port answered a connect, this PASS is NOT the guard holding -- check what vantage:mode said before you read it as one"
        note "surface:$ADMIN_PORT-admin-spa was NOT asserted by this run: it is the mgmt/on-box lane's check. Everything the guest vantage can see was still asserted in full"
    else
        # ONE run, ONE lane. An earlier revision re-resolved to mgmt here when
        # --vantage auto had read the port as closed at preflight, which produced
        # a green run whose identity:admin:* checks had already been emitted as
        # guest SKIPs -- the admin build identity was never asserted, and nothing
        # said so beyond a note (round-1 cross-family review, finding F4). The
        # lane is now resolved once, in 0a, and a contradiction is reported as
        # what it is: this vantage CAN see the admin board, so the run must be
        # repeated in the lane that asserts it.
        if [ "$VANTAGE" = "auto" ]; then
            chk "surface:$ADMIN_PORT-admin-spa-not-guest-reachable" FAIL ":$ADMIN_PORT/ -> $FETCH_CODE from a guest vantage, although auto-detection read the port as closed at preflight: the admin board IS answering this vantage, so either 31-admin-board-not-guest-reachable.nft is inert here or the preflight probe raced the box. This run stays in the guest lane (flipping mid-run would end the run green with the admin build identity never asserted), so re-run with an explicit --vantage mgmt to assert the admin surface AND its identity in one lane"
        else
            chk "surface:$ADMIN_PORT-admin-spa-not-guest-reachable" FAIL ":$ADMIN_PORT/ -> $FETCH_CODE from a guest vantage: the admin board IS answering a br-lan client, so 31-admin-board-not-guest-reachable.nft is inert (or --vantage guest was forced on a box this run can reach :$ADMIN_PORT from -- re-run with --vantage auto|mgmt in that case)"
        fi
    fi
else
    admin_spa_check
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
        chain_port="$(url_port "$target" 2>/dev/null || true)"
        [ -n "$chain_port" ] && reach "$chain_port" "captive:chain-ends-200"
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
        ns_port="$(url_port "$ns_href" 2>/dev/null || true)"
        [ -n "$ns_port" ] && reach "$ns_port" "captive:stub-noscript-fallback"
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
fold api "${API_HELPER[@]}" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only api \
    $([ "$STRICT" = "1" ] && printf '%s' --strict)
printf '\n===== 5. Lightning quote path =====\n'
fold ln "${API_HELPER[@]}" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only ln

printf '\n===== 6. money path (default: nothing of value is sent) =====\n'
MONEY_ARGS=""
[ "$SKIP_MONEY_PATH" = "1" ] && MONEY_ARGS="--skip-money-path"
fold money "${API_HELPER[@]}" --router-ip "$ROUTER_IP" --api-port "$API_PORT" \
    --only money $MONEY_ARGS
if [ -z "${RHP_CASHU_TOKEN:-}" ]; then
    note "paid lane: RHP_CASHU_TOKEN not set -> no token is sent and no ecash is touched (by design)"
fi
fold paid "${API_HELPER[@]}" --router-ip "$ROUTER_IP" --api-port "$API_PORT" --only paid
if [ -z "${RHP_CASHU_TOKEN:-}" ]; then
    chk "paid:spends-nothing-by-default" PASS "the default run sent no token; only an empty-body POST touched the payment lane"
else
    chk "paid:spends-nothing-by-default" SKIP "RHP_CASHU_TOKEN WAS supplied, so this run DID touch the payment lane: read the paid:* lines above, do not read this line as 'nothing was spent'"
fi

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
# The section-0 burst is the only place this harness reports on a state it has
# not reasoned about yet, so its failures are PROVISIONAL -- a non-terminal
# status that section 0 prints and never counts. Resolve them against the rest of
# the run before the summary: a port that some later check demonstrably REACHED
# (a completed request against that port, recorded in $REACHED by the check that
# made it) was raced, not dead, and reporting it as a fatal preflight failure
# would make a red line mean nothing -- the exact confusion this harness exists
# to remove.
# Every pending id leaves this block as exactly one terminal line, so no check id
# is ever both green and red in one transcript.
# A port is credited ONLY from the reach record: a (port, id) pair written when a
# check completed a live request against that exact port. An id whitelist was
# tried first and was wrong (round-1 cross-family review, finding F1): it
# credited :$TLS_PORT from `surface:$LUCI_PORT-target-200`, whose port component
# is the LUCI port while the fetch follows a 307 Location that may go anywhere,
# and it credited the live ports from identity ids whose provenance is a
# filesystem comparison as much as a fetch (F2/F3). Both classes are gone: a
# dead port cannot be demoted by a check that never touched it, and there is no
# list left to keep in sync with the ids the run actually emits.
reach_evidence_id() {  # reach_evidence_id <port> -> id of the check that reached it
    awk -F'\t' -v p="$1" '$1 == p { print $2; exit }' "$REACHED" 2>/dev/null
}

if [ -n "$PENDING_TCP_PORTS" ]; then
    printf '\n===== verdict: resolving the section-0 liveness burst (RHPPROVISIONAL notes above) =====\n'
    for port in $PENDING_TCP_PORTS; do
        ev_id=""; ev_line=""
        ev_id="$(reach_evidence_id "$port")"
        if [ -n "$ev_id" ]; then
            # The id must still carry a PASS line AND still agree with the tally:
            # a reach record for an id that never passed would be a bug in the
            # recording, not evidence that the port is up.
            ev_line="$(grep -m1 -E "^RHPCHECK $ev_id PASS " "$TRANSCRIPT" | cut -c1-200)"
            grep -qE "^$ev_id PASS( |\$)" "$TALLY" || ev_line=""
        fi
        if [ -n "$ev_id" ] && [ -n "$ev_line" ]; then
            chk "net:tcp-$port" WARN "the section-0 burst found no listener on $ROUTER_IP:$port after $TCP_TRIES attempts, but $ev_id reached :$port later in this same run: $ev_line -- the burst raced the box, not a defect, so the preflight line is demoted here and is NOT fatal. No red line for a port this run itself went on to use"
        else
            chk "net:tcp-$port" FAIL "no TCP listener answers on $ROUTER_IP:$port after $TCP_TRIES attempts (ICMP is dropped here, so this TCP result IS the liveness answer), and no check in this run completed a request against :$port either: this is FINAL, not a race -- the section-0 result stands and is fatal"
        fi
    done
fi

printf '\nRHPRESULT total=%d pass=%d fail=%d skip=%d warn=%d\n' "$TOTAL" "$PASSED" "$FAILED_N" "$SKIPPED" "$WARNED"
if [ -n "$WARNED_IDS" ]; then
    printf 'RHPWARNED%s\n' "$WARNED_IDS"
fi
if [ "$FAILED_N" -gt 0 ]; then
    printf 'RHPFAILED%s\n' "$FAILED_IDS"
    printf 'RHPEXIT 1\n'
    exit 1
fi
printf 'RHPEXIT 0\n'
exit 0
