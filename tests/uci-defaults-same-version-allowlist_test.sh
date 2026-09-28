#!/usr/bin/env bash
# Offline tests for the captive-portal allow list on the SAME-VERSION setup
# path of packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The real bug this guards: the flag file /etc/tollgate-setup-done stores the
# setup version, and when it already equals SETUP_VERSION the script takes the
# short path — verify the wireless APs, re-assert the uhttpd contract, delete a
# stale nodogsplash gatewaydomainname — and returns. That path never touched
# the nodogsplash users_to_router allow list, so an install of a build whose
# version string is unchanged (the pre15 round: both pre14 and pre15 report
# v0.6.0-alpha3, so apk sees the same 0.6.0_alpha3-r0 and the flag stays equal)
# kept the pre-existing list.
#
# WHAT THE LIST MUST HOLD (2026-09-26 contract): `users_to_router` is
# nodogsplash's PRE-AUTHENTICATION allow list, i.e. what a client that has paid
# nothing may reach ON THE ROUTER, and the customer journey owns exactly the
# portal (`:2050` NDS gateway, `:2051` SPA) plus the backend API (`:2121`).
# Every admin surface is off it:
#
#   * `:8090` / `:8443` — the owner-facing admin board (uhttpd.admin), whose
#     login form and rpcd `/ubus` session endpoint share one cleartext origin;
#     the session behind them reaches `file:["exec"]`,
#     `system:["password_set"]` and `tollgate wallet_drain_cashu`.
#   * `:8080` / `:443` — LuCI, whose `/www/index.html` meta-refreshes into
#     `/cgi-bin/luci`. Measured on the bench MT3000 (2026-09-25, pre17): a
#     paying guest whose plain-HTTP `http://<router>/` was dead (nothing
#     listened on :80 — see tests/uci-defaults-trusted-entry-80_test.sh) fell
#     through to `https://<router>/` and landed on that login, which the
#     operator reported as "the install is broken".
#
# An EARLIER decision (docs/architecture/luci-https-pre-auth-reachability-decision.md,
# 2026-09-22) ADDED `:8080` and `:443` here on purpose, to keep the `:8080` ->
# `https://<router>/` hop from dead-ending. This test pinned that contract; it
# now pins the opposite, so the reversal is guarded rather than merely
# implemented. The removals must be active (`del_list`), not just omitted: an
# already-deployed router carries all four entries — an earlier build wrote
# them, the feed's 92-tollgate-admin-setup still re-asserts `:8090`/`:8443`, and
# `tollgate-cli ssl enable` still writes `:443`.
#
# Nothing here needs a router, the SDK or the network: the setup script is
# copied into a temp dir (only its SETUP_FLAG and LOGFILE paths are rewritten —
# both are absolute paths into the live system) and run with `/bin/sh` against
# a fake `uci` and a fake `apk`, so the real driver — version resolution, the
# same-version branch, the allow-list writer — is exercised end to end.
#
# Usage: bash tests/uci-defaults-same-version-allowlist_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
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
# run resolves the version from the package manager. `apk list --installed` is
# the apk-lane branch of that resolution; the fake prints the shape the real
# one does, so the same version the installer would see is the one the test
# seeds into the flag file.
FAKE_VERSION="0.6.0_alpha4-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
# fake apk — only `apk list --installed` is consulted by the setup script.
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, list options stored as
# one line per entry. `get` joins a list's entries with $UCI_LIST_SEP (default
# newline, i.e. the modern one-entry-per-line form); a space reproduces the
# single-line form, where the entry under test may sit mid-line.
export UCI_STATE="$TMP/uci.state"
export UCI_LIST_SEP="${UCI_LIST_SEP:-}"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
sep="${UCI_LIST_SEP:-}"
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
        # `uci del_list cfg.sec.opt=value` drops every entry equal to value.
        # The list options under test carry spaces ("allow tcp port 8090"), so
        # the match is whole-line, never word-split and never a substring: an
        # operator's own "allow tcp port 8081" must survive the removal of
        # "allow tcp port 8080".
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
# Only two absolute paths are redirected: the flag file (which decides
# same-version vs full setup) and the log. The body — including the same-version
# branch and every allow-list write — is the shipped code, run by /bin/sh.
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi

# ------------------------------------------- admin credential fixtures
# The same driver also runs the admin-credential gate, and that reads the live
# /etc/shadow (and would invoke `passwd root` if the hash were empty). Pin both
# files to fixtures — a router whose credential is already set, so the gate is
# a no-op here and this test never touches the host's account state.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"

# ------------------------------------------- the :80 entry point's docroot
# The same-version path also re-asserts the plain-HTTP entry point
# (setup_uhttpd_trusted_entry), which writes a stub document into its docroot.
# Pin that to the sandbox: without this the test would write into the LIVE
# /etc/tollgate/router-home of whatever host runs it.
export ROUTER_HOME_DIR="$TMP/router-home"

KEY="nodogsplash.@nodogsplash[0].users_to_router"
seed() { # seed <list entry>... — one argument per entry
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    local e
    for e in "$@"; do printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"; done
}
# seed_lines <newline separated entries> [extra entry]... — for the LEGACY
# block below, whose entries contain spaces and must not be word-split.
seed_lines() {
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    printf '%s\n' "$1" | while IFS= read -r e; do
        [ -n "$e" ] && printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"
    done
    shift
    local e
    for e in "$@"; do printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"; done
}
count_entry() { grep -F -c "$KEY=$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has_entry() { grep -F -q "$KEY=$1" "$UCI_STATE"; }
has_no_entry() { ! grep -F -q "$KEY=$1" "$UCI_STATE"; }

# The list an operator's router carries today: the customer journey's rules plus
# every admin-surface allowance the fleet accumulated — the board's
# :8090/:8443 (an earlier build wrote them, the feed's 92-tollgate-admin-setup
# still re-asserts them) and LuCI's :8080/:443.
LEGACY="allow tcp port 2121
allow tcp port 8080
allow tcp port 2050
allow tcp port 2051
allow tcp port 8090
allow tcp port 8443
allow tcp port 443"
# Entries the allow list MUST hold exactly once after any run: the customer
# journey and nothing else.
JOURNEY_PORTS="2121 2050 2051"
# Entries the allow list must NEVER hold: LuCI (an admin login) and the admin
# board (a root-capable login over plain HTTP), all reachable over the captive
# bridge today.
ADMIN_PORTS="8080 443 8090 8443"

run_same_version() { # run the copied script with the flag already set
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

journey_ports_once() { # <label> — every customer-journey port present exactly once
    local label="$1" port n
    for port in $JOURNEY_PORTS; do
        n=$(count_entry "allow tcp port $port")
        [ "$n" = 1 ] && ok "$label: 'allow tcp port $port' present exactly once" \
                     || bad "$label: 'allow tcp port $port' present $n times (want 1)"
    done
}

no_admin_ports() { # <label> — every admin surface stays out of the pre-auth list
    local label="$1" port
    for port in $ADMIN_PORTS; do
        if has_no_entry "allow tcp port $port"; then
            ok "$label: 'allow tcp port $port' absent from users_to_router"
        else
            bad "$label: 'allow tcp port $port' is in users_to_router — a pre-auth guest on the open SSID can reach an admin login"
        fi
    done
}

# ---------------------------------------------------- same-version repo path
echo "== same-version install converges the allow list on the customer journey"
seed_lines "$LEGACY"
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
# The branch the driver took, as the driver itself logged it:
#   `2026-09-25 01:00:00 - Setup branch VERIFY — marker matches: recorded=… expected=…`
# The anchor is the verdict TOKEN followed by a boundary. A bare
# `Setup branch VERIFY` also matches `Setup branch VERIFY_REPAIR` (the driver
# logs the two verdicts with the same prefix); the control below pins that the
# distinction is real, so the assertion cannot be silently weakened.
BRANCH_VERIFY_ANCHOR='Setup branch VERIFY([[:space:]]|$)'
branch_is_verify() { grep -Eq "$BRANCH_VERIFY_ANCHOR" "$1" 2>/dev/null; }

# Control for the anchor itself: a `Setup branch VERIFY_REPAIR` line must NOT
# satisfy it. VERIFY_REPAIR on this fixture means the marker was rewritten when
# it should have been left alone — precisely the regression the assertion below
# exists to catch — so an anchor that cannot tell the two apart is not an
# assertion at all.
printf '%s\n' '2026-01-01 00:00:00 - Setup branch VERIFY_REPAIR — not an orderable release marker: recorded=unsubstituted expected=v0.6.0-alpha4 (relation UNORDERABLE)' > "$TMP/anchor-probe.log"
if branch_is_verify "$TMP/anchor-probe.log"; then
    bad "anchor control: a 'Setup branch VERIFY_REPAIR' line satisfies the VERIFY anchor"
else
    ok "anchor control: a 'Setup branch VERIFY_REPAIR' line does not satisfy the VERIFY anchor"
fi

# The marker here is the apk-shaped version the resolution path produces
# (`0.6.0_alpha4-r0`), which normalises to the same release as the shipped
# `v0.6.0-alpha4`, so the verdict is SAME -> verify/repair.
if branch_is_verify "$LOGFILE"; then
    ok "same-version branch was the path taken (flag = $FAKE_VERSION)"
else
    bad "same-version branch not taken (log: $(head -n 2 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
journey_ports_once "same-version install"
no_admin_ports "same-version install"

echo "== re-running on the same version does not duplicate anything"
run_same_version
journey_ports_once "second same-version run"
no_admin_ports "second same-version run"

echo "== an admin-surface allowance left by an earlier install is removed"
# This is the fleet case: the router already carries all four entries because a
# previous install (or the feed's 92-tollgate-admin-setup, or tollgate-cli's
# `ssl enable`) wrote them. Omitting them from the writer only fixes a
# factory-fresh router, so the removal has to happen on every path that runs.
seed 'allow tcp port 2121' 'allow tcp port 8090' 'allow tcp port 8443'
run_same_version
[ "$(count_entry 'allow tcp port 2121')" = 1 ] \
    && ok "stale allowance: the captive-portal rules are left alone" \
    || bad "stale allowance: 'allow tcp port 2121' lost"
no_admin_ports "stale allowance"

echo "== the LuCI allowance an earlier install wrote is removed too"
# The pre-fix shipped writer ADDED both of these, so a router installed before
# this change carries them even though nothing writes them any more.
seed 'allow tcp port 8080' 'allow tcp port 443'
run_same_version
no_admin_ports "LuCI allowance"
journey_ports_once "LuCI allowance"

echo "== the removal is by whole value, not by substring"
# An operator's own rule on a neighbouring port must survive: `del_list
# …='allow tcp port 443'` must not take `allow tcp port 8443` with it, and the
# removal of `:8080` must not touch a hand-written `:8081`. This is the trap the
# old anchored add-check existed for (#516), inverted: with the adds gone, the
# risk moves to a removal implemented as a substring or sed match.
seed_lines "allow tcp port 4443
allow tcp port 8081" 'allow tcp port 8080'
run_same_version
for keep in 4443 8081; do
    n=$(count_entry "allow tcp port $keep")
    [ "$n" = 1 ] && ok "the operator's own 'allow tcp port $keep' rule survived" \
                 || bad "the operator's own 'allow tcp port $keep' rule was removed by a neighbouring-port removal ($n copies, want 1)"
done
n=$(count_entry 'allow tcp port 8080')
[ "$n" = 0 ] && ok "the :8080 admin allowance was removed while :8081 survived" \
             || bad ":8080 allowance present $n times (want 0)"
journey_ports_once "neighbouring-port removal"

echo "== single-line (space separated) allow list is handled too"
UCI_LIST_SEP=' ' seed_lines "$LEGACY"
UCI_LIST_SEP=' ' run_same_version
journey_ports_once "space separated list"
no_admin_ports "space separated list"

# ------------------------------------------------------- lib-only seam check
# The same-version path and the full-setup path must share one writer: the
# function the fix extracts is exercised directly here, with the script sourced
# lib-only (no setup run) exactly like the other 99-tollgate-setup tests.
echo "== the shared allow-list writer is idempotent on its own"
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
if command -v assert_nodogsplash_allow_entries >/dev/null 2>&1; then
    ok "script exposes assert_nodogsplash_allow_entries()"
    GATEWAY_NAME="TestGate"
    seed_lines "$LEGACY"
    assert_nodogsplash_allow_entries >/dev/null 2>&1
    assert_nodogsplash_allow_entries >/dev/null 2>&1
    for port in $JOURNEY_PORTS; do
        n=$(count_entry "allow tcp port $port")
        [ "$n" = 1 ] && ok "writer: 'allow tcp port $port' present exactly once after two runs" \
                     || bad "writer: 'allow tcp port $port' present $n times after two runs (want 1)"
    done
    no_admin_ports "writer"
else
    bad "script does not expose assert_nodogsplash_allow_entries() — the same-version path has no allow-list writer to call"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
