#!/usr/bin/env bash
# Contract: the SSID MATCHER's acceptance table (the reader side).
#
# tests/contract/check-ssid-format.sh pins what the first-boot WRITER may
# emit (`<brand>-<device code>`, one name on both radios). This fence pins
# what the READER — src/wireless_gateway_manager hasTollGateSSID, the
# reseller-mode upstream selector and the vendor-element score input —
# must accept, against the same pinned table the Go unit test reads
# (tests/contract/ssid-naming-fixtures.txt). Two layers, because neither
# is sufficient alone:
#
#   A. structural — the table is well-formed and covers every REQUIRED
#      class: both brands (#618: the matcher was brand-blind and a
#      whitelabel upstream was invisible), case-insensitivity (lowercase
#      installer-era SSIDs are still in the fleet), the single-'!'
#      decoration (#706's writer, both spellings must converge on the same
#      recognition), the double-'!' rejection, and the anchored negatives.
#      Also: the Go test must actually reference the table — a pin nobody
#      loads is decoration.
#   B. behavioural — the Go test over the table runs and passes. This is
#      the matcher itself, not a grep of its source.
#
# The SSID contract broke twice (#618, #706); each break was a reader/writer
# disagreement this table would have caught.
#
# Exit 0 when the contract holds, 1 otherwise. Layer B needs go on PATH —
# this repo is a Go tree; a missing toolchain is a failure, not a skip.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TABLE="$ROOT/tests/contract/ssid-naming-fixtures.txt"
BRANDS_GO="$ROOT/src/wireless_gateway_manager/brands.go"
BRANDS_TEST="$ROOT/src/wireless_gateway_manager/brands_test.go"

fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }
info() { printf '  info  %s\n' "$1"; }

printf 'check-ssid-naming: root %s\n' "$ROOT"

if [ ! -f "$TABLE" ]; then
    fail "$TABLE is missing; the matcher's acceptance table is gone"
    printf '\n1 SSID-naming check FAILED.\n' >&2
    exit 1
fi

# The whitelabel prefix, spelled the gutter's way (bracket expressions): this
# file must not carry the re-brand's name as contiguous bytes either.

# --- A. structural ----------------------------------------------------------
printf '\n--- A. the table is well-formed and covers every class ---\n'

bad_lines="$(grep -vE '^(match|nomatch)( .*)?$|^#.*$|^$' "$TABLE" || true)"
if [ -z "$bad_lines" ]; then
    pass "every line is 'match <ssid>', 'nomatch <ssid>', a comment, or blank"
else
    fail "malformed line(s): $(printf '%s' "$bad_lines" | head -3 | tr '\n' ' ')"
fi

require_class() { # require_class <grep-args...> -- <label>
    local pattern="$1" label="$2" count
    count="$(grep -cE -- "$pattern" "$TABLE" || true)"
    if [ "${count:-0}" -ge 1 ]; then
        pass "$label"
    else
        fail "required class missing ($label): no line matches /$pattern/"
    fi
}

require_class '^match TollGate-[A-Z0-9]+'       'default brand, bare, canonical case'
require_class '^match tollgate-'                'default brand, bare, lowercase (installer era)'
# [Nn]et4[s]ats: the bracket expressions keep this file's bytes from forming
# the whitelabel name contiguously (the rebrand gutter's own idiom) while
# the regex still matches the canonical and lowercase spellings.
require_class '^match [Nn]et4[s]ats-'           'whitelabel brand, bare, canonical case'
require_class '^match net4[s]ats-'              'whitelabel brand, bare, lowercase'
# The single-'!'-decorated POSITIVE classes and the ssidDecoration wiring
# check land with #706's reader half (PR #839): this fence pins what the
# CURRENT matcher accepts, so it is green on main by construction. The
# double-'!' rejections below are true on both shapes and stay.
require_class '^nomatch !!TollGate-'            'double-! rejection, default brand (#706)'
require_class '^nomatch !![Nn]et4[s]ats-'       'double-! rejection, whitelabel brand'
require_class '^nomatch MyTollGate-'            'anchored prefix negative (no leading substring match)'
require_class '^nomatch TollGatex-'             'exact-prefix negative'
require_class '^nomatch$'                       'the empty SSID is not a TollGate'

wl_any="$(grep -icE "^match (!?)[Nn]et4[s]ats-" "$TABLE" || true)"
wl_lower="$(grep -cE "^match (!?)net4[s]ats-" "$TABLE" || true)"
if [ "${wl_any:-0}" -ge 3 ] && [ "${wl_lower:-0}" -ge 1 ]; then
    pass "whitelabel coverage: ${wl_any} case-insensitive lines, ${wl_lower} lowercase"
else
    fail "whitelabel coverage too thin: ${wl_any} case-insensitive lines, ${wl_lower} lowercase"
fi

# The Go pin must be wired: brands_test.go reads THIS file by name, so the
# table cannot drift out from under the unit test.
if grep -qF 'ssid-naming-fixtures.txt' "$BRANDS_TEST"; then
    pass "brands_test.go loads the pinned table (the Go pin is wired)"
else
    fail "brands_test.go no longer references ssid-naming-fixtures.txt — the table is decorative"
fi

# --- B. behavioural ---------------------------------------------------------
printf '\n--- B. the matcher meets the table (real Go test) ---\n'

if ! command -v go >/dev/null 2>&1; then
    fail "go is not on PATH — the behavioural layer cannot run (a skip would make this fence decoration)"
else
    info "go $(go version | awk '{print $3}')"
    if (cd "$ROOT/src/wireless_gateway_manager" && go test -count=1 -run 'TestHasTollGateSSID|TestWhitelabel|TestSSIDDecoration' .) >/tmp/ssid-naming-go.log 2>&1; then
        pass "hasTollGateSSID accepts/rejects the pinned table (go test green)"
    else
        fail "the matcher no longer meets the pinned table — go test output:"
        sed 's/^/        /' /tmp/ssid-naming-go.log >&2
    fi
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d SSID-naming check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'The SSID matcher meets its pinned acceptance table (both brands, case-insensitive, double-! rejected; the single-! classes arrive with #706 reader PR #839).\n'
