#!/usr/bin/env bash
# regenerate the environment-recon log (raw/01-env-recon.txt) for the T5c re-verification
set -uo pipefail
cd "$(dirname "$0")"
{
  echo "=== env recon $(date -u +%FT%TZ) ==="
  uname -a
  echo "--- cpu ---"
  grep -m1 'model name' /proc/cpuinfo
  echo "cores: $(grep -c ^processor /proc/cpuinfo)"
  echo "loadavg: $(cat /proc/loadavg)"
  echo "--- go ---"
  go version
  echo "GOROOT=$(go env GOROOT)"
  echo "GOMODCACHE=$(go env GOMODCACHE)"
  echo "--- rust ---"
  rustc -V
  cargo -V
  echo "toolchains: $(rustup toolchain list | tr '\n' ' ')"
  echo "--- docker (the card body says docker is stubbed/broken; probe it) ---"
  docker info 2>&1 | grep -E 'Server Version|Storage Driver|Cannot connect|ERROR' || echo "(docker info failed)"
  echo "--- cdk-go v0.17.3 prebuilt native libs ---"
  ls -1 "$(go env GOMODCACHE)/github.com/cashubtc/cdk-go@v0.17.3/bindings/cdkffi/native/"
  echo "--- cdk-go v0.17.3 per-platform link stanzas (which platforms can link at all) ---"
  ls -1 "$(go env GOMODCACHE)/github.com/cashubtc/cdk-go@v0.17.3/bindings/cdkffi/" | grep -E '^link_'
} 2>&1 | tee raw/01-env-recon.txt
echo "wrote raw/01-env-recon.txt"
