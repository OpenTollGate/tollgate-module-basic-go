#!/bin/sh
# phases/01-build.sh — F1/F2/F3/F4/F10/E1 (build host + the deployed artifact).
#
# Runs on the BUILD HOST. Produces raw/build-*.txt. Requires a checkout of the
# candidate revision; set SRC to it (default: a worktree of the deployed SHA).
#
# The deployed binary is fetched by phases/00-env.sh (raw/tollgate-wrt.device.bin)
# and analysed here too, because the interesting F4/E3 result is the DIFFERENCE
# between the documented build path and the artifact that actually ships.
. "$(dirname "$0")/../lib/common.sh"

SRC="${SRC:-$HOME/worktrees/baseline-build-d0cb92f2}"
REV="${REV:-d0cb92f2}"
BUILDDIR="${BUILDDIR:-$OUT/build}"
T="$OUT/build-report.txt"
mkdir -p "$BUILDDIR"

say() { echo "$*" | tee -a "$T"; }
sec() { say ""; say "## $*"; }

say "# F1/F2/F3/F4/F10/E1 — build-host metrics + deployed-artifact analysis"
say "# generated (UTC): $(utc)"
say "# candidate revision: $REV  (worktree: $SRC)"

[ -d "$SRC" ] || { say "src worktree missing: $SRC"; exit 1; }

# ---------------------------------------------------------------- F1 / F2
sec "F1/F2 — binary size, documented CI/local build path"
say '# literal command (packaging/local-build-ipk.sh, .github/workflows/build-package.yml):'
say '#   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \'
say '#     go build -C src -o tollgate-wrt -trimpath -ldflags="<version stamps>" main.go'
PKG_VERSION=$(cat "$SRC/VERSION" 2>/dev/null || echo unknown)
GIT_COMMIT=$(git -C "$SRC" rev-parse --short HEAD)
BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
LDFLAGS="-s -w -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.Version=$PKG_VERSION' -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.GitCommit=$GIT_COMMIT' -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.BuildTime=$BUILD_TIME' -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager.GitBranch=main'"
say "PKG_VERSION=$PKG_VERSION GIT_COMMIT=$GIT_COMMIT"

STRIPPED="$BUILDDIR/tollgate-wrt.stripped.aarch64"
UNSTRIPPED="$BUILDDIR/tollgate-wrt.UNSTRIPPED.aarch64"
if [ ! -x "$STRIPPED" ]; then
    say "--- cold build (stripped) ---"
    { time CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -C "$SRC/src" -o "$STRIPPED" -trimpath -ldflags="$LDFLAGS" main.go ; } 2>&1 | tee -a "$T"
fi
if [ ! -x "$UNSTRIPPED" ]; then
    say "--- cold build (unstripped) ---"
    { time CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -C "$SRC/src" -o "$UNSTRIPPED" -trimpath -ldflags="${LDFLAGS#-s -w }" main.go ; } 2>&1 | tee -a "$T"
fi

for b in "$STRIPPED" "$UNSTRIPPED"; do
    say ""
    say "file: $b"
    ls -l "$b" | tee -a "$T"
    sha256sum "$b" | tee -a "$T"
    file "$b" | tee -a "$T"
    size "$b" | tee -a "$T"
    go version -m "$b" 2>/dev/null | head -3 | tee -a "$T"
done

sec "F2 — section/symbol breakdown (unstripped)"
readelf -SW "$UNSTRIPPED" 2>/dev/null | sed -n '/^ *\[/p' | head -40 | tee -a "$T"
say ""
say "stripping delta: $(( $(wc -c < "$UNSTRIPPED") - $(wc -c < "$STRIPPED") )) bytes ($(awk -v u="$(wc -c < "$UNSTRIPPED")" -v s="$(wc -c < "$STRIPPED")" 'BEGIN{printf "%.1f", (u-s)*100/u}')% of unstripped)"
say "largest sections (bytes):"
readelf -SW "$UNSTRIPPED" 2>/dev/null \
  | awk '/^ *\[/{name=$2; size=$6; if (size ~ /^[0-9a-f]+$/) printf "%10d %s\n", strtonum("0x" size), name}' \
  | sort -rn | head -12 | tee -a "$T"

# ---------------------------------------------------------------- F4
sec "F4 — dynamic dependencies / libc linkage"
say "--- documented build (CGO_ENABLED=0) ---"
readelf -d "$STRIPPED" 2>/dev/null | grep -E 'NEEDED|RPATH|RUNPATH' || say "NEEDED: (none — statically linked)"
say "--- deployed artifact (raw/tollgate-wrt.device.bin) ---"
if [ -s "$OUT/tollgate-wrt.device.bin" ]; then
    ls -l "$OUT/tollgate-wrt.device.bin" | tee -a "$T"
    sha256sum "$OUT/tollgate-wrt.device.bin" | tee -a "$T"
    file "$OUT/tollgate-wrt.device.bin" | tee -a "$T"
    readelf -d "$OUT/tollgate-wrt.device.bin" 2>/dev/null | grep -E 'NEEDED|RPATH|RUNPATH' | tee -a "$T"
else
    say "(raw/tollgate-wrt.device.bin missing — run: ./run.sh env)"
fi

# ---------------------------------------------------------------- E1
sec "E1 — direct + transitive dependency count and licences"
cd "$SRC/src" 2>/dev/null || cd "$SRC"
go list -m all 2>/dev/null > "$OUT/build-golist-mall.txt"
say "modules (go list -m all): $(wc -l < "$OUT/build-golist-mall.txt")"
say "go directive: $(grep -m1 '^go ' go.mod 2>/dev/null)"
say "gonuts pin: $(grep -m1 'gonuts-tollgate' go.mod 2>/dev/null)"
say "replace directives: $(grep -c '^replace\|=> ' go.mod 2>/dev/null)"
say "replace lines:"
grep -A1 '^replace' go.mod 2>/dev/null | head -20 | tee -a "$T"
say ""
say "top modules by size contribution (approximate: file count in module cache)"
say "(see build-golist-mall.txt for the full list)"

# ---------------------------------------------------------------- F3
sec "F3 — packaged .apk size contribution"
say "Deployed package as installed: $(rssh 'apk list -I 2>/dev/null | grep -i tollgate-wrt')"
say "Deployed /usr/bin/tollgate-wrt: $(rssh 'ls -l /usr/bin/tollgate-wrt')"
say "NOTE: the .apk is built by the feed (golang-package.mk, source-built with the"
say "      OpenWrt cross toolchain), not by the documented CI command — see the F4"
say "      delta above. Building the .apk here needs the OpenWrt SDK for"
say "      mediatek-filogic; that is E5/T5c work, not baseline work."

# ---------------------------------------------------------------- F8 context
sec "F8 — descriptor budget"
say "router ulimit -n (service): $(rssh "grep -i 'open files' /proc/\$(pidof $WALLET_PROC)/limits 2>/dev/null | head -1")"

say ""
say "wrote $T"
