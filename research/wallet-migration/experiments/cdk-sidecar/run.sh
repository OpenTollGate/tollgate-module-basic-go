#!/usr/bin/env bash
# T5c — build cdk-cli as a static musl sidecar (aarch64). SUCCESS path.
# See ./output.txt for results and ./env.txt for the environment.
#
# Requires: docker + host rustup (stable with aarch64-unknown-linux-musl, and
# nightly + rust-src for the mipsel build-std attempt) + the SDK images.
set -euo pipefail

WORK="${WORK:-/tmp/r2-cdk-sidecar}"
SDK_AARCH64="${SDK_AARCH64:-openwrt/sdk:mediatek-filogic-v25.12.5}"
SDK_MIPSEL="${SDK_MIPSEL:-openwrt/sdk:ramips-mt7621-v25.12.5}"
TC_A=/builder/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
TC_M=/builder/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin

mkdir -p "$WORK"
if [ ! -d "$WORK/cdk" ]; then
  git clone --depth 1 --branch v0.17.3 https://github.com/cashubtc/cdk.git "$WORK/cdk"
fi

echo "=== aarch64: cargo build -p cdk-cli (static musl) ==="
docker run --rm \
  -v "$WORK:/work" -v "$HOME/.rustup:/builder/.rustup" -v "$HOME/.cargo:/builder/.cargo" \
  -w /work/cdk \
  -e HOME=/builder -e CARGO_HOME=/builder/.cargo -e RUSTUP_HOME=/builder/.rustup \
  -e STAGING_DIR=/builder/staging_dir \
  -e CC_aarch64_unknown_linux_musl=$TC_A/aarch64-openwrt-linux-musl-gcc \
  -e CXX_aarch64_unknown_linux_musl=$TC_A/aarch64-openwrt-linux-musl-g++ \
  -e AR_aarch64_unknown_linux_musl=$TC_A/aarch64-openwrt-linux-musl-gcc-ar \
  -e CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=$TC_A/aarch64-openwrt-linux-musl-gcc \
  -e RUSTFLAGS="-C panic=abort" \
  "$SDK_AARCH64" bash -c '
    export PATH=/builder/.rustup/toolchains/stable-x86_64-unknown-linux-gnu/bin:$PATH
    cargo build -p cdk-cli --release --target aarch64-unknown-linux-musl --no-default-features
    '"$TC_A"'/aarch64-openwrt-linux-musl-strip --strip-all \
      target/aarch64-unknown-linux-musl/release/cdk-cli
    ls -la target/aarch64-unknown-linux-musl/release/cdk-cli
  '

echo "=== mipsel: EXPECTED FAIL (AtomicU64 missing, tier-3 build-std) ==="
docker run --rm \
  -v "$WORK:/work" -v "$HOME/.rustup:/builder/.rustup" -v "$HOME/.cargo:/builder/.cargo" \
  -w /work/cdk \
  -e HOME=/builder -e CARGO_HOME=/builder/.cargo -e RUSTUP_HOME=/builder/.rustup \
  -e STAGING_DIR=/builder/staging_dir \
  -e CC_mipsel_unknown_linux_musl=$TC_M/mipsel-openwrt-linux-musl-gcc \
  -e CXX_mipsel_unknown_linux_musl=$TC_M/mipsel-openwrt-linux-musl-g++ \
  -e AR_mipsel_unknown_linux_musl=$TC_M/mipsel-openwrt-linux-musl-gcc-ar \
  -e CARGO_TARGET_MIPSEL_UNKNOWN_LINUX_MUSL_LINKER=$TC_M/mipsel-openwrt-linux-musl-gcc \
  -e RUSTFLAGS="-C panic=abort" \
  "$SDK_MIPSEL" bash -c '
    export PATH=/builder/.rustup/toolchains/nightly-x86_64-unknown-linux-gnu/bin:$PATH
    cargo build -p cdk-cli --release --target mipsel-unknown-linux-musl \
      --no-default-features -Z build-std=std,panic_abort
  '
