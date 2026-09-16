#!/usr/bin/env bash
# Build the gonuts-side WalletPort driver used by behavioural_parity.py (T2e).
#
#   TOLLWALLET_DIR=/path/to/src/tollwallet ./run.sh
#
# The driver imports the repo's nested tollwallet module, so both that module and
# its `lightning` sibling are path-replaced here (a dependency's own `replace`
# directives are ignored by Go; only the main module's apply).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOLLWALLET_DIR="${TOLLWALLET_DIR:-/home/c03rad0r/r2-work/tollgate-go/src/tollwallet}"
LIGHTNING_DIR="$(dirname "$TOLLWALLET_DIR")/lightning"

if [[ ! -d "$TOLLWALLET_DIR" ]]; then
  echo "no tollwallet module at $TOLLWALLET_DIR (set TOLLWALLET_DIR)" >&2
  exit 2
fi

cat > "$HERE/go.mod" <<EOF
module gonutsinterop

go 1.25.0

require github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet v0.0.0-00010101000000-000000000000

replace github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet => $TOLLWALLET_DIR
replace github.com/OpenTollGate/tollgate-module-basic-go/src/lightning => $LIGHTNING_DIR
EOF

cd "$HERE"
GOTOOLCHAIN=auto GOFLAGS=-mod=mod go build -o gonutsinterop .
echo "built $HERE/gonutsinterop"
