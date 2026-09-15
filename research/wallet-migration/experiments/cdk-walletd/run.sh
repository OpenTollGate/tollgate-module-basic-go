#!/usr/bin/env bash
# Build + run the experimental CDK wallet sidecar daemon, then smoke-test the
# protocol it shares with the Go sidecar client (src/tollwallet/sidecar.go).
#
# Usage:
#   1. copy ./Cargo.toml and ./src/main.rs into <cdk-checkout>/crates/cdk-walletd
#   2. ./run.sh <cdk-checkout>
set -euo pipefail

CDK="${1:?usage: $0 <cdk-checkout>}"
TC="${RUSTUP_STABLE:-$HOME/.rustup/toolchains/stable-x86_64-unknown-linux-gnu}"
export CARGO_HOME="$HOME/.cargo" RUSTUP_HOME="$HOME/.rustup"
export RUSTC="$TC/bin/rustc"
export CARGO_TARGET_DIR="$CDK/target-walletd"

mkdir -p "$CDK/crates/cdk-walletd/src"
cp "$(dirname "$0")/Cargo.toml" "$CDK/crates/cdk-walletd/Cargo.toml"
cp "$(dirname "$0")/src/main.rs" "$CDK/crates/cdk-walletd/src/main.rs"

( cd "$CDK" && "$TC/bin/cargo" build -p cdk-walletd --release )

BIN="$CARGO_TARGET_DIR/release/cdk-walletd"
SOCK="$(mktemp -u /tmp/cdk-walletd.XXXX.sock)"

"$BIN" --socket "$SOCK" --work-dir "$CARGO_TARGET_DIR/wallet-work" --mint http://127.0.0.1:1 >/tmp/cdk-walletd.log 2>&1 &
DPID=$!
for _ in $(seq 1 20); do [ -S "$SOCK" ] && break; sleep 0.3; done

python3 - "$SOCK" <<'PY'
import socket, json, sys
s = socket.socket(socket.AF_UNIX); s.settimeout(4); s.connect(sys.argv[1])
f = s.makefile("rwb")
for r in ({"id":1,"method":"info"}, {"id":2,"method":"get_balance"},
          {"id":3,"method":"decode_token","params":{"token":"bad"}}):
    f.write((json.dumps(r)+"\n").encode()); f.flush()
    print(r["method"], "->", f.readline().decode().strip()[:150])
s.close()
PY

kill -9 "$DPID" 2>/dev/null || true
echo "ok"
