#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SRC_DIR="${REPO_ROOT}/src"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

GIT_SHA=$(cd "${REPO_ROOT}" && git rev-parse --short HEAD)
GIT_BRANCH=$(cd "${REPO_ROOT}" && git branch --show-current)

build_stripped() {
    local label="$1" output="$2" tags="$3"
    echo "Building ${label}..." >&2
    (cd "${SRC_DIR}" && go build -tags "${tags}" -o "${output}" .) 2>&1 >&2
    cp "${output}" "${output}.s"
    strip "${output}.s" 2>/dev/null || true
    stat -c%s "${output}.s"
}

G=$(build_stripped "gonuts" "${TMP_DIR}/tg-gonuts" "testenv")
C=$(build_stripped "cdk_wallet" "${TMP_DIR}/tg-cdk" "testenv,cdk_wallet")
GM=$(awk "BEGIN{printf \"%.2f\",${G}/1048576}")
CM=$(awk "BEGIN{printf \"%.2f\",${C}/1048576}")
D=$((C-G))
DM=$(awk "BEGIN{printf \"%+.2f\",${D}/1048576}")
P=$(awk "BEGIN{printf \"%+.1f\",(${C}-${G})*100/${G}}")

echo "=== TollGate Wallet Backend Size Report ==="
echo "Branch: ${GIT_BRANCH}  Commit: ${GIT_SHA}"
echo ""
printf "%-22s %12s %8s\n" "Variant" "bytes" "MB"
printf "%-22s %12s %8s\n" "----------------------" "------------" "--------"
printf "%-22s %12s %8s\n" "Default (gonuts)" "${G}" "${GM}"
printf "%-22s %12s %8s\n" "cdk_wallet (stub)" "${C}" "${CM}"
echo ""
echo "Delta: ${D} bytes (${DM} MB, ${P}%)"
echo ""
echo "NOTE: cdk_wallet uses a STUB (no cdk-go linked). Real CdkWallet"
echo "adds ~29MB raw / ~17MB UPX'd (libcdk_ffi.so). See issue #271."
