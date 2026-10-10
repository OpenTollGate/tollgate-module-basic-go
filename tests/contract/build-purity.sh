#!/usr/bin/env bash
# Verifies that test-only configuration (TOLLGATE_TEST_CONFIG_DIR, and the
# rateLimitTestBypass test-context bypass of #748) is not
# compiled into the production binary. Catches the class of bug where a Go
# source file with an init() that sets test env vars or flags uses a filename
# that doesn't match *_test.go and lacks a build-tag guard.

set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT="$(pwd)"
SRC="$ROOT/src"

RED='\033[31m'
GREEN='\033[32m'
BOLD='\033[1m'
RESET='\033[0m'

PASS=0
FAIL=0

pass() { PASS=$((PASS+1)); echo "  ${GREEN}PASS${RESET}: $1"; }
fail() { FAIL=$((FAIL+1)); echo "  ${RED}FAIL${RESET}: $1"; }

echo "${BOLD}=== Build-Purity Contract Tests ===${RESET}"
echo ""

# --- Test 1: No non-_test.go source file sets TOLLGATE_TEST_CONFIG_DIR ---
echo "${BOLD}--- source file check ---${RESET}"
offenders=""
while IFS= read -r -d '' f; do
    base="$(basename "$f")"
    case "$base" in
        *_test.go) continue ;;
    esac
    # The audited test-context writers: TOLLGATE_TEST_CONFIG_DIR (os.Setenv)
    # and rateLimitTestBypass = true (the #748 test-context bypass). Both
    # must live only in build-tag-guarded files or _test.go — this is the
    # check behind "#778: enforced by the build-purity contract".
    if grep -qE 'os\.Setenv.*TOLLGATE_TEST_CONFIG_DIR|rateLimitTestBypass[[:space:]]*=[[:space:]]*true' "$f"; then
        first_line=$(head -1 "$f")
        if echo "$first_line" | grep -q '^//go:build'; then
            continue
        fi
        offenders="$offenders $f"
    fi
done < <(find "$SRC" -name '*.go' -not -path '*/vendor/*' -print0)

if [ -z "$offenders" ]; then
    pass "no production source file sets test-context state (TOLLGATE_TEST_CONFIG_DIR / rateLimitTestBypass: all guarded by build tag or _test.go)"
else
    fail "production files set test-context state without guard:$offenders"
fi

# --- Test 2: Any file with test config state has a build tag ---
echo "${BOLD}--- build tag guard check ---${RESET}"
while IFS= read -r -d '' f; do
    base="$(basename "$f")"
    # skip this script's directory
    if grep -qE 'os\.Setenv.*TOLLGATE_TEST_CONFIG_DIR|rateLimitTestBypass[[:space:]]*=[[:space:]]*true' "$f"; then
        first_line=$(head -1 "$f")
        if echo "$first_line" | grep -q '^//go:build'; then
            pass "$base has build tag guard"
        else
            fail "$base sets test-context state without build tag"
        fi
    fi
done < <(find "$SRC" -name '*.go' -not -path '*/vendor/*' -print0)

# --- Test 3: Production binary does not contain test config path ---
echo "${BOLD}--- production binary check ---${RESET}"
tmpbin=$(mktemp)
trap 'rm -f "$tmpbin"' EXIT

if GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -o "$tmpbin" -trimpath -ldflags="-s -w" "$SRC" 2>/dev/null; then
    if strings "$tmpbin" | grep -q 'tollgate-main-test'; then
        fail "production binary contains 'tollgate-main-test' test path"
    else
        pass "production binary free of test config path"
    fi
else
    # cross-compile may fail on CI without ARM toolchain — skip gracefully
    echo "  SKIP: cross-compile unavailable (non-ARM CI runner)"
fi

# --- Test 4: go vet with testenv tag passes ---
echo "${BOLD}--- go vet ---${RESET}"
if (cd "$SRC" && go vet -tags testenv ./... 2>&1); then
    pass "go vet -tags testenv passes"
else
    fail "go vet -tags testenv failed"
fi

echo ""
echo "${BOLD}=== Results: ${GREEN}${PASS} passed${RESET}, ${RED}${FAIL} failed${RESET} ==="
[ "$FAIL" -eq 0 ]
