#!/usr/bin/env bash
# Hosted-runner-independent package build for tollgate-wrt.
#
# Orders a short-lived SHC (Sovereign Hybrid Compute) VM, syncs the working
# tree (including uncommitted changes) to it, builds the aarch64 .ipk via
# packaging/local-build-ipk.sh, proves --version on the built binaries
# (natively for amd64, under qemu for the arm64 binaries extracted from the
# .ipk), copies the artifact back, and cancels the VM.
#
# Requires: SHC_API_KEY in env, shc CLI on PATH (github.com/Amperstrand/shc-toolkit),
# ssh, and a public key at SSH_PUB (default ~/.ssh/id_ed25519.pub).
#
# Usage:
#   bash scripts/shc-build-package.sh                 # defaults below
#   PKG_VERSION=v0.7.0-alpha11 SHC_SIZE=nvme-4c-16gb bash scripts/shc-build-package.sh
#
# Environment:
#   SHC_SIZE      VM size           (default: nvme-2c-8gb — Katy, tx; avoid ssd-*/dev-*)
#
# Zone guidance (verified 2026-09-12, from a Norway/EU route): SHC has two
# facilities. Katy, Texas hosts nvme-* (g4, default) and hdd-* (g8, ~6%
# cheaper) — both validated end-to-end by this script. Cherryvale, Kansas
# hosts ssd-*/dev-* (g7), flagged unreachable from EU routes (shc-toolkit
# issue #39); the shc CLI refuses those orders before submitting — do not
# use until SHC resolves the zone.#   SHC_TEMPLATE  OS template       (default: debian13)
#   SHC_REAP      reaper deadline   (default: 2h — backstop if this script dies)
#   SSH_PUB       public key path   (default: ~/.ssh/id_ed25519.pub)
#   GO_VERSION    Go toolchain      (default: 1.25.8 — must satisfy src/go.mod)
#   PKG_VERSION   embedded version  (default: v0.7.0-alpha10, as in local-build-ipk.sh)
#   OUT_DIR       artifact dir      (default: artifacts/shc)
set -euo pipefail

SHC_SIZE="${SHC_SIZE:-nvme-2c-8gb}"
SHC_TEMPLATE="${SHC_TEMPLATE:-debian13}"
SHC_REAP="${SHC_REAP:-2h}"
SSH_PUB="${SSH_PUB:-$HOME/.ssh/id_ed25519.pub}"
GO_VERSION="${GO_VERSION:-1.25.8}"
PKG_VERSION="${PKG_VERSION:-v0.7.0-alpha10}"
OUT_DIR="${OUT_DIR:-artifacts/shc}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

die() { printf 'ERROR: %s\n' "$1" >&2; exit 1; }
command -v shc >/dev/null 2>&1 || die "shc CLI not on PATH (pip install shc-toolkit)"
[ -n "${SHC_API_KEY:-}" ] || die "SHC_API_KEY not set"
[ -f "$SSH_PUB" ] || die "public key not found: $SSH_PUB"

VM_ID=""
cleanup() {
  if [ -n "$VM_ID" ]; then
    echo "--- cancelling VM $VM_ID"
    shc cancel "$VM_ID" >/dev/null 2>&1 || echo "WARNING: failed to cancel VM $VM_ID — cancel it manually (reaper backstop: $SHC_REAP)" >&2
  fi
}
trap cleanup EXIT

HOSTNAME_TAG="tollgate-build-$(date +%m%d%H%M)-$(git rev-parse --short HEAD)"
echo "=== [1/5] ordering VM: $SHC_SIZE ($SHC_TEMPLATE), reap $SHC_REAP ==="
ORDER_OUT="$(shc order --hostname "$HOSTNAME_TAG" --size "$SHC_SIZE" \
  --template "$SHC_TEMPLATE" --ssh-key "$SSH_PUB" --reap "$SHC_REAP" \
  --pay --verify-reachability)" || die "order failed"
VM_ID="$(printf '%s\n' "$ORDER_OUT" | sed -nE 's/.*Waiting for VM ([0-9]+).*/\1/p' | head -1)"
VM_IP="$(printf '%s\n' "$ORDER_OUT" | sed -nE 's/.*IP:[[:space:]]+([0-9.]+).*/\1/p' | head -1)"
VM_USER="$(printf '%s\n' "$ORDER_OUT" | sed -nE 's/.*User:[[:space:]]+([a-z]+).*/\1/p' | head -1)"
[ -n "$VM_ID" ] && [ -n "$VM_IP" ] && [ -n "$VM_USER" ] || die "could not parse order output:\n$ORDER_OUT"
echo "VM $VM_ID ready: $VM_USER@$VM_IP"

# SHC reuses IPs across orders: drop any stale known_hosts entry for this
# address so the fresh VM's new host key does not fail strict checking.
ssh-keygen -R "$VM_IP" >/dev/null 2>&1 || true

SSH_CMD=(ssh -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new "$VM_USER@$VM_IP")

echo "=== [2/5] syncing working tree ==="
git rev-parse --short HEAD > /tmp/.tollgate-head-sha.$$
tar czf - --exclude=./.git --exclude=./bin --exclude=./artifacts \
  --exclude='./packaging/*.ipk' . | "${SSH_CMD[@]}" 'rm -rf /tmp/tree && mkdir -p /tmp/tree && tar xzf - -C /tmp/tree'
scp -q /tmp/.tollgate-head-sha.$$ "$VM_USER@$VM_IP:/tmp/tree/.head-sha"
rm -f /tmp/.tollgate-head-sha.$$

echo "=== [3/5] remote build (local-build-ipk.sh, verbatim) ==="
"${SSH_CMD[@]}" "set -e
  # git rev-parse shim: the synced tree has no .git
  mkdir -p /tmp/shim
  printf '%s\n' '#!/bin/sh' 'for a in \"\$@\"; do case \"\$a\" in rev-parse) cat /tmp/tree/.head-sha; exit 0;; esac; done' 'exit 1' > /tmp/shim/git
  chmod +x /tmp/shim/git
  curl -sSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz -o /tmp/go.tgz
  sudo tar -C /usr/local -xzf /tmp/go.tgz
  export PATH=/usr/local/go/bin:/tmp/shim:\$PATH
  go version
  cd /tmp/tree
  bash packaging/local-build-ipk.sh"

echo "=== [4/5] proving --version on built binaries ==="
"${SSH_CMD[@]}" "set -e
  IPK=/tmp/tree/packaging/tollgate-wrt_${PKG_VERSION}_aarch64_cortex-a53.ipk
  [ -f \"\$IPK\" ] || { echo 'ipk missing' >&2; exit 1; }
  sudo apt-get update -qq >/dev/null 2>&1 || true
  sudo apt-get install -y -qq qemu-user-static binutils >/dev/null 2>&1 || true
  rm -rf /tmp/ipkx && mkdir -p /tmp/ipkx && cd /tmp/ipkx
  tar xzf \"\$IPK\"
  tar xzf ./control.tar.gz; tar xzf ./data.tar.gz
  echo '--- control:'; grep -E '^(Package|Version|Architecture|License):' ./control
  if command -v qemu-aarch64-static >/dev/null 2>&1; then
    qemu-aarch64-static usr/bin/tollgate --version
    qemu-aarch64-static usr/bin/tollgate-wrt --version
  else
    echo 'NOTE: qemu-user-static unavailable; skipping arm64 execution proof'
  fi
  sha256sum \"\$IPK\" /tmp/tree/bin/arm64/tollgate-wrt /tmp/tree/bin/arm64/tollgate"

echo "=== [5/5] fetching artifact ==="
mkdir -p "$OUT_DIR"
scp -q "$VM_USER@$VM_IP:/tmp/tree/packaging/tollgate-wrt_${PKG_VERSION}_aarch64_cortex-a53.ipk" "$OUT_DIR/"
sha256sum "$OUT_DIR/tollgate-wrt_${PKG_VERSION}_aarch64_cortex-a53.ipk"
echo "DONE: $OUT_DIR/tollgate-wrt_${PKG_VERSION}_aarch64_cortex-a53.ipk (VM $VM_ID cancelled by exit trap)"
