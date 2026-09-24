#!/usr/bin/env bash
# Offline test for the invariant that the :8090 admin board is never left
# standing on a router whose root credential is UNSET. No router, no SDK, no
# network.
#
# The real defect this guards: the board's only login endpoint is rpcd's
# session.login, and the distribution's /etc/config/rpcd maps that login's
# password to root's shadow hash (`password '$p$root'` ->
# getspnam("root")->sp_pwdp). rpcd's rpc_login_test_password() begins with
# `if (!hash || !*hash) return true;` — an EMPTY hash authenticates ANY
# password, including the empty string. A stock first-boot rootfs has exactly
# that empty hash, so a freshly packaged router handed out a root session to
# whoever reached the listener, and that session reaches the board's ACL
# (`file:["exec"]`, `system:["password_set"]`, `tollgate wallet_drain_cashu`):
# root command execution, a root password change, or the operator's money,
# with no credential at all.
#
# Reachability is covered elsewhere (the pre-auth allowance and the br-lan
# packet-filter drop). This test covers the credential half: the shipped
# packaging must FAIL CLOSED — establish a credential when there is none
# (surfaced to the operator exactly once), and refuse to leave an admin
# listener standing when no credential can be established or verified.
#
# It reuses the repo's existing packaging tier instead of inventing a harness:
# the unit assertions source the shipped script's functions with
# TOLLGATE_SETUP_LIB_ONLY=1 against a fake `uci`, a fake `passwd` and a fake
# /etc/shadow (the tests/uci-defaults-band_test.sh seam), and the integration
# assertion RUNS the shipped driver on the same-version path exactly as
# tests/uci-defaults-same-version-allowlist_test.sh does — a gate that no
# driver calls is not a control.
#
# Usage: bash tests/packaging/admin-board-requires-credential_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# --------------------------------------------------------------- fake apk
# The shipped script carries the __TOLLGATE_VERSION__ placeholder, so a source
# run resolves the setup version from the package manager.
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
# Flat-file stand-in: one "key=value" line per option. del_list removes every
# line whose value matches EXACTLY (entries contain spaces, so the match is
# whole-line and never word-split).
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
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
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
# never changed" case the gate must catch.
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
export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
# Only the absolute paths into the live system are redirected: the flag file,
# the log, and the two credential sources. Everything else — the same-version
# driver branch, the gate itself — is the shipped code, run by /bin/sh.
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
export LOGFILE
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
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

# ------------------------------------------------------------- assertions
ADMIN_HTTP="uhttpd.admin.listen_http=0.0.0.0:8090"
ADMIN_HTTP6="uhttpd.admin.listen_http=[::]:8090"
ADMIN_HTTPS="uhttpd.admin.listen_https=0.0.0.0:8443"
ADMIN_HTTPS6="uhttpd.admin.listen_https=[::]:8443"
CFGUI_HTTP="uhttpd.net4sats.listen_http=0.0.0.0:8090"
MAIN_STRAY="uhttpd.main.listen_http=0.0.0.0:8090"
MAIN_LUCI="uhttpd.main.listen_http=0.0.0.0:8080"

count_state() { grep -F -c -- "$1" "$UCI_STATE" 2>/dev/null || true; }
has_state()   { grep -F -q -- "$1" "$UCI_STATE" 2>/dev/null; }

# A deployed router: the portal-staged :8090 board, this module's own :8090
# configUI writer, a historical stray :8090 on uhttpd.main, and LuCI's :8080.
seed_deployed_router() {
    : > "$UCI_STATE"
    {
        printf '%s\n' \
            'uhttpd.admin=uhttpd' "$ADMIN_HTTP" "$ADMIN_HTTP6" "$ADMIN_HTTPS" "$ADMIN_HTTPS6" \
            'uhttpd.net4sats=uhttpd' "$CFGUI_HTTP" 'uhttpd.net4sats.listen_http=[::]:8090' \
            'uhttpd.main=uhttpd' "$MAIN_LUCI" "$MAIN_STRAY"
    } >> "$UCI_STATE"
}

# assert_board_dropped <label> — every admin listener is gone, the customer
# journey is intact.
assert_board_dropped() {
    local label="$1" gone=1 entry
    for entry in "$ADMIN_HTTP" "$ADMIN_HTTP6" "$ADMIN_HTTPS" "$ADMIN_HTTPS6" "$CFGUI_HTTP" "$MAIN_STRAY"; do
        if has_state "$entry"; then
            gone=0
            bad "$label: $entry is still configured — the board is served behind an empty root hash"
        fi
    done
    [ "$gone" = 1 ] && ok "$label: every :8090/:8443 admin listener was dropped"
    if has_state "$MAIN_LUCI"; then
        ok "$label: LuCI's :8080 listener was left alone"
    else
        bad "$label: LuCI's :8080 listener was removed (the gate must not touch non-admin surfaces)"
    fi
}

# assert_board_kept <label> — a credential exists, so the board stays.
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
    PASSWD_FAILS=0
    export PASSWD_FAILS
}

passwd_calls() { grep -c . "$PASSWD_LOG" 2>/dev/null || true; }

# ----------------------------------------------------- the driver calls it
# A gate no driver calls is not a control: this runs the SHIPPED driver on the
# same-version path (the path every reinstall/upgrade takes) with the fake
# uci/passwd/shadow in place. It comes first so the RED output names the
# defect at the level the operator is exposed to, not just as a missing
# symbol.
run_same_version() {
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >"$TMP/run.out" 2>"$TMP/run.err"
    return $?
}

echo "== same-version reinstall with an empty hash and a failing passwd"
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
if grep -q 'Flag matches' "$LOGFILE" 2>/dev/null; then
    ok "the same-version branch was the path taken"
else
    bad "the same-version branch was not taken (log: $(head -n 2 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
assert_board_dropped "same-version reinstall, no credential"

echo "== same-version reinstall with an empty hash and a working passwd"
seed_deployed_router
seed_shadow ''
reset_run
run_same_version
# The hash is read straight out of the shadow fixture — the assertion must not
# depend on the function it is checking.
root_hash="$(awk -F: '$1 == "root" { print $2; exit }' "$SHADOW_FILE" 2>/dev/null || true)"
if [ -n "$root_hash" ]; then
    ok "the driver established a credential on the credential-less router"
else
    bad "the driver left the router credential-less — an empty root hash behind the :8090 admin board, which rpcd authenticates ANY password against"
fi
assert_board_kept "same-version reinstall, credential established"

# Source every function from the (path-rewritten) shipped script without
# running the setup — the same seam tests/uci-defaults-band_test.sh uses.
TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT_UNDER_TEST" 2>/dev/null
if ! command -v admin_credential_state >/dev/null 2>&1; then
    echo "FAIL the shipped setup script exposes no admin credential gate (admin_credential_state)" >&2
    echo "     => an empty root hash is served as an admin board: rpcd authenticates ANY password against it" >&2
    exit 1
fi
ok "setup script sources in lib-only mode and exposes the credential gate"

# ------------------------------------------------------- credential states
echo "== the credential state is read from root's hash, not assumed"
seed_shadow '$1$abc$0123456789abcdef'
[ "$(admin_credential_state)" = "set" ] \
    && ok "a real hash reads as 'set'" \
    || bad "a real hash read as '$(admin_credential_state)' (want set) — an established credential would be treated as missing"

seed_shadow ''
[ "$(admin_credential_state)" = "empty" ] \
    && ok "an empty hash reads as 'empty' (the state rpcd authenticates ANY password against)" \
    || bad "an empty hash read as '$(admin_credential_state)' (want empty) — the vulnerability's own state is not detected"

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
    || bad "with no shadow and no passwd file the state read as '$(admin_credential_state)' (want unknown) — an unverifiable router would be assumed safe"

echo "== the generated credential is strong and shell-safe"
seed_shadow ''
pw_probe="$(generate_admin_password 2>/dev/null || true)"
ALPHABET="abcdefghijkmnpqrstuvwxyz23456789"  # pragma: allowlist secret
if [ "${#pw_probe}" = "20" ]; then
    ok "generated credential is 20 characters"
else
    bad "generated credential is ${#pw_probe} characters (want 20)"
fi
if [ -n "$pw_probe" ] && [ -z "${pw_probe//[$ALPHABET]/}" ]; then
    ok "generated credential uses only the look-alike-free alphabet"
else
    bad "generated credential '$pw_probe' uses characters outside [$ALPHABET] — look-alikes or shell-hostile characters"
fi
if [ "$(generate_admin_password)" != "$(generate_admin_password)" ]; then
    ok "two generations differ (the value is not a constant)"
else
    bad "generate_admin_password returned the same value twice"
fi

# ------------------------------------------- empty hash: establish it, keep the board
echo "== empty hash + a working passwd: a credential is established and shown once"
seed_deployed_router
seed_shadow ''
reset_run
out=$(enforce_admin_credential 2>"$TMP/err")
rc=$?

if [ "$(admin_credential_state)" = "set" ]; then
    ok "a credential was established on the credential-less router"
else
    bad "no credential was established (root hash is still '$(admin_credential_state)') — the deploy leaves an empty root hash behind the :8090 admin board, which rpcd authenticates ANY password against"
fi
[ "$(passwd_calls)" = "1" ] \
    && ok "passwd was invoked exactly once" \
    || bad "passwd was invoked $(passwd_calls) times (want 1)"
if [ "$rc" = "0" ]; then
    ok "enforce_admin_credential reports success once the credential is verified"
else
    bad "enforce_admin_credential returned $rc although the credential took (stderr: $(head -n 2 "$TMP/err" | tr '\n' ' '))"
fi

credential="$(cat "$PASSWD_SEEN" 2>/dev/null || true)"
if [ -n "$credential" ]; then
    ok "the credential that was applied is observable"
    shown_out=$(printf '%s\n' "$out" | grep -F -c -- "$credential" || true)
    [ "$shown_out" = "1" ] \
        && ok "the credential is shown exactly once on the install output" \
        || bad "the credential appears $shown_out times on the install output (want exactly 1 — the operator must be able to read it, and it must not be re-printed)"
    shown_log=$(grep -F -c -- "$credential" "$LOGFILE" 2>/dev/null || true)
    [ "$shown_log" = "1" ] \
        && ok "the credential is recorded exactly once in the root-only setup log" \
        || bad "the credential appears $shown_log times in the setup log (want exactly 1)"
    if grep -F -q -- "$credential" "$UCI_STATE" 2>/dev/null; then
        bad "the credential landed in UCI state (world-readable /etc/config)"
    else
        ok "the credential never reaches UCI (/etc/config is world-readable)"
    fi
else
    bad "no credential was applied to the router"
fi

if grep -q 'generated one for the admin board' "$LOGFILE" 2>/dev/null; then
    ok "the log names the generated credential (operator finds it after an unattended install)"
else
    bad "the log does not record that a credential was generated (log: $(head -n 3 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
assert_board_kept "empty hash + working passwd"

# ------------------------------------------- empty hash: passwd does not take -> fail closed
echo "== empty hash + a passwd that does not take: the admin board is refused"
seed_deployed_router
seed_shadow ''
reset_run
PASSWD_FAILS=1
export PASSWD_FAILS
# Both streams: the refusal is an error (stderr) and the operator sees both on
# the install console.
out=$(enforce_admin_credential 2>&1 || true)

if [ "$(admin_credential_state)" = "empty" ]; then
    ok "the router still has no credential (the failure is the one under test)"
else
    bad "the fixture did not stay credential-less (state: $(admin_credential_state))"
fi
assert_board_dropped "empty hash + failed passwd"
if printf '%s' "$out" | grep -q 'no usable password'; then
    ok "the refusal names the reason on the install output"
else
    bad "the refusal was not surfaced on the install output (got: $(printf '%s' "$out" | head -n 2 | tr '\n' ' '))"
fi
if grep -q 'refusing to serve the :8090 admin board' "$LOGFILE" 2>/dev/null; then
    ok "the refusal is recorded in the setup log"
else
    bad "the log does not record the refusal (log: $(head -n 3 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
fi
if grep -q 'passwd root' "$LOGFILE" 2>/dev/null; then
    ok "the log tells the operator how to undo the refusal (passwd root)"
else
    bad "the log does not tell the operator how to restore the board"
fi

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
if printf '%s' "$out" | grep -q '[a-z0-9]\{20\}'; then
    bad "the gate printed a credential although none was generated"
else
    ok "nothing credential-shaped is printed when no credential is generated"
fi
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

# ------------------------------------------------- the full-setup path too
# The full setup path is not executed here: it writes /etc/profile, the kernel
# hostname and sysctls, which an offline test must not touch. The call site is
# asserted instead (the same shape PR #546's test uses for its second writer).
echo "== the full-setup path runs the same gate"
if awk '/^# Full setup \(first boot or version change\)$/{f=1} f && /^enforce_admin_credential$/{print "found"; exit}' "$ROOT/$SCRIPT" | grep -q found; then
    ok "the full-setup driver calls enforce_admin_credential right after setup_uhttpd_configui"
else
    bad "the full-setup path does not call enforce_admin_credential — a first boot would leave an empty root hash behind the :8090 admin board"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
