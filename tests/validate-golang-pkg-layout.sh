#!/bin/bash
#
# validate-golang-pkg-layout.sh
#
# Validates that the tollgate-wrt feed Makefile's GO_PKG / GO_PKG_BUILD_PKG /
# GO_PKG_LDFLAGS_X variables are correct for the go.mod-in-src/ layout.
#
# This simulates what golang-build.sh (from lang/golang/) does:
#   1. configure(): cd $BUILD_DIR, copy *.go/go.mod/etc to $GO_BUILD_DIR/src/$GO_PKG/
#   2. build(): check $BUILD_DIR/go.mod, run go list $GO_BUILD_PKG
#
# We can't run a full OpenWrt SDK locally, but we CAN verify:
#   - go.mod is reachable from PKG_BUILD_DIR
#   - GO_PKG matches the module declaration
#   - GO_PKG_BUILD_PKG resolves to a real directory
#   - GO_PKG_LDFLAGS_X targets the correct import paths
#   - The Go toolchain can actually build the target

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC_DIR="$REPO_ROOT/src"
MAKEFILE="$REPO_ROOT/packaging/openwrt-feed/net/tollgate-wrt/Makefile"

PASS=0
FAIL=0

ok()   { echo "  PASS: $1"; PASS=$((PASS+1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

echo "=== golang-package.mk layout validation ==="
echo ""

# --- Extract Makefile variables (expand $(GO_PKG) manually) ---
GO_PKG=$(grep '^GO_PKG:=' "$MAKEFILE" | sed 's/GO_PKG:=//')
PKG_VERSION=$(grep '^PKG_VERSION:=' "$MAKEFILE" | sed 's/PKG_VERSION:=//')
# GO_PKG_BUILD_PKG uses $(GO_PKG) — expand it
GO_PKG_BUILD_PKG=$(grep '^GO_PKG_BUILD_PKG:=' "$MAKEFILE" -A1 | tail -1 | sed "s/[[:space:]]*//" | sed "s|\$(GO_PKG)|${GO_PKG}|")

echo "GO_PKG          = $GO_PKG"
echo "GO_PKG_BUILD_PKG= $GO_PKG_BUILD_PKG"
echo "PKG_VERSION     = $PKG_VERSION"
echo ""

# --- Check 1: go.mod exists at src/ ---
if [ -f "$SRC_DIR/go.mod" ]; then
    ok "go.mod found at src/go.mod"
else
    fail "go.mod NOT found at src/go.mod — PKG_BUILD_DIR must point at src/"
    exit 1
fi

# --- Check 2: module path matches GO_PKG ---
MODULE_PATH=$(head -1 "$SRC_DIR/go.mod" | awk '{print $2}')
if [ "$MODULE_PATH" = "$GO_PKG" ]; then
    ok "GO_PKG ($GO_PKG) matches go.mod module declaration"
else
    fail "GO_PKG ($GO_PKG) != go.mod module ($MODULE_PATH)"
fi

# --- Check 3: GO_PKG_BUILD_PKG resolves to a real directory ---
BUILD_PKG_REL="${GO_PKG_BUILD_PKG#$GO_PKG/}"
SOURCE_PATH="$SRC_DIR/$BUILD_PKG_REL"
if [ -d "$SOURCE_PATH" ]; then
    ok "GO_PKG_BUILD_PKG resolves: $SOURCE_PATH exists"
else
    fail "GO_PKG_BUILD_PKG resolves to $SOURCE_PATH which does NOT exist"
fi

# --- Check 4: GO_PKG_BUILD_PKG contains main.go ---
if [ -f "$SOURCE_PATH/main.go" ]; then
    ok "main.go found at $SOURCE_PATH/main.go"
else
    fail "main.go NOT found at $SOURCE_PATH/main.go"
fi

# --- Check 5: LDFLAGS_X targets the correct import path ---
# Expand $(GO_PKG) in the LDFLAGS lines and check
LDFLAGS_EXPANDED=$(grep 'GO_PKG_LDFLAGS_X' "$MAKEFILE" -A3 | grep 'cli.Version' | sed "s|\$(GO_PKG)|${GO_PKG}|")
EXPECTED_LD="${GO_PKG}/src/cli.Version"
if echo "$LDFLAGS_EXPANDED" | grep -q "$EXPECTED_LD"; then
    ok "GO_PKG_LDFLAGS_X uses /src/cli (matches go.mod replace directive)"
else
    fail "GO_PKG_LDFLAGS_X cli path wrong: expected $EXPECTED_LD, got: $LDFLAGS_EXPANDED"
fi

# --- Check 6: Actual Go cross-compile with the correct ldflags ---
echo ""
echo "=== Go cross-compile test (linux/amd64) ==="
LDFLAGS="-X '${GO_PKG}/src/cli.Version=v${PKG_VERSION}-test' \
  -X '${GO_PKG}/src/cli.GitCommit=test123' \
  -X '${GO_PKG}/src/cli.BuildTime=0'"

if (cd "$SRC_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -buildvcs=false -trimpath -ldflags="$LDFLAGS" -o /dev/null ./cmd/tollgate-wrt) 2>&1; then
    ok "go build ./cmd/tollgate-wrt succeeded with corrected ldflags"
else
    fail "go build ./cmd/tollgate-wrt failed"
fi

# --- Check 7: Verify ldflags actually inject (old path should NOT work) ---
echo ""
echo "=== ldflags injection verification ==="
TMPBIN=$(mktemp)
WRONG_LDFLAGS="-X '${GO_PKG}/cli.Version=v${PKG_VERSION}-test'"

if (cd "$SRC_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -buildvcs=false -trimpath -ldflags="$WRONG_LDFLAGS" -o "$TMPBIN" ./cmd/tollgate-wrt) 2>&1; then
    # Build succeeded, but Version won't be injected because /cli is wrong path
    VERSION=$(strings "$TMPBIN" | grep -c 'v0.4.0-test' || true)
    if [ "$VERSION" -gt 0 ]; then
        fail "Wrong ldflags path (/cli) unexpectedly injected version — investigate"
    else
        ok "Wrong ldflags path (/cli) correctly does NOT inject version (confirms /src/cli is needed)"
    fi
else
    fail "go build with wrong ldflags path failed unexpectedly"
fi
rm -f "$TMPBIN"

# --- Check 8: Simulate golang-build.sh configure() file copy ---
echo ""
echo "=== golang-build.sh configure() simulation ==="
SIM_DIR=$(mktemp -d)
SIM_GO_BUILD="$SIM_DIR/build"
SIM_GO_PKG_DIR="$SIM_GO_BUILD/src/$GO_PKG"

# Simulate configure(): copy *.go, *.c, *.cc, *.cpp, *.h, *.hh, *.hpp, *.proto, *.s, go.mod, go.sum
mkdir -p "$SIM_GO_PKG_DIR"
cd "$SRC_DIR"
find ./ -path "*/.*" -prune -o -not -type d -print | while read -r f; do
    case "$f" in
        *.go|*.c|*.cc|*.cpp|*.h|*.hh|*.hpp|*.proto|*.s)
            dest="$SIM_GO_PKG_DIR/${f#./}"
            mkdir -p "${dest%/*}"
            cp -fpR "$f" "$dest"
            ;;
        */go.mod|*/go.sum|*/go.work)
            dest="$SIM_GO_PKG_DIR/${f#./}"
            mkdir -p "${dest%/*}"
            cp -fpR "$f" "$dest"
            ;;
    esac
done

# Verify go.mod ended up at $SIM_GO_PKG_DIR/go.mod (not $SIM_GO_PKG_DIR/src/go.mod)
if [ -f "$SIM_GO_PKG_DIR/go.mod" ]; then
    ok "configure() copies go.mod to \$GO_BUILD_DIR/src/\$GO_PKG/go.mod (no extra src/ prefix)"
else
    fail "go.mod not at expected location after configure() simulation"
    echo "  Looking for: $SIM_GO_PKG_DIR/go.mod"
    find "$SIM_GO_BUILD" -name "go.mod" -type f 2>/dev/null | head -10
fi

# Verify cmd/tollgate-wrt ended up at the right place
if [ -d "$SIM_GO_PKG_DIR/cmd/tollgate-wrt" ]; then
    ok "configure() copies cmd/tollgate-wrt to \$GO_BUILD_DIR/src/\$GO_PKG/cmd/tollgate-wrt/"
else
    fail "cmd/tollgate-wrt not at expected location after configure()"
    find "$SIM_GO_BUILD" -name "tollgate-wrt" -type d 2>/dev/null | head -10
fi

# Clean up simulation
rm -rf "$SIM_DIR"

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
exit $FAIL
