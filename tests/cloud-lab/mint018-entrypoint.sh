#!/bin/bash
# mint018-entrypoint.sh — SCRATCH-ADDITION rc1-866 (disclosed).
# cdk-mintd 0.18.x ignores CDK_MINTD_* env vars at startup
# (database-authoritative configuration). This shim renders the equivalent
# settings document once from the same env vars the 0.17.6 lab daemon
# consumed, validates it, config-inits the work dir (the /data volume), and
# execs the daemon — giving compose the same UX as the 0.17.6 image,
# including across container restarts (marker file in the volume).
set -eu

WORK="${CDK_MINTD_WORK_DIR:-/data}"
PORT="${CDK_MINTD_LISTEN_PORT:-8085}"
HOST="${CDK_MINTD_LISTEN_HOST:-0.0.0.0}"
URL="${CDK_MINTD_URL:-http://127.0.0.1:${PORT}}"

# 0.18 refuses literal secrets in the document: reference the env var the
# 0.17.6 compose already sets; the daemon resolves it at startup.
MNEMONIC_LINE=""
[ -n "${CDK_MINTD_MNEMONIC:-}" ] && MNEMONIC_LINE="mnemonic = \"env:CDK_MINTD_MNEMONIC\""
FEE_LINE=""
[ -n "${CDK_MINTD_INPUT_FEE_PPK:-}" ] && FEE_LINE="input_fee_ppk = ${CDK_MINTD_INPUT_FEE_PPK}"

mkdir -p "$WORK"

if [ ! -f "$WORK/.cdk018-init" ]; then
    cat > /tmp/mint.toml <<EOF
[info]
url = "${URL}"
listen_host = "${HOST}"
listen_port = ${PORT}
${MNEMONIC_LINE}
${FEE_LINE}

[mint_info]
name = "cdk-lab-018"
description = "cloud-lab cdk-mintd 0.18.1 (lane rc1-866)"

[payment_backend]
backend = "fakewallet"

[fake_wallet]

[database]
engine = "sqlite"
EOF
    cdk-mintd config validate --file /tmp/mint.toml
    cdk-mintd --work-dir "$WORK" config init --new-mint --file /tmp/mint.toml
    touch "$WORK/.cdk018-init"
    echo "mint018: settings document initialized in ${WORK}"
fi

exec cdk-mintd --work-dir "$WORK"
