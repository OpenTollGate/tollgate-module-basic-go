#!/usr/bin/env bash
#
# assert-portal-bundle-contract.sh - guard the captive-portal bundle the module
# ships inside the APK/IPK.
#
# The module does not compile the portal: packaging/portal-build.sh builds it
# from the revision pinned in packaging/build-inputs.json (.portal.commit) and
# stages the result into packaging/files/. Two failure modes are guarded here.
#
# CHECK A - pinned portal decodes Cashu tokens without a keyset list
#   The token-validation path must be keyset-agnostic (cashu-ts
#   getTokenMetadata / equivalent). A decode call that needs a MintKeyset list
#   - `getDecodedToken(token)` with no second argument - throws on every v4
#   (cashuB) token that carries a SHORT keyset id, which is what
#   coinos/minibits hand out:
#
#     getDecodedToken(v4Token)  -> "A short keyset ID v2 was encountered, but
#                                   got no keysets to map it to."
#     getTokenMetadata(v4Token) -> OK
#
#   reproduced against the pinned dependency (@cashu/cashu-ts 2.9.0) with the
#   coinos v4 fixture the portal itself uses in tests/unit/mint-fee.test.js.
#   The portal surfaces that failure to the user as #CU102, so a release pin
#   with this decode must not ship.
#
#   NOTE: the two strings above are cashu-ts internals. They are present in
#   bundles built from BOTH the regressed and the fixed portal revisions (the
#   bundler preserves the strings, not the identifiers), so grepping a built
#   bundle for them cannot be used as the gate - the pinned SOURCE is checked
#   instead. A minimum-diff marker for the fixed revision is that its bundle
#   also carries the getTokenMetadata call the fix introduces (verified: the
#   regressed bundle has 0, this one has 1) - reported below, not asserted.
#
# CHECK B - the committed bundle matches what the pin actually builds
#   The tracked part of packaging/files/tollgate-captive-portal-site/ (and
#   tollgate-admin/) is a checked-in copy of the pinned build output; the
#   hashed JS assets are gitignored and rebuilt from the pin. If a build has
#   been staged in this tree (run `bash packaging/portal-build.sh` first), the
#   staged bytes must come from the pinned commit and the tracked copy must
#   still match them, i.e. `git status` must be clean inside those two dirs.
#
# Usage:  bash tests/packaging/assert-portal-bundle-contract.sh [--portal-dir DIR]
#         PORTAL_DIR=/path/to/tollgate-captive-portal-site (or --portal-dir)
#         skips the fetch when the local clone already has the pin.
#
# Exit codes: 0 pass (or skipped), 1 contract violation.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_INPUTS="$ROOT/packaging/build-inputs.json"
GUEST_DIR="packaging/files/tollgate-captive-portal-site"
ADMIN_DIR="packaging/files/tollgate-admin"

PORTAL_DIR="${PORTAL_DIR:-}"
# packaging/portal-build.sh checks the pinned portal out here by default, so a
# tree that just ran it (CI's build-portal job, a local build) can be verified
# without refetching.
if [ -z "$PORTAL_DIR" ] && [ -d /tmp/tollgate-captive-portal-site/.git ]; then
  PORTAL_DIR=/tmp/tollgate-captive-portal-site
fi
while [ $# -gt 0 ]; do
  case "$1" in
    --portal-dir) PORTAL_DIR="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }
warn() { echo "WARN: $*"; }
skip() { echo "SKIP: $*"; }

for tool in jq git; do
  command -v "$tool" >/dev/null 2>&1 || { skip "$tool is not available"; exit 0; }
done

[ -f "$BUILD_INPUTS" ] || { skip "$BUILD_INPUTS not found"; exit 0; }

pin="$(jq -r '.portal.commit // empty' "$BUILD_INPUTS")"
repo="$(jq -r '.portal.repo // empty' "$BUILD_INPUTS")"
[ -n "$pin" ] || { skip "no .portal.commit in $BUILD_INPUTS"; exit 0; }

echo "=== portal bundle contract ==="
echo "pin  : $pin"
echo "repo : $repo"

# ---------------------------------------------------------------- CHECK A ---
echo
echo "--- check A: pinned portal decode is keyset-agnostic ---"

show_source() {  # $1 = git dir, $2 = path in tree
  git -C "$1" show "$2:src/helpers/cashu.js" 2>/dev/null
}

cashu_js=""
source_origin=""
if [ -n "$PORTAL_DIR" ] && git -C "$PORTAL_DIR" rev-parse --git-dir >/dev/null 2>&1; then
  if git -C "$PORTAL_DIR" cat-file -e "${pin}^{commit}" 2>/dev/null; then
    cashu_js="$(show_source "$PORTAL_DIR" "$pin")"
    source_origin="$PORTAL_DIR (pin present locally)"
  fi
fi

tmp_dir=""
if [ -z "$cashu_js" ] && [ -n "$repo" ]; then
  tmp_dir="$(mktemp -d)"
  if git -C "$tmp_dir" init -q 2>/dev/null &&
     git -C "$tmp_dir" remote add origin "$repo" 2>/dev/null &&
     git -C "$tmp_dir" fetch -q --depth 1 origin "$pin" 2>/dev/null; then
    cashu_js="$(git -C "$tmp_dir" show "FETCH_HEAD:src/helpers/cashu.js" 2>/dev/null)"
    source_origin="$repo@$pin (shallow fetch)"
  fi
fi

if [ -z "$cashu_js" ]; then
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    fail "could not obtain src/helpers/cashu.js at $pin (no local clone, fetch failed) - cannot verify the pin"
  else
    skip "could not obtain src/helpers/cashu.js at $pin (offline and no local clone)"
  fi
  [ -n "$tmp_dir" ] && rm -rf "$tmp_dir"
  echo
  echo "check A: not verified"
  exit $(( failures > 0 ? 1 : 0 ))
fi
[ -n "$tmp_dir" ] && rm -rf "$tmp_dir"

echo "source: $source_origin"
keyset_agnostic=0
if printf '%s' "$cashu_js" | grep -q 'getTokenMetadata'; then
  keyset_agnostic=1
  echo "  getTokenMetadata present (lines: $(printf '%s' "$cashu_js" | grep -n 'getTokenMetadata' | cut -d: -f1 | paste -sd, -))"
else
  echo "  getTokenMetadata absent"
fi
if printf '%s' "$cashu_js" | grep -q 'getDecodedToken('; then
  echo "  getDecodedToken call sites (lines: $(printf '%s' "$cashu_js" | grep -n 'getDecodedToken(' | cut -d: -f1 | paste -sd, -))"
fi

if [ "$keyset_agnostic" -eq 1 ]; then
  echo "check A: PASS - pinned portal decodes tokens without a keyset list"
else
  fail "check A: pinned $pin validates tokens with a keyset-requiring decode (getTokenMetadata absent)"
  echo "        Real v4 (cashuB) tokens carry short keyset ids; decoding them without"
  echo "        a MintKeyset list raises \"A short keyset ID v2 was encountered, but"
  echo "        got no keysets to map it to\", which the portal reports as #CU102."
  echo "        Fix the decode in the portal, bump .portal.commit to that revision"
  echo "        and re-run 'bash packaging/portal-build.sh'."
fi

# ---------------------------------------------------------------- CHECK B ---
echo
echo "--- check B: committed bundle matches the pin ---"

if [ ! -f "$ROOT/packaging/portal-build-inputs.json" ] || [ ! -d "$ROOT/$GUEST_DIR/assets" ]; then
  skip "no staged portal build in this tree (run 'bash packaging/portal-build.sh' to enable check B)"
else
  staged_pin="$(jq -r '.portal_commit // empty' "$ROOT/packaging/portal-build-inputs.json")"
  echo "staged build portal_commit: ${staged_pin:-<unset>}"
  if [ "$staged_pin" != "$pin" ]; then
    fail "check B: staged bundle was built from ${staged_pin:-<unset>} but the pin is $pin (non-reproducible build)"
  fi

  entry="$(ls "$ROOT/$GUEST_DIR/assets"/index-*.js 2>/dev/null | head -1)"
  if [ -n "$entry" ]; then
    echo "guest entry: ${entry#"$ROOT"/} $(sha256sum "$entry" | cut -c1-16) ($(wc -c < "$entry") bytes)"
  fi

  if git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    drift="$(git -C "$ROOT" status --porcelain --untracked-files=all -- "$GUEST_DIR" "$ADMIN_DIR" 2>/dev/null)"
    if [ -n "$drift" ]; then
      fail "check B: the committed portal bundle does not match what the pin builds:"
      printf '%s\n' "$drift" | sed 's/^/        /' >&2
      echo "        commit the regenerated copy from 'bash packaging/portal-build.sh'."
    else
      echo "check B: PASS - tracked bundle matches the pinned build output"
    fi
  else
    skip "not a git worktree - cannot compare the tracked bundle"
  fi
fi

# ------------------------------------------------------------ informational --
echo
echo "--- informational: decode markers in the staged bundle ---"
found_asset=""
for d in "$ROOT/$GUEST_DIR/assets" "$ROOT/$ADMIN_DIR/assets"; do
  [ -d "$d" ] || continue
  for f in "$d"/*.js; do
    [ -f "$f" ] || continue
    if grep -q 'CU102' "$f" 2>/dev/null; then
      found_asset="$f"
      break 2
    fi
  done
done
if [ -n "$found_asset" ]; then
  echo "asset: ${found_asset#"$ROOT"/}"
  for marker in 'short keyset ID v2 was encountered' 'no keysets to map it to' 'getTokenMetadata' 'proofAmounts'; do
    n="$(grep -o -F -- "$marker" "$found_asset" | wc -l | tr -d ' ')"
    echo "  $(printf '%-40s' "$marker") $n"
  done
  echo "  (the two keyset strings are cashu-ts internals and also appear in bundles"
  echo "   built from the fixed revisions - do not use them as a gate)"
else
  skip "no staged guest/admin JS asset carrying CU102 found"
fi

echo
if [ "$failures" -gt 0 ]; then
  echo "=== portal bundle contract: FAILED ($failures) ==="
  exit 1
fi
echo "=== portal bundle contract: OK ==="
