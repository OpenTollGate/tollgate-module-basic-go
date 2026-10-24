#!/usr/bin/env bash
# Offline test for the GPL license agreement across the release lanes (#751).
#
# The two release lanes disagreed on shipping the GPL-3.0 text: the SDK-free
# .ipk lanes installed it at /usr/share/doc/tollgate-wrt/LICENSE while the
# SDK .apk lane (packaging/Makefile) was metadata-only — same version,
# different contents, and the single content delta blocking byte-equivalence.
#
# The agreement, decided on compliance grounds: BOTH formats ship the text
# (GPL-3.0 distribution reaches the recipient with the binary; ~35 KB is
# noise on any router), at the SAME payload path. This suite pins every
# staging surface — the recipe parse follows
# tests/packaging/package-nodogsplash-dependency_test.sh (parse the recipe,
# not the comment describing it) — and fails when any lane drops the text,
# moves it, or makes its presence best-effort again.
#
# The built-artifact half of the invariant lives in
# tests/packaging/assert-artifact-contents.sh, which hard-fails any package
# (either format) whose payload lacks the file.
#
# Usage: bash tests/packaging/license-both-lanes_test.sh

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

MAKEFILE="packaging/Makefile"
IPK_SH="packaging/local-build-ipk.sh"
CI_YML=".github/workflows/build-package.yml"
PAYLOAD_PATH="usr/share/doc/tollgate-wrt/LICENSE"

echo "== every staging surface ships the GPL text at the same payload path"

# 1. The SDK .apk recipe installs the license into the payload.
if grep -qE '^\s*\$\(INSTALL_DATA\)\s+\$\(PKG_BUILD_DIR\)/LICENSE\s+\$\(1\)/usr/share/doc/\$\(PKG_NAME\)/LICENSE\s*$' "$MAKEFILE"; then
    ok "packaging/Makefile installs the license into the payload"
else
    bad "packaging/Makefile no longer INSTALL_DATAs \$(PKG_BUILD_DIR)/LICENSE to /usr/share/doc/\$(PKG_NAME)/LICENSE — the .apk lane dropped the GPL text (#751)"
fi

# 2. The same recipe REQUIRES the license at prepare time — best-effort
#    (`|| true`) staging can silently build a GPL binary without its text.
if grep -q 'ERROR: LICENSE not found' "$MAKEFILE" && ! grep -qE 'LICENSE.*\|\| true' "$MAKEFILE"; then
    ok "packaging/Makefile fails loudly when LICENSE is missing (no best-effort copy)"
else
    bad "packaging/Makefile stages LICENSE best-effort again — a GPL-3.0 build must fail, not silently ship without the text"
fi

# 3. The SDK-free .ipk lane installs it.
if grep -qF 'install -D -m 0644 LICENSE "$PAYLOAD/usr/share/doc/${PKG_NAME}/LICENSE"' "$IPK_SH"; then
    ok "packaging/local-build-ipk.sh installs the license at /$PAYLOAD_PATH (PKG_NAME resolves to tollgate-wrt)"
else
    bad "packaging/local-build-ipk.sh no longer installs the license at /$PAYLOAD_PATH"
fi

# 4. The CI .ipk lane installs it.
if grep -q "usr/share/doc/\${PACKAGE_NAME}/LICENSE" "$CI_YML"; then
    ok "the CI ipk lane (.github/workflows/build-package.yml) installs the license"
else
    bad "the CI ipk lane dropped the license install"
fi

# 5. Path agreement: the Makefile's destination (with PKG_NAME=tollgate-wrt)
#    and the shell lanes' destination are the same string.
makefile_dest="$(grep -oE '\$\(1\)/usr/share/doc/\$\(PKG_NAME\)/LICENSE' "$MAKEFILE" | head -1 | sed -e 's|\$(1)/||' -e 's|\$(PKG_NAME)|tollgate-wrt|')"
if [ "$makefile_dest" = "$PAYLOAD_PATH" ]; then
    ok "both lanes resolve to the same payload path: /$PAYLOAD_PATH"
else
    bad "payload paths diverge: Makefile '$makefile_dest' vs shell lanes '$PAYLOAD_PATH' — same version would ship different contents again (#751)"
fi

# 6. The metadata half stays: PKG_LICENSE still declares GPL-3.0-only (the
#    feed's license manifest is metadata AND the text — never either/or).
if grep -qE '^PKG_LICENSE:=GPL-3.0-only' "$MAKEFILE" && grep -qE '^PKG_LICENSE_FILES:=LICENSE' "$MAKEFILE"; then
    ok "PKG_LICENSE / PKG_LICENSE_FILES metadata intact alongside the shipped text"
else
    bad "packaging/Makefile dropped its license metadata — the text and the metadata are complementary, not alternatives"
fi

# 7. The artifact harness hard-fails a package without the file: the
#    built-artifact half must not be quietly downgraded to informational.
if grep -q 'usr/share/doc/tollgate-wrt/LICENSE' tests/packaging/assert-artifact-contents.sh && \
   awk '/LICENSE_PAYLOAD=/{found=NR} found && /exit 1/ && NR-found < 12 {ok=1; exit} END {exit !ok}' tests/packaging/assert-artifact-contents.sh; then
    ok "assert-artifact-contents.sh hard-fails packages missing the license"
else
    bad "assert-artifact-contents.sh no longer hard-fails on a missing license — the compliance gate went soft"
fi

echo
echo "== license-both-lanes: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
