#!/usr/bin/env bash
# run-rig.sh -- one command a third party runs to reproduce every number in
# RISK-AND-IAC.md.
#
#   bash rig/run-rig.sh
#
# NETWORK NEEDS, DECLARED PLAINLY:
#
#   step 1 (toolchain) needs the internet ONLY if the tools are absent. If
#     bitcoind and python3 are already present, this script touches NO network
#     at all -- it is localhost-only from there on. Set BITCOIND=/path/to/bitcoind
#     to point at an existing binary.
#   step 2 (sim) needs NO network and NO chain. It is arithmetic.
#   step 3 (channel demo) needs NO network, only a LOCAL regtest node.
#
# So the offline claim is checkable by the rig itself: run it with the network
# down once the toolchain is in place and every assertion still executes.
#
# Every artefact lands in rig/out/ and is committed.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$HERE/out"
BTC_VER="${BTC_VER:-31.1}"
BTC_SHA256="b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e"
BTC_TGZ="bitcoin-${BTC_VER}-x86_64-linux-gnu.tar.gz"
TOOLROOT="${TOOLROOT:-$HOME/.local/toolchain}"
BTCROOT="$TOOLROOT/btc"
DATADIR="$BTCROOT/regtest-data"
RPCPORT=18443
mkdir -p "$OUT"

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# --------------------------------------------------------------------------
say "step 0: environment (this is the B1 evidence; also run rig/check-env.sh)"
bash "$HERE/check-env.sh" | tee "$OUT/ENV.txt"

# --------------------------------------------------------------------------
say "step 1: toolchain"
PY="$(command -v python3)" || { echo "FATAL: python3 required"; exit 1; }
echo "python3: $PY ($($PY -V 2>&1))"

if [ -n "${BITCOIND:-}" ] && [ -x "${BITCOIND:-}" ]; then
  : # caller supplied a bitcoind; nothing to fetch
elif [ -x "$BTCROOT/bin/bitcoind" ]; then
  echo "bitcoind: $BTCROOT/bin/bitcoind (already installed)"
else
  echo "bitcoind: NOT PRESENT -> fetching (THIS STEP NEEDS THE NETWORK)"
  tmp="$(mktemp -d)"
  curl -sSL --max-time 300 -o "$tmp/$BTC_TGZ" \
    "https://bitcoincore.org/bin/bitcoin-core-${BTC_VER}/${BTC_TGZ}" || {
      echo "FATAL: download failed and no local bitcoind. This rig needs either"
      echo "       the internet once, or BITCOIND=/path/to/bitcoind."
      exit 2; }
  echo "$BTC_SHA256  $tmp/$BTC_TGZ" | sha256sum -c - || {
      echo "FATAL: checksum mismatch -- refusing to install"; exit 3; }
  rm -rf "$BTCROOT"; mkdir -p "$BTCROOT"
  tar -C "$BTCROOT" --strip-components=1 -xzf "$tmp/$BTC_TGZ"
  rm -rf "$tmp"
fi
BITCOIND="${BITCOIND:-$BTCROOT/bin/bitcoind}"
CLI="$(dirname "$BITCOIND")/bitcoin-cli"
"$BITCOIND" --version | head -1 | tee "$OUT/BITCOIND-VERSION.txt"

# --------------------------------------------------------------------------
say "step 2: economics simulator (no chain, no network)"
$PY "$HERE/sim_economics.py" | tee "$OUT/SIM-OUTPUT.txt"
sim_rc=${PIPESTATUS[0]}

# --------------------------------------------------------------------------
say "step 3: start a LOCAL regtest node (localhost only)"
if ! "$CLI" -datadir="$DATADIR" -rpcport=$RPCPORT -rpcuser=rig -rpcpassword=rig \
      getblockcount >/dev/null 2>&1; then
  rm -rf "$DATADIR"; mkdir -p "$DATADIR"
  cat > "$DATADIR/bitcoin.conf" <<EOF
regtest=1
server=1
fallbackfee=0.0002
txindex=1
[regtest]
rpcuser=rig
rpcpassword=rig
EOF
  "$BITCOIND" -datadir="$DATADIR" -daemon -port=19444 -rpcport=$RPCPORT \
    -rpcbind=127.0.0.1 -rpcallowip=127.0.0.1
  for i in $(seq 1 30); do
    "$CLI" -datadir="$DATADIR" -rpcport=$RPCPORT -rpcuser=rig -rpcpassword=rig \
      getblockcount >/dev/null 2>&1 && break
    sleep 1
  done
  R() { "$CLI" -datadir="$DATADIR" -rpcport=$RPCPORT -rpcuser=rig -rpcpassword=rig -rpcwallet=rig "$@"; }
  R createwallet rig >/dev/null 2>&1 || true
  R generatetoaddress 101 "$(R getnewaddress)" >/dev/null
fi
R() { "$CLI" -datadir="$DATADIR" -rpcport=$RPCPORT -rpcuser=rig -rpcpassword=rig -rpcwallet=rig "$@"; }
echo "regtest height $(R getblockcount)"

# --------------------------------------------------------------------------
say "step 4a: RED -- the construction a chain-blind payer would force"
$PY "$HERE/demo_channel.py" --variant naive | tee "$OUT/RED.txt"
red_rc=${PIPESTATUS[0]}
grep -c '\[FAIL\]' "$OUT/RED.txt" | xargs -I{} echo "RED: {} checks failed (expected: the non-custody check)"

# --------------------------------------------------------------------------
say "step 4b: GREEN -- the Spilman construction"
$PY "$HERE/demo_channel.py" --variant spilman | tee "$OUT/GREEN.txt"
green_rc=${PIPESTATUS[0]}

# --------------------------------------------------------------------------
say "VERDICT"
cat <<'EOF'
Read in this order:

  out/ENV.txt          what this host actually has (B1)
  out/SIM-OUTPUT.txt   the economics tables (A4) -- a SIMULATOR, see its limits
  out/RED.txt          the payer-offline construction: non-custody FAILS (A1)
  out/GREEN.txt        the Spilman construction: non-custody holds, but the
                       payer is interactive or chain-dependent (A2/T7)

Two checks in GREEN.txt are DELIBERATE failures, and are the finding, not a
bug: T7's "named a construction that is BOTH payer-chain-blind AND
non-custodial" is asserted FALSE on purpose. If a future contributor makes that
check pass, this memo is disproved and should be revised -- that is the
falsification condition made executable.
EOF
echo
echo "sim_rc=$sim_rc red_rc=$red_rc green_rc=$green_rc"
echo "(red_rc is expected to be non-zero: the naive variant is SUPPOSED to fail"
echo " the non-custody check. green_rc is expected to be non-zero too, because"
echo " GREEN.txt contains the deliberate disproof assertion.)"
exit 0
