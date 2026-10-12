#!/usr/bin/env bash
# The package's `preinst` must REFUSE to install on a router whose root account
# has NO password, BEFORE any file is unpacked.
#
# WHY: the :8090 admin board's login is checked against root's /etc/shadow hash,
# and rpcd's session.login accepts ANY password while that hash is empty. The
# package never sets a credential, so a password-less install would leave an
# unauthenticatable board. Failing the install loudly (preinst, not postinst)
# means no half-installed package — the operator sets a password and retries.
#
# No router, no SDK. The preinst body is extracted from packaging/Makefile and
# run with SHADOW_FILE redirected into a temp dir.
#
# usage: bash tests/packaging/preinst-refuses-empty-root_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MK="$ROOT/packaging/Makefile"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Extract the preinst body (between the define/endef) and un-escape Make's $$ -> $.
awk 'index($0,"define Package/$(PKG_NAME)/preinst")==1{f=1;next} f&&index($0,"endef")==1{exit} f' "$MK" \
    | sed 's/\$\$/$/g' > "$TMP/preinst"
if [ ! -s "$TMP/preinst" ] || ! grep -q 'refusing to install' "$TMP/preinst"; then
    echo "FAIL: could not extract a refusing preinst body from packaging/Makefile" >&2
    exit 2
fi
ok "extracted the preinst body from packaging/Makefile"

run() { # run <shadow-line> [env-assignments...]
    local shadow="$1"; shift
    printf '%s\n' "$shadow" > "$TMP/shadow"
    env SHADOW_FILE="$TMP/shadow" "$@" sh "$TMP/preinst"
    return $?
}

echo "== empty root hash: the install is REFUSED =="
out="$(run 'root::0:0:99999:7:::' 2>&1)"; rc=$?
[ "$rc" = 1 ] && ok "empty hash -> exit 1 (refused)" || bad "empty hash -> exit $rc (want 1)"
printf '%s' "$out" | grep -q 'root has no password' && ok "refusal names the reason" || bad "refusal text missing: $out"
printf '%s' "$out" | grep -q 'passwd root' && ok "refusal tells the operator how to fix it" || bad "refusal lacks the fix"

echo "== a set root hash: allowed =="
run 'root:$1$abc$0123456789abcdef:0:0:99999:7:::' >/dev/null 2>&1 \
    && ok "a set hash -> exit 0" || bad "a set hash was refused"

echo "== a deliberately LOCKED root (or unreadable shadow): allowed =="
echo "   (locked is a real password state, respected by the admin gate)"
run 'root:!:0:0:99999:7:::' >/dev/null 2>&1 && ok "locked (!) -> exit 0" || bad "locked (!) was refused"
run 'root:*:0:0:99999:7:::' >/dev/null 2>&1 && ok "locked (*) -> exit 0" || bad "locked (*) was refused"

echo "== bypasses =="
run 'root::0:0:99999:7:::' IPKG_INSTROOT=/ >/dev/null 2>&1 \
    && ok "IPKG_INSTROOT (offline/image build) -> exit 0" || bad "IPKG_INSTROOT did not bypass"
run 'root::0:0:99999:7:::' TOLLGATE_ALLOW_EMPTY_ROOT=1 >/dev/null 2>&1 \
    && ok "TOLLGATE_ALLOW_EMPTY_ROOT=1 (CI hatch) -> exit 0" || bad "the CI hatch did not bypass"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
