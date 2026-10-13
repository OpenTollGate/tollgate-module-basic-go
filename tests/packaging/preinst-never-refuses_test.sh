#!/usr/bin/env bash
# The package's `preinst` must be a NO-OP: it must never refuse an install.
#
# HISTORY: an earlier release refused to install on a router whose root account
# had no password (the admin board's login is checked against root's /etc/shadow
# hash, and rpcd's session.login accepts ANY password while that hash is empty).
# That fail-closed install was replaced by a FIXED default credential: the uci-
# defaults script sets root to `TollGate` when none exists and marks it
# provisional, so the operator is never locked out and the admin/config UI forces
# a change at first login. There is consequently nothing for the preinst to gate,
# and it is kept as a no-op so the control script keeps a stable shape across the
# opkg/apk lanes and offline/image builds.
#
# No router, no SDK. The preinst body is extracted from packaging/Makefile and
# run with SHADOW_FILE redirected into a temp dir.
#
# usage: bash tests/packaging/preinst-never-refuses_test.sh
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
if [ -s "$TMP/preinst" ]; then
    ok "extracted the preinst body from packaging/Makefile"
else
    echo "FAIL: could not extract a preinst body from packaging/Makefile" >&2
    exit 2
fi

echo "== the preinst no longer refuses on an empty root hash"
if grep -q 'refusing to install' "$TMP/preinst"; then
    bad "the preinst still carries the refusal the default-credential change removed"
else
    ok "the refusal text is gone from the preinst"
fi

run() { # run <shadow-line> [env-assignments...]
    local shadow="$1"; shift
    printf '%s\n' "$shadow" > "$TMP/shadow"
    env SHADOW_FILE="$TMP/shadow" "$@" sh "$TMP/preinst"
    return $?
}

echo "== every root state is allowed (the preinst only exits 0) =="
run 'root::0:0:99999:7:::' >/dev/null 2>&1 \
    && ok "empty hash -> exit 0 (no refusal; the uci-defaults set the default)" \
    || bad "empty hash -> non-zero (the refusal is back)"
run 'root:$1$abc$0123456789abcdef:0:0:99999:7:::' >/dev/null 2>&1 \
    && ok "a set hash -> exit 0" || bad "a set hash was refused"
run 'root:!:0:0:99999:7:::' >/dev/null 2>&1 && ok "locked (!) -> exit 0" || bad "locked (!) was refused"
run 'root:*:0:0:99999:7:::' >/dev/null 2>&1 && ok "locked (*) -> exit 0" || bad "locked (*) was refused"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
