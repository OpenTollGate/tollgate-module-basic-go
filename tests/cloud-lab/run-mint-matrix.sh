#!/usr/bin/env bash
# run-mint-matrix.sh — one payment suite against many mints (the signet zoo).
#
# The same test_mint_matrix.py runs against whichever mints MINT_MATRIX
# names; the router (upstream-ext) is configured to accept exactly those
# mints for the run. Two ways to reach the zoo:
#
#   ZOO_VIA=public (default) — the Cloudflare endpoints
#   (https://cdk-4312959.cashu.exchange, …): the production ingress, the
#   exact URL real wallets see, and no zoo-side mutation. Requires the
#   tunnel to be up (a down tunnel answers Cloudflare error 1033).
#
#   ZOO_VIA=local — docker network attach + container names
#   (http://zoo-cdk-0-17:8085, …): the co-located fallback for a dark
#   tunnel or an offline lab; needs the zoo containers on this host.
#
# Mint funding: ONE float per mint at session start (ZOO_FUND_SATS,
# default 2000), paid over real signet routing by the zoo's own
# pay-and-mint.sh (host-side, before the suite runs) so every test then
# spends offline. Requires ZOO_HOME (default ~/src/cashu-mint-zoo) and
# its zoo-client:tmp image.
#
# Usage:
#   ./run-mint-matrix.sh                       # default zoo matrix
#   MINTS="mine=https://my-mint.example" ./run-mint-matrix.sh
#   ZOO_VIA=local ./run-mint-matrix.sh         # tunnel-dark fallback
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ZOO_VIA="${ZOO_VIA:-public}"
ZOO_FUND_SATS="${ZOO_FUND_SATS:-2000}"
LAB_NET="${LAB_NET:-cloud-lab_tollgate-lab}"
ZOO_HOME="${ZOO_HOME:-$HOME/src/cashu-mint-zoo}"
PAY_AND_MINT="$ZOO_HOME/pay-and-mint.sh"

case "$ZOO_VIA" in
  public)
    MINTS="${MINTS:-cdk-0.17=https://cdk-4312959.cashu.exchange,cdk-0.18=https://cdk-d3dec24.cashu.exchange,cdk-0.18.1=https://cdk-a056e0f.cashu.exchange,ns-0.20=https://ns-1853902.cashu.exchange,ns-0.21=https://ns-a974914.cashu.exchange}" ;;
  local)
    MINTS="${MINTS:-cdk-0.17=http://zoo-cdk-0-17:8085,cdk-0.18=http://zoo-cdk-0-18:8085,cdk-0.18.1=http://zoo-cdk-a056e0f:8085,ns-0.20=http://zoo-ns-1853902:3338,ns-0.21=http://zoo-ns-a974914:3338}"
    # Zoo containers must be reachable by name from the lab network.
    for c in zoo-cdk-0-17 zoo-cdk-0-18 zoo-cdk-a056e0f zoo-ns-1853902 zoo-ns-a974914; do
      docker network connect "$LAB_NET" "$c" 2>/dev/null || true
    done ;;
  *)
    echo "ZOO_VIA must be 'public' or 'local'" >&2; exit 2 ;;
esac

# Router config for this run: the base accepted mints plus every matrix
# mint, generated fresh (gitignored — it is a per-run artifact).
python3 - "$MINTS" <<'PYEOF'
import json, sys
cfg = json.load(open("configs/upstream-config.json"))
for pair in sys.argv[1].split(","):
    if not pair.strip():
        continue
    name, url = pair.strip().split("=", 1)
    cfg["accepted_mints"].append({
        "url": url, "min_balance": 0, "balance_tolerance_percent": 0,
        "payout_interval_seconds": 999999, "min_payout_amount": 999999,
        "price_per_step": 1, "price_unit": "sats", "purchase_min_steps": 0})
json.dump(cfg, open("zoo-mint-config.json", "w"), indent=2)
print("router accepts:", [m["url"] for m in cfg["accepted_mints"]])
PYEOF

# Fund one float wallet per mint, host-side, keyed to the URL the test
# client will use (cdk-cli treats different mint URLs as different
# wallets — fund with the exact container/public URL from MINTS).
mkdir -p matrix-wallets
for pair in ${MINTS//,/ }; do
  name="${pair%%=*}"; url="${pair#*=}"
  echo "== funding float for $name ($url, ${ZOO_FUND_SATS} sats over signet)"
  if [ "$ZOO_VIA" = "local" ]; then
    ZOO_CDK_NETWORK="$LAB_NET" ZOO_CLIENT_IMAGE=zoo-client:tmp \
        bash "$PAY_AND_MINT" "$PWD/matrix-wallets/$name" "$url" "$ZOO_FUND_SATS"
  else
    ZOO_CLIENT_IMAGE=zoo-client:tmp \
        bash "$PAY_AND_MINT" "$PWD/matrix-wallets/$name" "$url" "$ZOO_FUND_SATS"
  fi
done

echo "== matrix: $MINTS (via $ZOO_VIA, floats funded)"
docker compose -f docker-compose.yml -f docker-compose.zoo.yml \
    --profile external-mints up -d --build mint upstream-matrix >/dev/null

MINT_MATRIX="$MINTS" ZOO_FUND_SATS="$ZOO_FUND_SATS" \
    UPSTREAM_URL=http://upstream-matrix:2121 \
    docker compose -f docker-compose.yml -f docker-compose.zoo.yml \
    --profile external-mints run --rm \
    -e MINT_MATRIX -e ZOO_FUND_SATS -e UPSTREAM_URL \
    --entrypoint sh client \
    -c 'cd /tests && rm -rf __pycache__ && python3 -m pytest -sv test_mint_matrix.py'

cleanup() {
    docker compose -f docker-compose.yml -f docker-compose.zoo.yml \
        --profile external-mints down -v >/dev/null 2>&1 || true
    rm -f zoo-mint-config.json
}
trap cleanup EXIT
echo "== mint matrix run complete"
