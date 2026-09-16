#!/usr/bin/env bash
# Build and run the T15 NUT-14 HTLC `n_sigs`-omission parity test against the
# cashu crate in a CDK checkout.
#
#   CDK_DIR=/path/to/cdk ./run.sh
#
# CDK_DIR must contain crates/cashu. Defaults to the research checkout used to
# produce raw/htlc-nsigs-parity-cdk.txt.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CDK_DIR="${CDK_DIR:-/home/c03rad0r/r2-work/cdk-upstream}"
CASHU_DIR="$CDK_DIR/crates/cashu"

if [[ ! -d "$CASHU_DIR" ]]; then
  echo "no cashu crate at $CASHU_DIR (set CDK_DIR to a CDK checkout)" >&2
  exit 2
fi

cat > "$HERE/Cargo.toml" <<EOF
[package]
name = "htlc-nsigs-parity"
version = "0.1.0"
edition = "2021"

[dependencies]
cashu = { path = "$CASHU_DIR" }
EOF

echo "cashu crate: $CASHU_DIR"
echo "cashu revision: $(git -C "$CDK_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
cd "$HERE"
CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$HERE/target}" cargo run --offline --release
