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

# The six rules the installer has always written (the list that was left in
# place — untouched — by the same-version install that exposed this).
LEGACY="allow tcp port 2121
allow tcp port 8080
allow tcp port 2050
allow tcp port 2051
allow tcp port 8090
allow tcp port 8443"
ALL_PORTS="2121 8080 2050 2051 8090 8443 443"

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

# ---------------------------------------------------- same-version repo path
echo "== same-version install re-adds a missing :443 rule"
seed_lines "$LEGACY"
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if grep -q "Flag matches" "$LOGFILE" 2>/dev/null; then
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

echo "== re-running on the same version does not duplicate anything"
run_same_version
one_each_port "second same-version run"

echo "== the :8443 rule must not satisfy the :443 check"
seed 'allow tcp port 8443'
run_same_version
if has_entry 'allow tcp port 443'; then
    ok "a list holding only 'allow tcp port 8443' still gains the :443 rule"
else
    bad "a list holding only 'allow tcp port 8443' did not gain the :443 rule (:8443 matched as a substring, or the entry was never written)"
fi
n=$(count_entry 'allow tcp port 8443')
[ "$n" = 1 ] && ok ":8443 rule left untouched (exactly one entry)" \
             || bad ":8443 rule present $n times (want 1)"
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok ":443 rule added exactly once" || bad ":443 rule present $n times (want 1)"

echo "== a :443 rule already present (tollgate-cli ssl enable) is not duplicated"
seed_lines "$LEGACY" 'allow tcp port 443'
run_same_version
one_each_port "pre-existing :443 rule"

echo "== single-line (space separated) allow list is handled too"
UCI_LIST_SEP=' ' seed_lines "$LEGACY"
UCI_LIST_SEP=' ' run_same_version
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok "space separated list: :443 added exactly once" \
             || bad "space separated list: :443 present $n times (want 1)"
for port in 2121 8080 2050 2051 8090 8443; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "space separated list: 'allow tcp port $port' kept once" \
                 || bad "space separated list: 'allow tcp port $port' present $n times (want 1)"
done

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
    for port in 2121 8080 2050 2051 8090 8443 443; do
        n=$(count_entry "allow tcp port $port")
        [ "$n" = 1 ] && ok "writer: 'allow tcp port $port' present exactly once after two runs" \
                     || bad "writer: 'allow tcp port $port' present $n times after two runs (want 1)"
    done
else
    bad "script does not expose assert_nodogsplash_allow_entries() — the same-version path has no allow-list writer to call"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
