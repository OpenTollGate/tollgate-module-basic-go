#!/usr/bin/env bash
# Measure the per-proof proof store against the whole-blob store.
#
# Part of the nucula persistence work. OUR script. It builds the port
# harness (experiments/nucula-port) twice — once against an unmodified nucula
# checkout ("before"), once against the branch that adds cashu::proof_store
# ("after") — and runs the measurement and consistency modes of both.
#
# No nucula source lives in this repository; point the two variables at
# checkouts you obtained yourself:
#
#   BEFORE_SRC=/path/to/nucula-8ad0812          (upstream main)
#   AFTER_SRC=/path/to/nucula@feat-incremental  (the change)
#
# Usage:
#   BEFORE_SRC=... AFTER_SRC=... ./measure.sh [output-dir]
#
# Modes used (see ../../src/harness/nucula_core_harness.cpp):
#   selftest   nucula's own suites (crypto/codec/wallet-math/JSON)
#   measure    whole-blob write cost (the old storage model)
#   spend      real wallet path: Wallet::remove_proofs() on a 200-proof wallet
#   pay        per-payment cost through the storage layer + round-trip checks
#   storetest  round trips, "unchanged save writes nothing", slot-mark bound,
#              exhaustive interruption sweeps, legacy migration, recovery
set -euo pipefail

HARNESS_DIR=$(cd "$(dirname "$0")/.." && pwd)
BEFORE_SRC=${BEFORE_SRC:?set BEFORE_SRC to an unmodified nucula source root (main/ lives there)}
AFTER_SRC=${AFTER_SRC:?set AFTER_SRC to the nucula source root with the per-proof store}
PROOFS=${PROOFS:-200}
OUT=${1:-$HARNESS_DIR/incremental/measurements/$(date -u +%Y%m%dT%H%M%SZ)}

mkdir -p "$OUT"

{
  echo "# per-proof store measurement"
  echo "date:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host:        $(uname -srm), $(nproc) cpus"
  echo "before_src:  $BEFORE_SRC"
  echo "after_src:   $AFTER_SRC"
  echo "harness:     $HARNESS_DIR"
  echo "proofs:      $PROOFS"
  echo
  echo "before wallets_nvs.cpp sha256: $(sha256sum "$BEFORE_SRC/main/wallet_nvs.cpp" | cut -d' ' -f1)"
  echo "after  wallets_nvs.cpp sha256: $(sha256sum "$AFTER_SRC/main/wallet_nvs.cpp" | cut -d' ' -f1)"
  echo "before wallet_internal.hpp sha256: $(sha256sum "$BEFORE_SRC/main/wallet_internal.hpp" | cut -d' ' -f1)"
  echo "after  wallet_internal.hpp sha256: $(sha256sum "$AFTER_SRC/main/wallet_internal.hpp" | cut -d' ' -f1)"
  for tree in "$BEFORE_SRC" "$AFTER_SRC"; do
    if git -C "$tree" rev-parse --git-dir >/dev/null 2>&1; then
      echo "$tree: git $(git -C "$tree" rev-parse HEAD)"
    fi
  done
} > "$OUT/00-env.txt"

echo "== configuring/building =="
cmake -S "$HARNESS_DIR" -B "$OUT/build-before" -GNinja -DNUCULA_SRC="$BEFORE_SRC" \
      > "$OUT/01-build-before.log" 2>&1
cmake --build "$OUT/build-before" >> "$OUT/01-build-before.log" 2>&1
cmake -S "$HARNESS_DIR" -B "$OUT/build-after" -GNinja -DNUCULA_SRC="$AFTER_SRC" \
      -DNUCULA_HARNESS_STORE_TESTS=ON > "$OUT/02-build-after.log" 2>&1
cmake --build "$OUT/build-after" >> "$OUT/02-build-after.log" 2>&1

run() { # run <label> <binary> <nvs-subdir> <args...>
  local label=$1 bin=$2 sub=$3; shift 3
  local dir="$OUT/nvs-$sub"
  mkdir -p "$dir"
  echo "##### $label: $(basename "$bin") $*" >> "$label"
  NUCULA_NVS_DIR="$dir" "$bin" "$@" >> "$label" 2>&1 || echo "exit=$?" >> "$label"
}

echo "== before =="
run "$OUT/10-before.txt"  "$OUT/build-before/nucula_core_harness" b-selftest selftest
run "$OUT/10-before.txt"  "$OUT/build-before/nucula_core_harness" b-measure  measure --proofs "$PROOFS"
run "$OUT/10-before.txt"  "$OUT/build-before/nucula_core_harness" b-spend    spend   --proofs "$PROOFS"
run "$OUT/10-before.txt"  "$OUT/build-before/nucula_core_harness" b-measure2 measure --proofs $((PROOFS * 2))

echo "== after =="
run "$OUT/20-after.txt"   "$OUT/build-after/nucula_core_harness"  a-selftest selftest
run "$OUT/20-after.txt"   "$OUT/build-after/nucula_core_harness"  a-measure  measure --proofs "$PROOFS"
run "$OUT/20-after.txt"   "$OUT/build-after/nucula_core_harness"  a-spend    spend   --proofs "$PROOFS"
run "$OUT/20-after.txt"   "$OUT/build-after/nucula_core_harness"  a-pay      pay --proofs "$PROOFS" --payments 5
run "$OUT/20-after.txt"   "$OUT/build-after/nucula_core_harness"  a-pay2     pay --proofs $((PROOFS * 2)) --payments 3

echo "== consistency suite (after) =="
run "$OUT/30-storetest.txt" "$OUT/build-after/nucula_core_harness" a-storetest storetest

echo
echo "results in $OUT"
grep -hE 'SELFTEST_RESULT|MEASURE_RESULT|SPEND_RESULT|PAY_RESULT|STORETEST_RESULT' "$OUT"/10-before.txt "$OUT"/20-after.txt "$OUT"/30-storetest.txt || true
