#!/usr/bin/env bash
# run-parity.sh — Phase 2 wallet parity lane (the gate before gonuts
# demotion). Host-based: cdk-mintd (docker, host netns) behind killproxy,
# cdk-walletd on-host, all money moved through the module's WalletPort
# client built from THIS worktree.
#
# Usage (from tests/cloud-lab/parity/):
#   ./run-parity.sh              # build daemon + client, run the suite
#   ./run-parity.sh --mutation   # additionally run the mutation check
#                               # (builds a gate-bypassed daemon, expects
#                               # the suite to detect it — RED is PASS
#                               # for the mutation)
#
# Knobs:
#   CDK_WALLETD_DIR   daemon repo (default /root/src/cdk-walletd)
#   PARITY_RUN        scratch dir (default /tmp/parity-run; wiped)
set -euo pipefail
cd "$(dirname "$0")"

WORKTREE_ROOT="$(cd ../../.. && pwd)"
RUN="${PARITY_RUN:-/tmp/parity-run}"
WALLETD="${CDK_WALLETD_DIR:-/root/src/cdk-walletd}"
TARGET_DIR="${CARGO_TARGET_DIR:-$HOME/.cargo-target}"
MINT_CONTAINER=parity-mint
PROXY_PORT=3340
MINT_PORT=43340

MUTATION=0
[ "${1:-}" = "--mutation" ] && MUTATION=1

cleanup() {
    pkill -f "killproxy.py $PROXY_PORT" 2>/dev/null || true
    docker rm -f "$MINT_CONTAINER" >/dev/null 2>&1 || true
    pkill -f "cdk-walletd --socket $RUN" 2>/dev/null || true
}
trap cleanup EXIT
cleanup

mkdir -p "$RUN"
rm -rf "$RUN"/*

# --- daemon (fork-pinned cdk) ---
(cd "$WALLETD" && cargo build --release 2>&1 | tail -1)

# --- mutation build: bypass the fail-closed recovery gate (test-only) ---
MUTANT_BIN=""
if [ "$MUTATION" = "1" ]; then
    MUTANT_BIN="$RUN/cdk-walletd.mutated"
    cp "$WALLETD/src/main.rs" "$RUN/main.rs.orig"
    # The mutation: every money-op recovery gate call becomes a plain
    # wallet open. If the suite cannot detect this, it is decoration.
    sed 's/d\.ensure_recovered(/d.wallet_for(/g' "$RUN/main.rs.orig" \
        > "$WALLETD/src/main.rs"
    (cd "$WALLETD" && cargo build --release 2>&1 | tail -1)
    cp "$TARGET_DIR/release/cdk-walletd" "$MUTANT_BIN"
    mv "$RUN/main.rs.orig" "$WALLETD/src/main.rs"
    # mv preserves the ORIGINAL mtime, which is OLDER than the mutant
    # build — cargo then considers the (mutant) target up-to-date and
    # the "restore" rebuild is a NO-OP, leaving the gate-bypassed mutant
    # as the DEFAULT daemon binary for every later run (measured
    # 2026-10-29: a full night of phantom "regressions"). touch forces
    # the rebuild; the strings check refuses to continue if the default
    # binary is not verifiably the real one.
    touch "$WALLETD/src/main.rs"
    (cd "$WALLETD" && cargo build --release 2>&1 | tail -1)  # restore
    # NOTE: no `strings | grep -q` here — under `set -o pipefail`,
    # grep -q closing the pipe early SIGPIPEs strings (141) and the
    # check false-fails. grep -a reads the binary directly.
    if ! grep -aq "recovery complete" "$TARGET_DIR/release/cdk-walletd"; then
        echo "FATAL: default daemon binary is not the restored build (still mutant?) — refusing to run" >&2
        exit 3
    fi
    if grep -aq "recovery complete" "$MUTANT_BIN"; then
        echo "FATAL: mutation binary contains the gate — the sed did not apply; mutation check would be blind" >&2
        exit 3
    fi
    echo "mutation binary: $MUTANT_BIN (default daemon verified restored)"
fi

# --- WalletPort client, built against THIS worktree's tollwallet ---
CLIENT_BUILD="$RUN/client-go"
cp -r "$WALLETD/client" "$CLIENT_BUILD"
sed -i "s|=> ../../tollgate-module-basic-go/src/tollwallet|=> $WORKTREE_ROOT/src/tollwallet|; \
        s|=> ../../tollgate-module-basic-go/src/lightning|=> $WORKTREE_ROOT/src/lightning|" \
    "$CLIENT_BUILD/go.mod"
(cd "$CLIENT_BUILD" && go mod tidy >/dev/null 2>&1 && go build -o "$RUN/cdkinterop" .)
echo "client: $RUN/cdkinterop (worktree tollwallet: $WORKTREE_ROOT)"

# --- mint: cdk-mintd 0.18.2, host netns, behind the proxy ---
# Advertised URL is the PROXY (the stable wallet identity); the real
# listener sits on $MINT_PORT. fakewallet: zero fee, instant settle.
cat > "$RUN/mint.toml" <<EOF
[info]
url = "http://127.0.0.1:$PROXY_PORT/"
listen_host = "127.0.0.1"
listen_port = $MINT_PORT
mnemonic = "file:/gate/mintseed.txt"
[info.logging]
[database]
engine = "sqlite"
[payment_backend]
backend = "fakewallet"
[fake_wallet]
fee_percent = 0
reserve_fee_min = 0
min_delay_time = 0
max_delay_time = 0
EOF
# Mint mnemonic: the well-known dev phrase. The mint's seed is
# irrelevant to the cases — every run gets a FRESH mint (no burned
# ranges), and the duplicate-outputs case reuses WALLET seeds, not the
# mint's.
echo "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about" > "$RUN/mintseed.txt"
docker rm -f "$MINT_CONTAINER" >/dev/null 2>&1 || true
# The `|| true` makes config init idempotent across docker start/stop —
# test_50 stops and starts the mint, and a re-run of --new-mint against
# the initialized DB errors out, leaving cdk-mintd never executed (the
# mint then never comes back; measured 2026-10-29).
docker run -d --name "$MINT_CONTAINER" --network host \
    -v "$RUN":/gate cashubtc/mintd:0.18.2 \
    sh -c "cdk-mintd config init --new-mint --file /gate/mint.toml || true; exec cdk-mintd" \
    >/dev/null
for _ in $(seq 1 50); do
    curl -sf "http://127.0.0.1:$MINT_PORT/v1/keys" >/dev/null 2>&1 && break
    sleep 0.3
done
curl -sf "http://127.0.0.1:$MINT_PORT/v1/keys" >/dev/null || {
    echo "mint failed to boot:"; docker logs "$MINT_CONTAINER" 2>&1 | tail -5; exit 1; }
echo "mint: 127.0.0.1:$MINT_PORT (advertised via proxy :$PROXY_PORT)"

# --- killproxy ---
python3 killproxy.py "$PROXY_PORT" "http://127.0.0.1:$MINT_PORT" "/v1/swap,/v1/melt/bolt11" \
    "$RUN/kill.marker" "$RUN/arm" > "$RUN/killproxy.log" 2>&1 &
for _ in $(seq 1 30); do
    grep -q "listening on" "$RUN/killproxy.log" 2>/dev/null && break
    sleep 0.2
done

# --- suite ---
# PARITY_DAEMON may be pre-set to run the suite against an alternative
# backend speaking the same protocol (e.g. micronuts-walletd).
export PARITY_RUN="$RUN" \
       PARITY_CLI="$RUN/cdkinterop" PARITY_MINT_CONTAINER="$MINT_CONTAINER"
export PARITY_DAEMON="${PARITY_DAEMON:-$TARGET_DIR/release/cdk-walletd}"
if [ -n "$MUTANT_BIN" ]; then
    # Mutation leg: run ONLY the gate case, against the MUTANT binary.
    # (PARITY_DAEMON must be swapped, not just the PARITY_MUTANT flag —
    # the original wiring ran the REAL binary with mutant expectations,
    # which fails for the wrong reason and proves nothing.) The mutant
    # bypasses the recovery gate on every money op; the case's MUTANT
    # branch asserts the fail-closed refusal is ABSENT. If that branch
    # FAILS, the mutant still fails closed and the check is blind.
    PARITY_DAEMON="$MUTANT_BIN" PARITY_MUTANT="$MUTANT_BIN" \
        /usr/bin/python3 -m pytest -v -k fail_closed test_parity.py
    rc=$?
    if [ $rc -eq 0 ]; then
        echo "MUTATION CHECK: PASS — the suite discriminates (mutant opens the gate, suite sees it)"
    else
        echo "MUTATION CHECK: FAIL — suite cannot detect the bypass; it is decoration" >&2
    fi
    exit $rc
fi
PYTEST_ARGS=${PARITY_PYTEST_ARGS:--x -v}
/usr/bin/python3 -m pytest $PYTEST_ARGS test_parity.py
# NOTE: PARITY_PYTEST_ARGS is intentionally unquoted for multi-word args
# (-k "40 or 50" must arrive as one shell word per pytest argument).
