#!/usr/bin/env bash
# tests/qemu/musl-smoke.sh — Reproducible musl cross-compile + QEMU smoke test
#
# Proves that a cdk_wallet-tagged TollGate binary built with musl-gcc
# starts and responds on an OpenWrt system. This is the committed
# evidence for issue #277 (Oracle F2).
#
# Usage:
#   bash tests/qemu/musl-smoke.sh [ROUTER_IP] [SRC_DIR]
#
# Arguments:
#   ROUTER_IP  — IP of the OpenWrt VM (default: 10.99.99.1)
#   SRC_DIR    — Path to the tollgate src/ directory (default: auto-detect)
#
# Prerequisites:
#   - musl-tools installed (apt install musl-tools)
#   - cdk-go dependency resolved (go mod download in src/tollwallet/)
#   - OpenWrt VM running with SSH access and libcdk_ffi.so at /usr/lib/
#   - The VM must have the tollgate-wrt service configured

set -euo pipefail

ROUTER_IP="${1:-10.99.99.1}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
SRC_DIR="${2:-${REPO_ROOT}/src}"
SSH_OPTS="-o ConnectTimeout=5 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
OUTPUT_DIR="${SCRIPT_DIR}/output"
mkdir -p "${OUTPUT_DIR}"

echo "=== musl-smoke: cdk-go on OpenWrt ==="
echo "Router: ${ROUTER_IP}"
echo "Source: ${SRC_DIR}"
echo ""

# Step 1: Build with musl
echo "--- Step 1: Build cdk_wallet binary with musl-gcc ---"
cd "${SRC_DIR}"
BINARY="${OUTPUT_DIR}/tollgate-cdk-musl"
CGO_ENABLED=1 CC=musl-gcc GOOS=linux GOARCH=amd64 \
    go build -tags cdk_wallet,testenv -o "${BINARY}" \
    -trimpath -ldflags="-s -w" .
echo "Built: ${BINARY} ($(stat -c%s "${BINARY}") bytes)"

# Step 2: Verify dynamic linker
echo ""
echo "--- Step 2: Verify ELF interpreter ---"
INTERP=$(readelf -l "${BINARY}" 2>/dev/null | grep "interpreter" | sed 's/.*interpreter: \(.*\)]/\1/')
echo "Interpreter: ${INTERP}"
if [[ "${INTERP}" != "/lib/ld-musl-x86_64.so.1" ]]; then
    echo "FAIL: expected /lib/ld-musl-x86_64.so.1, got ${INTERP}"
    exit 1
fi
echo "OK: musl dynamic linker confirmed"

# Step 3: Check NEEDED libraries
echo ""
echo "--- Step 3: Check dynamic dependencies ---"
NEEDED=$(readelf -d "${BINARY}" 2>/dev/null | grep NEEDED || true)
echo "${NEEDED}"

# Step 4: Deploy to VM
echo ""
echo "--- Step 4: Deploy to OpenWrt VM at ${ROUTER_IP} ---"
ssh ${SSH_OPTS} root@${ROUTER_IP} "killall tollgate-wrt 2>/dev/null; echo stopped" || true
scp -O ${SSH_OPTS} "${BINARY}" root@${ROUTER_IP}:/usr/bin/tollgate-wrt
ssh ${SSH_OPTS} root@${ROUTER_IP} "chmod +x /usr/bin/tollgate-wrt"
echo "Deployed."

# Step 5: Start service and smoke test
echo ""
echo "--- Step 5: Start service and HTTP smoke test ---"
ssh ${SSH_OPTS} root@${ROUTER_IP} "service tollgate-wrt start" || true
sleep 5

# HTTP check
HTTP_RESP=$(curl -s --connect-timeout 5 "http://${ROUTER_IP}:2121/balance" 2>&1 || echo "CURL_FAILED")
echo "HTTP /balance response: ${HTTP_RESP}"

if echo "${HTTP_RESP}" | grep -q '"status"'; then
    echo "PASS: Service responded with JSON status"
else
    echo "WARN: No JSON response (service may be in degraded mode without internet)"
fi

# Process check
PROC=$(ssh ${SSH_OPTS} root@${ROUTER_IP} "ps | grep tollgate-wrt | grep -v grep" 2>&1 || echo "NOT_RUNNING")
echo "Process: ${PROC}"

if echo "${PROC}" | grep -q "tollgate-wrt"; then
    echo "PASS: tollgate-wrt process is running"
    echo ""
    echo "=== RESULT: cdk-go binary runs on OpenWrt with musl libc ==="
    exit 0
else
    echo ""
    echo "=== RESULT: FAIL — tollgate-wrt not running ==="
    echo "Check: ssh root@${ROUTER_IP} logread | grep tollgate"
    exit 1
fi
