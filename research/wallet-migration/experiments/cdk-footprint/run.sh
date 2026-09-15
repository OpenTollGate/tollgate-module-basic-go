#!/usr/bin/env bash
# Spike C — build a WALLET-ONLY CDK probe for aarch64 + mipsel musl and report
# stripped sizes, proving the mipsel path with an upstreamable portable-atomic
# fix (drop nostr; route cdk-common's test-helper counters through
# portable-atomic). See ./output.txt and ./env.txt.
#
# Requires: docker, host rustup (stable for aarch64, nightly + rust-src for the
# mipsel -Z build-std), and the OpenWrt SDK images.
#
# Layout: expects a CDK checkout WITH our probe crate at crates/cdk-probe/ and
# the cdk-common patch already applied (see cdk-common-portable-atomic.patch).
set -euo pipefail

WORK="${WORK:-/home/c03rad0r/r2-work}"
CDK="${CDK:-$WORK/cdk}"
SDK_A="${SDK_A:-openwrt/sdk:mediatek-filogic-v25.12.5}"
SDK_M="${SDK_M:-openwrt/sdk:ramips-mt7621-v25.12.5}"
TC_A=/builder/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
TC_M=/builder/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin
RUST_STABLE=/builder/.rustup/toolchains/stable-x86_64-unknown-linux-gnu/bin
RUST_NIGHTLY=/builder/.rustup/toolchains/nightly-x86_64-unknown-linux-gnu/bin

echo "=== aarch64 (size-optimised floor) ==="
docker run --rm -v "$WORK:/work" -v "$HOME/.rustup:/builder/.rustup" -v "$HOME/.cargo:/builder/.cargo" \
  -w /work/cdk -e HOME=/builder -e CARGO_HOME=/builder/.cargo -e RUSTUP_HOME=/builder/.rustup -e STAGING_DIR=/builder/staging_dir \
  -e CARGO_TARGET_DIR=/work/cdk/target-fpmin \
  -e CC_aarch64_unknown_linux_musl=$TC_A/aarch64-openwrt-linux-musl-gcc \
  -e AR_aarch64_unknown_linux_musl=$TC_A/aarch64-openwrt-linux-musl-gcc-ar \
  -e CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=$TC_A/aarch64-openwrt-linux-musl-gcc \
  -e RUSTFLAGS="-C panic=abort" "$SDK_A" bash -c "
    export PATH=$RUST_STABLE:\$PATH
    cargo build -p cdk-probe --release --target aarch64-unknown-linux-musl --profile release-smaller
    $TC_A/aarch64-openwrt-linux-musl-strip --strip-all /work/cdk/target-fpmin/aarch64-unknown-linux-musl/release/cdk-probe
    ls -la /work/cdk/target-fpmin/aarch64-unknown-linux-musl/release/cdk-probe"

echo "=== mipsel (needs -Z build-std) ==="
docker run --rm -v "$WORK:/work" -v "$HOME/.rustup:/builder/.rustup" -v "$HOME/.cargo:/builder/.cargo" \
  -w /work/cdk -e HOME=/builder -e CARGO_HOME=/builder/.cargo -e RUSTUP_HOME=/builder/.rustup -e STAGING_DIR=/builder/staging_dir \
  -e CARGO_TARGET_DIR=/work/cdk/target-mipsel \
  -e CC_mipsel_unknown_linux_musl=$TC_M/mipsel-openwrt-linux-musl-gcc \
  -e AR_mipsel_unknown_linux_musl=$TC_M/mipsel-openwrt-linux-musl-gcc-ar \
  -e CARGO_TARGET_MIPSEL_UNKNOWN_LINUX_MUSL_LINKER=$TC_M/mipsel-openwrt-linux-musl-gcc \
  -e RUSTFLAGS="-C panic=abort" "$SDK_M" bash -c "
    export PATH=$RUST_NIGHTLY:\$PATH
    cargo build -p cdk-probe --release --target mipsel-unknown-linux-musl -Z build-std=std,panic_abort
    $TC_M/mipsel-openwrt-linux-musl-strip --strip-all /work/cdk/target-mipsel/mipsel-unknown-linux-musl/release/cdk-probe
    ls -la /work/cdk/target-mipsel/mipsel-unknown-linux-musl/release/cdk-probe"
