#!/usr/bin/env bash
# Offline test for the invariant that the admin board is never left standing on
# a router whose root credential is UNSET — by SETTING A FIXED DEFAULT. No
# router, no SDK, no network.
#
# The defect this guards: the board's only login endpoint is rpcd's
# session.login, and the distribution's /etc/config/rpcd maps that login's
# password to root's shadow hash (`password '$p$root'` ->
# getspnam("root")->sp_pwdp). rpcd's rpc_login_test_password() begins with
# `if (!hash || !*hash) return true;` — an EMPTY hash authenticates ANY
# password, including the empty string. A stock first-boot rootfs has exactly
# that empty hash, so a freshly packaged router handed out a root session to
# whoever reached the listener (ACL: file exec, system.password_set,
# tollgate wallet_drain_cashu).
#
# The fix has TWO halves and this suite pins both:
#   * when root has NO credential, the package SETS A FIXED DEFAULT
#     (TOLLGATE_DEFAULT_CREDENTIAL = "TollGate") and marks it PROVISIONAL, so
#     the operator can always log in and the admin/config UI forces them to
#     choose their own at first login — a router is never left unusable, and
#     never left on a value the operator cannot know (a random one);
#   * the package STILL accepts NO install-time credential: a value from
#     TOLLGATE_ADMIN_PASSWORD or a staged /etc/tollgate/admin-password file must
#     never reach root (that is how an operator ends up on a run-book EXAMPLE);
#   * an operator's own credential is PRESERVED — never rewritten;
#   * if the default write does not take, the gate FAILS CLOSED (drops the
#     admin listeners) rather than leaving an empty hash behind a live board.
#
# It reuses the repo's existing packaging tier instead of inventing a harness:
# the unit assertions source the shipped script's functions with
# TOLLGATE_SETUP_LIB_ONLY=1 against a fake `uci`, a fake `passwd` and a fake
# /etc/shadow (the tests/uci-defaults-band_test.sh seam), and the integration
# assertion RUNS the shipped driver on the same-version path exactly as
# tests/uci-defaults-same-version-allowlist_test.sh does — a gate that no
# driver calls is not a control.
#
# Usage: bash tests/packaging/admin-board-defaults-to-tollgate_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
DEFAULT_CREDENTIAL="TollGate"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

export TOLLGATE_ENTRY_UI_MARKER="$TMP/entry-ui-mapping"
export TOLLGATE_ENTRY_UI_CONFIG="$TMP/config.json"
export ADMIN_PROVISIONAL_MARKER="$TMP/admin-credential-provisional"
export TOLLGATE_ADMIN_PASSWORD=""

# --------------------------------------------------------------- fake apk
FAKE_VERSION="0.6.0_alpha4-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# --------------------------------------------------------------- fake uci
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
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
        [ "$key" = "$1" ] && exit 1
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -e "$1=" -e "$1." "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export)
        cat "$state"
        ;;
    commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# ------------------------------------------------------------ fake passwd
# Emulates BusyBox `passwd root`: reads the new password twice from stdin and
# writes a hash into the shadow file. PASSWD_FAILS=1 makes it exit 1 without
# touching the file — the "passwd reported success (or failed) but the hash
# never changed" case the gate must catch (fail closed).
export PASSWD_LOG="$TMP/passwd.log"
export PASSWD_SEEN="$TMP/passwd.seen"
export PASSWD_FAILS=0
cat > "$TMP/bin/passwd" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "$PASSWD_LOG"
IFS= read -r p1 || p1=""
IFS= read -r p2 || p2=""
[ "${PASSWD_FAILS:-0}" = "1" ] && exit 1
[ -n "$p1" ] || exit 1
printf '%s\n' "$p1" > "$PASSWD_SEEN"
awk -F: -v OFS=: -v h='$1$stub$abcdefghijklmnopqrstuv' \
    '$1 == "root" { $2 = h } { print }' "$SHADOW_FILE" > "$SHADOW_FILE.tmp" 2>/dev/null
mv "$SHADOW_FILE.tmp" "$SHADOW_FILE"
exit 0
SHIM
chmod +x "$TMP/bin/passwd"
export ORIGINAL_PATH="$PATH"
export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
export LOGFILE
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
export ROUTER_HOME_DIR="$TMP/router-home"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=\"\${LOGFILE:-/tmp/tollgate-setup\.log}\"\$|LOGFILE=$LOGFILE|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi

# ------------------------------------------------------------- assertions
ADMIN_HTTP="uhttpd.admin.listen_http=0.0.0.0:8090"
ADMIN_HTTP6="uhttpd.admin.listen_http=[::]:8090"
ADMIN_HTTPS="uhttpd.admin.listen_https=0.0.0.0:8443"
ADMIN_HTTPS6="uhttpd.admin.listen_https=[::]:8443"
CFGUI_SEC="net4"; CFGUI_SEC="${CFGUI_SEC}sats"
CFGUI_HTTP="uhttpd.$CFGUI_SEC.listen_http=0.0.0.0:8090"
CFGUI_HTTP6="uhttpd.$CFGUI_SEC.listen_http=[::]:8090"
MAIN_STRAY="uhttpd.main.listen_http=0.0.0.0:8090"
MAIN_LUCI="uhttpd.main.listen_http=0.0.0.0:8080"

count_state() { grep -F -c -- "$1" "$UCI_STATE" 2>/dev/null || true; }
has_state()   { grep -F -q -- "$1" "$UCI_STATE" 2>/dev/null; }
section_lines() { grep -E -c -- "^uhttpd\.$1(\.|=)" "$UCI_STATE" 2>/dev/null || true; }

seed_deployed_router() {
    : > "$UCI_STATE"
    {
        printf '%s\n' \
            'uhttpd.admin=uhttpd' "$ADMIN_HTTP" "$ADMIN_HTTP6" "$ADMIN_HTTPS" "$ADMIN_HTTPS6" \
            "uhttpd.$CFGUI_SEC=uhttpd" "$CFGUI_HTTP" "$CFGUI_HTTP6" \
            'uhttpd.main=uhttpd' "$MAIN_LUCI" "$MAIN_STRAY"
    } >> "$UCI_STATE"
}

assert_board_dropped() {
    local label="$1" gone=1 entry
    for entry in "$ADMIN_HTTP" "$ADMIN_HTTP6" "$ADMIN_HTTPS" "$ADMIN_HTTPS6" "$CFGUI_HTTP" "$CFGUI_HTTP6" "$MAIN_STRAY"; do
        if has_state "$entry"; then
            gone=0
            bad "$label: $entry is still configured — the board is served behind an empty root hash"
        fi
    done
    [ "$gone" = 1 ] && ok "$label: every :8090/:8443 admin listener was dropped"
    if [ "$(section_lines "$CFGUI_SEC")" = 0 ]; then
        ok "$label: the foreign configUI section was deleted, not just port-stripped"
    else
        bad "$label: the foreign configUI section survived ($(section_lines "$CFGUI_SEC") config lines)"
    fi
    if has_state "$MAIN_LUCI"; then
        ok "$label: LuCI's :8080 listener was left alone"
    else
        bad "$label: LuCI's :8080 listener was removed (the gate must not touch non-admin surfaces)"
    fi
}

assert_board_kept() {
    local label="$1" hash
    hash="$(awk -F: '$1 == "root" { print $2; exit }' "$SHADOW_FILE" 2>/dev/null || true)"
    if [ -n "$hash" ]; then
        ok "$label: a credential backs the board (root hash is not empty)"
    else
        bad "$label: the board is served with an EMPTY root hash — rpcd authenticates ANY password against it"
    fi
    if has_state "$ADMIN_HTTP" && has_state "$ADMIN_HTTPS"; then
        ok "$label: the :8090/:8443 admin board is still served"
    else
        bad "$label: the admin listener was dropped although a credential exists — the owner loses the board"
    fi
}

seed_shadow() { # seed_shadow <hash-field> — writes a fake /etc/shadow
    printf 'root:%s:0:0:99999:7:::\ndaemon:*:0:0:99999:7:::\n' "$1" > "$SHADOW_FILE"
    : > "$PASSWD_FILE"
}

reset_run() {
    : > "$LOGFILE"
    : > "$PASSWD_LOG"
    : > "$PASSWD_SEEN"
    rm -f "$ADMIN_PROVISIONAL_MARKER"
    PASSWD_FAILS=0
    export PASSWD_FAILS
}

passwd_calls() { grep -c . "$PASSWD_LOG" 2>/dev/null || true; }

run_same_version() {
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >"$TMP/run.out" 2>"$TMP/run.err"
    return $?
}

BRANCH_VERIFY_ANCHOR='Setup branch VERIFY([[:space:]]|$)'
branch_is_verify() { grep -Eq "$BRANCH_VERIFY_ANCHOR" "$1" 2>/dev/null; }
printf '%s\n' '2026-01-01 00:00:00 - Setup branch VERIFY_REPAIR — not an orderable release marker: recorded=unsubstituted expected=v0.6.0-alpha4 (relation UNORDERABLE)' > "$TMP/anchor-probe.log"
if branch_is_verify "$TMP/anchor-probe.log"; then
    bad "anchor control: a 'Setup branch VERIFY_REPAIR' line satisfies the VERIFY anchor"
else
    ok "anchor control: a 'Setup branch VERIFY_REPAIR' line does not satisfy the VERIFY anchor"
fi

# ----------------------------------------------------- the driver calls it
echo "== same-version reinstall, empty hash, FAILING passwd: fail closed"
seed_deployed_router
seed_shadow ''
reset_run
PASSWD_FAILS=1
export PASSWD_FAILS
run_same_version
rc=$?
[ "$rc" = "0" ] \
    && ok "the same-version run still exits 0 (the gate refuses the board, it does not break the install)" \
    || bad "the same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if branch_is_verify "$LOGFILE"; then
    ok "the same-version branch was the path taken"
else
    bad "the same-version branch was not taken (log: $(head -n 2 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
assert_board_dropped "same-version reinstall, empty hash + failing passwd"

echo "== same-version reinstall, empty hash: the default is set and the board is served"
seed_deployed_router
seed_shadow ''
reset_run
run_same_version
root_hash="$(awk -F: '$1 == "root" { print $2; exit }' "$SHADOW_FILE" 2>/dev/null || true)"
[ -n "$root_hash" ] \
    && ok "a credential was established on the credential-less router" \
    || bad "the router was left credential-less after install"
[ "$(cat "$PASSWD_SEEN" 2>/dev/null)" = "$DEFAULT_CREDENTIAL" ] \
    && ok "the credential set is the fixed default ($DEFAULT_CREDENTIAL)" \
    || bad "the credential set was '$(cat "$PASSWD_SEEN" 2>/dev/null)' (want $DEFAULT_CREDENTIAL)"
[ -f "$ADMIN_PROVISIONAL_MARKER" ] \
    && ok "the provisional marker was written (the admin/config UI forces a change)" \
    || bad "the provisional marker was NOT written — the default would be treated as the operator's choice"
assert_board_kept "same-version reinstall, empty hash"

# ------------------------------------------------------- source every function
export TOLLGATE_ADMIN_PASSWORD=""
TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT_UNDER_TEST" 2>/dev/null
if ! command -v admin_credential_state >/dev/null 2>&1; then
    echo "FAIL the shipped setup script exposes no admin credential gate (admin_credential_state)" >&2
    exit 1
fi
ok "setup script sources in lib-only mode and exposes the credential gate"
if [ "${TOLLGATE_DEFAULT_CREDENTIAL:-}" = "$DEFAULT_CREDENTIAL" ]; then
    ok "the fixed default credential constant is exposed ($DEFAULT_CREDENTIAL)"
else
    bad "TOLLGATE_DEFAULT_CREDENTIAL is '${TOLLGATE_DEFAULT_CREDENTIAL:-<unset>}' (want $DEFAULT_CREDENTIAL)"
fi

# ------------------------------------------------------- credential states
echo "== the credential state is read from root's hash, not assumed"
seed_shadow '$1$abc$0123456789abcdef'
[ "$(admin_credential_state)" = "set" ] \
    && ok "a real hash reads as 'set'" \
    || bad "a real hash read as '$(admin_credential_state)' (want set)"
seed_shadow ''
[ "$(admin_credential_state)" = "empty" ] \
    && ok "an empty hash reads as 'empty'" \
    || bad "an empty hash read as '$(admin_credential_state)' (want empty)"
seed_shadow '!'
[ "$(admin_credential_state)" = "locked" ] \
    && ok "a locked account ('!') reads as 'locked'" \
    || bad "a '!'-locked account read as '$(admin_credential_state)' (want locked)"
seed_shadow '*'
[ "$(admin_credential_state)" = "locked" ] \
    && ok "a locked account ('*') reads as 'locked'" \
    || bad "a '*'-locked account read as '$(admin_credential_state)' (want locked)"
rm -f "$SHADOW_FILE" "$PASSWD_FILE"
[ "$(admin_credential_state)" = "unknown" ] \
    && ok "an unreadable credential state reads as 'unknown'" \
    || bad "with no shadow and no passwd the state read as '$(admin_credential_state)' (want unknown)"

echo "== the package no longer GENERATES a random credential"
if grep -q '^generate_admin_password()' "$ROOT/$SCRIPT"; then
    bad "generate_admin_password still exists — the package must never generate a random credential"
else
    ok "generate_admin_password is absent (the package sets a FIXED default, not a random one)"
fi

# ------------------------------------------- empty hash, no supplied password -> set the default
echo "== empty hash + no supplied password: the fixed default is set and the board is served"
seed_deployed_router
seed_shadow ''
reset_run
out=$(TOLLGATE_ADMIN_PASSWORD="" enforce_admin_credential 2>&1); rc=$?
if [ "$(admin_credential_state)" = "set" ]; then
    ok "a credential now backs the router"
else
    bad "the gate set NO credential (state: $(admin_credential_state))"
fi
[ "$(cat "$PASSWD_SEEN" 2>/dev/null)" = "$DEFAULT_CREDENTIAL" ] \
    && ok "passwd was given the fixed default ($DEFAULT_CREDENTIAL)" \
    || bad "passwd saw '$(cat "$PASSWD_SEEN" 2>/dev/null)' (want $DEFAULT_CREDENTIAL)"
[ -f "$ADMIN_PROVISIONAL_MARKER" ] \
    && ok "the provisional marker was written" \
    || bad "the provisional marker was NOT written"
[ "$rc" = "0" ] && ok "the gate reports success" || bad "the gate returned $rc on a successful default write"
assert_board_kept "empty hash -> default set"

# ------------------------------------------- empty hash + supplied env value -> env ignored, default set
echo "== a supplied TOLLGATE_ADMIN_PASSWORD is IGNORED (the fixed default wins)"
seed_deployed_router
seed_shadow ''
reset_run
out=$(TOLLGATE_ADMIN_PASSWORD="WillNotTake1234" enforce_admin_credential 2>"$TMP/err"); rc=$?
[ "$(cat "$PASSWD_SEEN" 2>/dev/null)" = "$DEFAULT_CREDENTIAL" ] \
    && ok "the env value never reached root (the fixed default did)" \
    || bad "the env value reached root ('$(cat "$PASSWD_SEEN" 2>/dev/null)')"
[ "$rc" = "0" ] && ok "the gate reports success" || bad "the gate returned $rc"
assert_board_kept "supplied env value, empty hash"

# ------------------------------------------- empty hash + staged file -> file ignored, default set
echo "== a staged password FILE is IGNORED (the fixed default wins)"
seed_deployed_router
seed_shadow ''
reset_run
printf '%s' 'StagedFile-9x' > "$TMP/admin-password"
out=$(ADMIN_PASSWORD_FILE="$TMP/admin-password" TOLLGATE_ADMIN_PASSWORD="" enforce_admin_credential 2>"$TMP/err"); rc=$?
[ "$(cat "$PASSWD_SEEN" 2>/dev/null)" = "$DEFAULT_CREDENTIAL" ] \
    && ok "the staged file never reached root (the fixed default did)" \
    || bad "the staged file reached root ('$(cat "$PASSWD_SEEN" 2>/dev/null)')"
[ -e "$TMP/admin-password" ] \
    && ok "the staged password file is left on disk (the package does not consume it)" \
    || bad "the package consumed a file it must not touch"
[ "$rc" = "0" ] && ok "the gate reports success" || bad "the gate returned $rc"
assert_board_kept "staged file, empty hash"
rm -f "$TMP/admin-password"

# ------------------------------------------- failed default write -> fail closed
echo "== empty hash + a FAILING passwd: the board is refused (fail closed)"
seed_deployed_router
seed_shadow ''
reset_run
PASSWD_FAILS=1
export PASSWD_FAILS
out=$(TOLLGATE_ADMIN_PASSWORD="" enforce_admin_credential 2>&1 || true)
[ "$(admin_credential_state)" = "empty" ] \
    && ok "the router stays credential-less (the failure is the one under test)" \
    || bad "the fixture did not stay credential-less (state: $(admin_credential_state))"
assert_board_dropped "empty hash + failed default write"
if printf '%s' "$out" | grep -q 'could not establish a root password'; then
    ok "the failure is surfaced on the install output"
else
    bad "the failure was not surfaced (got: $(printf '%s' "$out" | head -n 2 | tr '\n' ' '))"
fi
PASSWD_FAILS=0
export PASSWD_FAILS

# ------------------------------------------- unreadable state: fail closed too
echo "== unreadable credential state: the admin board is refused"
seed_deployed_router
rm -f "$SHADOW_FILE" "$PASSWD_FILE"
reset_run
out=$(enforce_admin_credential 2>"$TMP/err" || true)
assert_board_dropped "unreadable credential state"
if grep -q 'no readable root credential state' "$LOGFILE" 2>/dev/null; then
    ok "the log names the unreadable state"
else
    bad "the unreadable state was not recorded (log: $(head -n 3 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi

# ------------------------------------------- set hash: never touched
echo "== an existing credential is never reset"
seed_deployed_router
seed_shadow '$1$real$0123456789abcdef'
before="$(cat "$SHADOW_FILE")"
reset_run
out=$(enforce_admin_credential 2>"$TMP/err")
rc=$?
if [ "$(cat "$SHADOW_FILE")" = "$before" ]; then
    ok "the operator's existing root hash is byte-identical after the gate"
else
    bad "the existing root hash was rewritten — a reinstall would silently change the operator's password"
fi
[ "$(passwd_calls)" = "0" ] \
    && ok "passwd is never invoked on a router that already has a credential" \
    || bad "passwd was invoked $(passwd_calls) times although a credential already existed"
[ "$rc" = "0" ] && ok "the gate reports success for a set credential" || bad "the gate returned $rc for a set credential"
assert_board_kept "set hash"

# ------------------------------------------- locked account: never re-enabled
echo "== a deliberately locked root account is respected"
seed_deployed_router
seed_shadow '!'
before="$(cat "$SHADOW_FILE")"
reset_run
out=$(enforce_admin_credential 2>"$TMP/err")
if [ "$(cat "$SHADOW_FILE")" = "$before" ]; then
    ok "the locked account is left locked (password login stays refused)"
else
    bad "the gate re-enabled password auth on a deliberately locked account"
fi
[ "$(passwd_calls)" = "0" ] \
    && ok "passwd is never invoked on a locked account" \
    || bad "passwd was invoked $(passwd_calls) times on a locked account"
assert_board_kept "locked account"

# ------------------------------------------- a carried-over marker is re-stated
echo "== a provisional mark carried over from an older install is still honoured"
seed_deployed_router
seed_shadow '$6$generated$hash'
reset_run
mkdir -p "$(dirname "$ADMIN_PROVISIONAL_MARKER")"
printf 'default credential in use\n' > "$ADMIN_PROVISIONAL_MARKER"
out=$(TOLLGATE_ADMIN_PASSWORD="" enforce_admin_credential 2>"$TMP/err")
rc=$?
if grep -q 'not yet chosen by the operator' "$LOGFILE" 2>/dev/null; then
    ok "a carried-over marker re-states that the credential was not chosen"
else
    bad "a later run implies the credential was chosen by the operator"
fi
[ "$(passwd_calls)" = "0" ] \
    && ok "re-stating the recovery path does not touch the credential" \
    || bad "passwd was invoked $(passwd_calls) times while merely reporting state"
[ "$rc" = "0" ] \
    && ok "the gate reports success for a set credential carrying a marker" \
    || bad "the gate returned $rc for a set credential"
assert_board_kept "set hash + carried-over provisional mark"
rm -f "$ADMIN_PROVISIONAL_MARKER"

# ------------------------------------------------- the full-setup path too
echo "== the full-setup path runs the same gate"
if awk '/^log "Running full setup/{f=1} f && /^enforce_admin_credential$/{print "found"; exit}' "$ROOT/$SCRIPT" | grep -q found; then
    ok "the full-setup driver calls enforce_admin_credential in its uhttpd section"
else
    bad "the full-setup path does not call enforce_admin_credential — a first boot would leave an empty root hash behind the admin board"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
