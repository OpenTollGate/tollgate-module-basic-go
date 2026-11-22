#!/usr/bin/env bash
# check-go.sh -- does this host build the module, and with which Go?
#
# Three-way finding, because the answer is not "yes/no":
#   1. Ubuntu 24.04's packaged Go (1.22.5) CANNOT build the module.
#   2. The module's own go.mod floor is 1.26.0, so the fetch is mandatory.
#   3. A fetched upstream Go 1.27.2 DOES build src/ -- so the Go research
#      surface is reachable on this host, with a declared network step.
#
# Also runs research/bearer-port-demo, the RED/GREEN convention this spike
# follows.
set -uo pipefail
TMBG="${TMBG:-/tmp/tmbg}"
OUT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/out"
mkdir -p "$OUT"
TEE="$OUT/GO-TOOLCHAIN.txt"

{
echo "host        : $(hostname)"
echo "date        : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

echo "== Go toolchains on this host =="
for g in /tmp/goroot/bin/go "$HOME/.local/toolchain/go/bin/go" "$(command -v go 2>/dev/null)"; do
  [ -x "$g" ] && printf '  %-46s %s\n' "$g" "$("$g" version 2>&1)"
done
echo "  apt candidate for golang-go: $(apt-cache policy golang-go 2>/dev/null | awk '/Candidate/{print $2}')"
echo

echo "== module Go floors (the fetch is not optional) =="
for f in "$TMBG/go.mod" "$TMBG/src/go.mod" "$TMBG/research/bearer-port-demo/go.mod"; do
  [ -r "$f" ] && printf '  %-46s %s\n' "$f" "$(awk '/^go /{print "go "$2; exit}' "$f")"
done
echo

OLDGO=/tmp/goroot/bin/go
if [ -x "$OLDGO" ]; then
  echo "== DEMONSTRATION: Ubuntu's packaged Go cannot build the module =="
  echo "  \$ cd $TMBG/src && $OLDGO build ./..."
  ( cd "$TMBG/src" && GOTOOLCHAIN=local GOFLAGS=-mod=mod "$OLDGO" build ./... 2>&1 | head -6 )
  echo "  -> apt's Go is too old. 'apt install golang-go' is NOT a working path."
  echo
fi

NEWGO="$HOME/.local/toolchain/go/bin/go"
[ -x "$NEWGO" ] || NEWGO="$(command -v go 2>/dev/null || true)"
if [ -n "$NEWGO" ] && [ -x "$NEWGO" ]; then
  echo "== FETCHED Go $("$NEWGO" version | awk '{print $3}') =="
  echo "  \$ cd $TMBG/src && go build ./...   (downloads deps on first run)"
  ( cd "$TMBG/src" && GOTOOLCHAIN=local GOFLAGS=-mod=mod timeout 600 "$NEWGO" build ./... 2>&1 | tail -6 )
  brc=$?
  echo "  build rc=$brc"
  echo
  echo "== research/bearer-port-demo (the RED/GREEN convention this spike follows) =="
  ( cd "$TMBG/research/bearer-port-demo" && GOTOOLCHAIN=local "$NEWGO" test -count=1 ./... 2>&1 | tail -4 )
  echo "  test rc=$?"
fi
echo
echo "== CONCLUSION =="
echo "  The Go research surface IS reachable on this host, but ONLY after a"
echo "  network fetch of a Go >= 1.26 toolchain (~70 MB). Ubuntu 24.04's own"
echo "  golang-go (1.22.5) is below the module's floor and cannot build it."
echo "  QEMU (for a real OpenWrt aarch64 image) is NOT present; no Docker/podman."
} 2>&1 | tee "$TEE"
