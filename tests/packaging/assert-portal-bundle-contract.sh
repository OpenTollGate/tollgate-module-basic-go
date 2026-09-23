#!/usr/bin/env bash
#
# assert-portal-bundle-contract.sh - guard the captive-portal bundle the module
# ships inside the APK/IPK.
#
# The module does not compile the portal: packaging/portal-build.sh builds it
# from the revision pinned in packaging/build-inputs.json (.portal.commit) and
# stages the result into packaging/files/. Four failure modes are guarded here.
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
# CHECK C - the pinned portal sends the client MAC on the Lightning calls and
#           defines the strings it renders (#LN004)
#   Two defects shipped together and are guarded together, because advancing
#   the pin to pick up one fix (check A) can silently drop the other:
#
#     C1 the /ln-invoice handler identifies the client by its "mac" query
#        parameter and only falls back to an IP-derived lookup when that is
#        empty, but the portal sent it on NEITHER the invoice CREATE (POST)
#        nor the status POLL (GET). An invoice created without the MAC was
#        billed against whatever address the request appeared to come from,
#        and the poll could not match it back to the device the operator is
#        on - the Lightning payment step of the happy path fails (#LN004).
#
#     C2 the portal renders `LN003_*` / `LN004_*` error strings but
#        public/locales/en.json defined neither, so the user saw the literal
#        i18n key instead of a message.
#
#   Both are asserted against the pinned SOURCE (and, when a build is staged
#   here, reported against the staged bundle): the minified bundle keeps
#   `mac=${encodeURIComponent(` and the defined keys verbatim.
#
# CHECK D - the pinned portal's swap-fee pre-check passes real keyset OBJECTS
#           (the CU110 "token too small" gate)
#   src/helpers/mint-fee.js pre-checks a submitted token against the mint's swap
#   fee, so a token the fee consumes entirely gets a clear message instead of the
#   mint's opaque "no outputs provided" error. getDecodedToken(token, keysets)
#   wants MintKeyset OBJECTS as its second argument: cashu-ts maps a v2 SHORT
#   keyset id by reading `id.slice(0, proof.id.length)` off each entry. Handing
#   it a list of id STRINGS therefore made that read `undefined.slice` and throw
#
#     TypeError: Cannot read properties of undefined (reading 'slice')
#
#   on every real v4 short-keyset note (coinos.io, minibits). The helper's catch
#   swallowed the throw into { status: 0 } = "no pre-check", so the CU110 "token
#   too small" gate silently never fired and the under-funded token was
#   submitted anyway. Advancing the pin for another fix can silently drop this
#   one again - check A passes on the broken revision too - so the decode shape
#   is asserted here:
#
#     D1 the regressed shape is GONE - no list of keyset id strings is handed to
#        getDecodedToken;
#     D2 the fixed shape is PRESENT - the keysets given to getDecodedToken are
#        built as objects, which is what cashu-ts reads;
#     D3 the decode cannot fail SILENTLY - the fixed helper logs why it gave up
#        (`could not decode token proofs`), which is the string that made the
#        CU110 regression diagnosable instead of invisible.
#
#   Asserted against the pinned SOURCE; the staged-bundle counts for the two
#   diagnostic strings the fix introduces are reported in the informational
#   block below.
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

# ------------------------------------------------------- pin source helper ---
# Resolve a path inside the pinned portal tree: a local clone that already has
# the pin first, then a single shallow fetch of that SHA. Every check below
# reads the pin through this, so they cannot disagree about the revision.
# The helper runs in a command substitution, so where the content came from is
# recorded in a file rather than a variable.
PIN_SOURCE_ORIGIN=""
pin_tmp_dir=""
pin_origin_file="$(mktemp)"

pin_file() {  # $1 = path in the portal tree; prints the file (empty on failure)
  _path="$1"
  _content=""
  _origin=""
  if [ -n "$PORTAL_DIR" ] && git -C "$PORTAL_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    if git -C "$PORTAL_DIR" cat-file -e "${pin}^{commit}" 2>/dev/null; then
      _content="$(git -C "$PORTAL_DIR" show "${pin}:${_path}" 2>/dev/null)"
      [ -n "$_content" ] && _origin="$PORTAL_DIR (pin present locally)"
    fi
  fi
  if [ -z "$_content" ] && [ -n "$repo" ]; then
    if [ -z "$pin_tmp_dir" ]; then
      pin_tmp_dir="$(mktemp -d)"
      if git -C "$pin_tmp_dir" init -q 2>/dev/null &&
         git -C "$pin_tmp_dir" remote add origin "$repo" 2>/dev/null &&
         git -C "$pin_tmp_dir" fetch -q --depth 1 origin "$pin" 2>/dev/null; then
        :
      fi
    fi
    _content="$(git -C "$pin_tmp_dir" show "FETCH_HEAD:${_path}" 2>/dev/null)"
    [ -n "$_content" ] && _origin="$repo@$pin (shallow fetch)"
  fi
  [ -n "$_origin" ] && printf '%s' "$_origin" > "$pin_origin_file"
  printf '%s' "$_content"
}

# A pin whose tree is unreachable is unverifiable: strict in CI, a skip locally.
pin_unavailable() {  # $1 = what could not be read
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    fail "$1 at $pin (no local clone, fetch failed) - cannot verify the pin"
    return 1
  fi
  skip "$1 at $pin (offline and no local clone)"
  return 0
}

cashu_js="$(pin_file src/helpers/cashu.js)"
lightning_js="$(pin_file src/helpers/lightning.js)"
locales_json="$(pin_file public/locales/en.json)"
mint_fee_js="$(pin_file src/helpers/mint-fee.js)"
PIN_SOURCE_ORIGIN="$(cat "$pin_origin_file" 2>/dev/null)"

if [ -z "$cashu_js" ] && [ -z "$lightning_js" ] && [ -z "$locales_json" ] && [ -z "$mint_fee_js" ]; then
  pin_unavailable "any pinned portal source"
  [ -n "$pin_tmp_dir" ] && rm -rf "$pin_tmp_dir"
  rm -f "$pin_origin_file"
  echo
  echo "checks A, C and D: not verified"
  exit $(( failures > 0 ? 1 : 0 ))
fi

# ---------------------------------------------------------------- CHECK A ---
echo
echo "--- check A: pinned portal decode is keyset-agnostic ---"

if [ -z "$cashu_js" ]; then
  pin_unavailable "src/helpers/cashu.js"
else
  echo "source: $PIN_SOURCE_ORIGIN"
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
fi

# ---------------------------------------------------------------- CHECK C ---
echo
echo "--- check C: pinned portal sends the client MAC on the Lightning calls ---"
echo "            (and defines the LN003/LN004 strings it renders)"

if [ -z "$lightning_js" ]; then
  pin_unavailable "src/helpers/lightning.js"
else
  echo "source: $PIN_SOURCE_ORIGIN"
  mac_sent=1
  for marker in 'getClientMac' 'mac=${encodeURIComponent(' '?${mac}' '&${mac}'; do
    n="$(printf '%s' "$lightning_js" | grep -o -F -- "$marker" | wc -l | tr -d ' ')"
    echo "  $(printf '%-30s' "$marker") $n"
    [ "$n" -gt 0 ] || mac_sent=0
  done
  if [ "$mac_sent" -eq 1 ]; then
    echo "check C1: PASS - create (POST) and status poll (GET) both carry 'mac='"
  else
    fail "check C1: pinned $pin does not send the client MAC on every /ln-invoice call"
    echo "        The backend identifies the client by the 'mac' query parameter and"
    echo "        only falls back to an IP-derived lookup when it is empty. Without it"
    echo "        the invoice is billed against the address the request arrived from"
    echo "        and the status poll cannot match it back to the device the operator"
    echo "        is on, so the Lightning step of the happy path fails (#LN004)."
  fi
fi

if [ -z "$locales_json" ]; then
  pin_unavailable "public/locales/en.json"
else
  missing_keys=""
  for key in LN003_label LN003_message LN004_label LN004_message; do
    if printf '%s' "$locales_json" | grep -q "\"$key\""; then
      echo "  $(printf '%-16s' "\"$key\"") defined"
    else
      echo "  $(printf '%-16s' "\"$key\"") MISSING"
      missing_keys="$missing_keys $key"
    fi
  done
  if [ -z "$missing_keys" ]; then
    echo "check C2: PASS - the LN003/LN004 error strings exist"
  else
    fail "check C2: pinned $pin renders LN003/LN004 error codes but the locale does not define:$missing_keys"
    echo "        The portal then shows the literal i18n key instead of a message."
  fi
fi

# ---------------------------------------------------------------- CHECK D ---
echo
echo "--- check D: pinned portal's fee pre-check passes real keyset objects ---"
echo "            (the CU110 'token too small' swap-fee gate)"

if [ -z "$mint_fee_js" ]; then
  pin_unavailable "src/helpers/mint-fee.js"
else
  echo "source: $PIN_SOURCE_ORIGIN"
  fee_precheck_ok=1

  # D1 - the regressed shape must be GONE. cashu-ts resolves a v2 SHORT keyset id
  # by reading `id.slice(0, proof.id.length)` off every entry of the list, so a
  # list of id STRINGS throws
  #   TypeError: Cannot read properties of undefined (reading 'slice').
  for marker in 'keysetIds' 'keysets.map((keyset) => keyset.id)' 'getDecodedToken(trimmed, keysetIds)'; do
    n="$(printf '%s' "$mint_fee_js" | grep -o -F -- "$marker" | wc -l | tr -d ' ')"
    echo "  $(printf '%-52s' "old shape - $marker") $n"
    [ "$n" -eq 0 ] || fee_precheck_ok=0
  done

  # D2 - the fixed shape must be PRESENT: the keysets handed to the decoder are
  # built as objects ({ id, unit, input_fee_ppk }), which is what cashu-ts reads.
  for marker in 'mintKeysets.push({' 'getDecodedToken(token, keysets)'; do
    n="$(printf '%s' "$mint_fee_js" | grep -o -F -- "$marker" | wc -l | tr -d ' ')"
    echo "  $(printf '%-52s' "fixed shape - $marker") $n"
    [ "$n" -gt 0 ] || fee_precheck_ok=0
  done

  # D3 - the decode must not fail SILENTLY. The regression stayed invisible only
  # because the catch turned the throw into { status: 0 } = "no pre-check"; the
  # fixed helper logs the reason before giving up.
  n="$(printf '%s' "$mint_fee_js" | grep -o -F -- 'could not decode token proofs' | wc -l | tr -d ' ')"
  echo "  $(printf '%-52s' "fixed shape - could not decode token proofs") $n"
  [ "$n" -gt 0 ] || fee_precheck_ok=0

  if [ "$fee_precheck_ok" -eq 1 ]; then
    echo "check D: PASS - the fee pre-check decodes with real keyset objects"
  else
    fail "check D: pinned $pin pre-checks the swap fee with a decode that cannot work"
    echo "        src/helpers/mint-fee.js must hand getDecodedToken() a list of"
    echo "        MintKeyset OBJECTS. Passing id STRINGS makes cashu-ts read"
    echo "        'undefined.slice', which throws"
    echo "          TypeError: Cannot read properties of undefined (reading 'slice')"
    echo "        on every real v4 short-keyset note (coinos.io, minibits). The catch"
    echo "        then reports { status: 0 } = \"no pre-check\", so the CU110 \"token"
    echo "        too small\" gate never fires. Fix the fee pre-check in the portal,"
    echo "        bump .portal.commit to that revision and re-run"
    echo "        'bash packaging/portal-build.sh'."
  fi
fi

[ -n "$pin_tmp_dir" ] && rm -rf "$pin_tmp_dir"
rm -f "$pin_origin_file"

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
  # The LN004 markers DO discriminate: a bundle that never sends the MAC has no
  # `mac=${encodeURIComponent(` at all, so the count is the minimum-diff signal
  # for the #LN004 fix (asserted on the pin's source in check C, reported here).
  for marker in 'mac=${encodeURIComponent(' 'LN003_label' 'LN004_label'; do
    n="$(grep -o -F -- "$marker" "$found_asset" | wc -l | tr -d ' ')"
    echo "  $(printf '%-40s' "$marker") $n"
  done
  # The CU110 markers DO discriminate too: the regressed mint-fee.js contains
  # neither string, the fix introduces both, and string literals survive
  # minification. A 0 here means the staged bundle predates the fee pre-check fix
  # (asserted on the pin's source in check D, reported here).
  for marker in 'could not decode token proofs' 'token references unknown keyset'; do
    n="$(grep -o -F -- "$marker" "$found_asset" | wc -l | tr -d ' ')"
    echo "  $(printf '%-40s' "$marker") $n"
  done
else
  skip "no staged guest/admin JS asset carrying CU102 found"
fi

echo
if [ "$failures" -gt 0 ]; then
  echo "=== portal bundle contract: FAILED ($failures) ==="
  exit 1
fi
echo "=== portal bundle contract: OK ==="
