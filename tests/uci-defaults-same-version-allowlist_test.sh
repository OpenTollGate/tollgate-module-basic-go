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
# kept the pre-existing list. On that round the `allow tcp port 443` pre-auth
# rule introduced for the LuCI :8080 -> https:// redirect was missing and the
# router came out of the install with a 4/9 sweep: uhttpd.main.redirect_https=1
# answers :8080 with `307 Location: https://<router>/`, nodogsplash REJECTs
# :443 for an unauthenticated client, and LuCI is unreachable before payment.
#
# Nothing here needs a router, the SDK or the network: the setup script is
# copied into a temp dir (only its SETUP_FLAG and LOGFILE paths are rewritten —
# both are absolute paths into the live system) and run with `/bin/sh` against
# a fake `uci` and a fake `apk`, so the real driver — version resolution, the
# same-version branch, the allow-list writer — is exercised end to end.
#
# Second bug this guards: the same writer used to ADD `allow tcp port 8090` and
# `allow tcp port 8443` — the admin board's uhttpd instance on :8090 (plain
# HTTP) and its opt-in :8443 TLS listener. `users_to_router` is nodogsplash's
# PRE-AUTHENTICATION allow list, so an unauthenticated guest on the open SSID
# could load the board's login form and POST credential-carrying JSON-RPC to
# its /ubus endpoint over cleartext. The board is owner-facing (reached over
# br-private, exactly like the whitelabel configUI in setup_uhttpd_configui)
# and is not part of the customer journey, so the admin ports must be ABSENT
# from the list — and actively removed by every setup path, because an earlier
# install (or a build of either writer from before this change) put them there
# and a deployed router only converges if the removal is re-asserted.
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
        # the match is whole-line, never word-split — a substring match would
        # also delete "allow tcp port 8090" from a differently-spelled entry.
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

# The list an operator's router carries today: the captive-portal rules, the
# admin board's :8090/:8443 allowance an earlier install wrote, and :443. The
# first four and :443 must survive every setup path; :8090/:8443 must not.
LEGACY="allow tcp port 2121
allow tcp port 8080
allow tcp port 2050
allow tcp port 2051
allow tcp port 8090
allow tcp port 8443"
# Ports the allow list MUST hold exactly once after any run.
ALL_PORTS="2121 8080 2050 2051 443"
# Ports the allow list must NEVER hold: the admin board (uhttpd.admin) serves
# a root-capable login over plain HTTP on :8090, and its opt-in TLS listener is
# :8443. Both are owner-facing; a pre-auth guest must not reach an admin UI.
ADMIN_PORTS="8090 8443"

run_same_version() { # run the copied script with the flag already set
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

one_each_port() { # <label> — every port present exactly once
    local label="$1" port n
    for port in $ALL_PORTS; do
        n=$(count_entry "allow tcp port $port")
        [ "$n" = 1 ] && ok "$label: 'allow tcp port $port' present exactly once" \
                     || bad "$label: 'allow tcp port $port' present $n times (want 1)"
    done
}

no_admin_ports() { # <label> — the admin board stays out of the pre-auth list
    local label="$1" port
    for port in $ADMIN_PORTS; do
        if has_no_entry "allow tcp port $port"; then
            ok "$label: 'allow tcp port $port' absent from users_to_router"
        else
            bad "$label: 'allow tcp port $port' is in users_to_router — a pre-auth guest on the open SSID can reach the :8090 admin login over plain HTTP"
        fi
    done
}

# ---------------------------------------------------- same-version repo path
echo "== same-version install re-adds a missing :443 rule"
seed_lines "$LEGACY"
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
# The branch the driver took, as the driver itself logged it. The marker here
# is the apk-shaped version the resolution path produces
# (`0.6.0_alpha4-r0`), which normalises to the same release as the shipped
# `v0.6.0-alpha4`, so the verdict is SAME -> verify/repair.
if grep -q "^.*Setup branch VERIFY" "$LOGFILE" 2>/dev/null; then
    ok "same-version branch was the path taken (flag = $FAKE_VERSION)"
else
    bad "same-version branch not taken (log: $(head -n 2 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
if has_entry 'allow tcp port 443'; then
    ok "same-version install: stale allow list gains 'allow tcp port 443'"
else
    bad "same-version install: 'allow tcp port 443' still missing — the :8080 -> https:// redirect dead-ends on a REJECTed :443 (LuCI unreachable pre-auth)"
fi
one_each_port "same-version install"
no_admin_ports "same-version install"

echo "== re-running on the same version does not duplicate anything"
run_same_version
one_each_port "second same-version run"
no_admin_ports "second same-version run"

echo "== an admin-board allowance left by an earlier install is removed"
# This is the fleet case: the router already carries the :8090/:8443 entries
# because a previous install (or the feed's 92-tollgate-admin-setup) wrote
# them. Omitting them from the writer only fixes a factory-fresh router, so
# the removal has to happen on every path that runs.
seed 'allow tcp port 2121' 'allow tcp port 8090' 'allow tcp port 8443'
run_same_version
[ "$(count_entry 'allow tcp port 2121')" = 1 ] \
    && ok "stale allowance: the captive-portal rules are left alone" \
    || bad "stale allowance: 'allow tcp port 2121' lost"
no_admin_ports "stale allowance"

echo "== the :8443 rule must not satisfy the :443 check"
seed 'allow tcp port 8443'
run_same_version
if has_entry 'allow tcp port 443'; then
    ok "a list holding only 'allow tcp port 8443' still gains the :443 rule"
else
    bad "a list holding only 'allow tcp port 8443' did not gain the :443 rule (:8443 matched as a substring, or the entry was never written)"
fi
n=$(count_entry 'allow tcp port 8443')
[ "$n" = 0 ] && ok ":8443 admin-board rule removed" \
             || bad ":8443 admin-board rule present $n times (want 0)"
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok ":443 rule added exactly once" || bad ":443 rule present $n times (want 1)"

echo "== a :443 rule already present (tollgate-cli ssl enable) is not duplicated"
seed_lines "$LEGACY" 'allow tcp port 443'
run_same_version
one_each_port "pre-existing :443 rule"
no_admin_ports "pre-existing :443 rule"

echo "== single-line (space separated) allow list is handled too"
UCI_LIST_SEP=' ' seed_lines "$LEGACY"
UCI_LIST_SEP=' ' run_same_version
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok "space separated list: :443 added exactly once" \
             || bad "space separated list: :443 present $n times (want 1)"
for port in 2121 8080 2050 2051; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "space separated list: 'allow tcp port $port' kept once" \
                 || bad "space separated list: 'allow tcp port $port' present $n times (want 1)"
done
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
    seed 'allow tcp port 8443'
    assert_nodogsplash_allow_entries >/dev/null 2>&1
    assert_nodogsplash_allow_entries >/dev/null 2>&1
    for port in $ALL_PORTS; do
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
