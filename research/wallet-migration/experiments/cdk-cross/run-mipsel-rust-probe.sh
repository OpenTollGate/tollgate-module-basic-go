#!/usr/bin/env bash
# T5c — mipsel-unknown-linux-musl, the arch that is Rust TIER 3.
#
# Three measurements, cheapest first:
#   1. can rustup install the target at all?  (prebuilt std availability)
#   2. can a MINIMAL cdylib be built for it with nightly `-Z build-std`?
#      (i.e. is std itself the blocker, or the dependency tree?)
#   3. what does CDK's real dependency graph do for this target?
#      run separately:  cargo +nightly check -Zbuild-std=std,panic_abort \
#                         --target mipsel-unknown-linux-musl -p cdk-ffi
#
# Usage: bash run-mipsel-rust-probe.sh <probe-crate-dir> <openwrt-mipsel-sdk-dir> <outdir>
set -uo pipefail

PROBE=$(readlink -f "${1:?probe crate dir}")
SDK=$(readlink -f "${2:?openwrt mipsel SDK dir}")
OUT=$(readlink -f "${3:?output dir}")
mkdir -p "$OUT"
TC=$SDK/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin

{
  echo "### mipsel-unknown-linux-musl Rust probe"
  echo "host        : $(uname -srm)"
  echo "rustc stable: $(rustc -V)"
  echo "rustc nightly: $(rustup run nightly rustc -V 2>&1)"
  echo "sdk         : $(basename "$SDK")"
  echo "toolchain   : $($TC/mipsel-openwrt-linux-musl-gcc --version | head -1)"
  echo
  echo "### 1. rustup target add mipsel-unknown-linux-musl"
  rustup target add mipsel-unknown-linux-musl 2>&1
  echo "exit=$?"
  echo
  echo "### 2. minimal cdylib, nightly -Z build-std=std,panic_abort"
} >"$OUT/13-mipsel-rust-probe.txt" 2>&1

cd "$PROBE" || exit 1
export STAGING_DIR=$SDK/staging_dir
export CARGO_TARGET_MIPSEL_UNKNOWN_LINUX_MUSL_LINKER=$TC/mipsel-openwrt-linux-musl-gcc
export CC_mipsel_unknown_linux_musl=$TC/mipsel-openwrt-linux-musl-gcc
export CXX_mipsel_unknown_linux_musl=$TC/mipsel-openwrt-linux-musl-g++
export AR_mipsel_unknown_linux_musl=$TC/mipsel-openwrt-linux-musl-ar
export RUSTFLAGS="-C target-feature=-crt-static"

{
  echo "RUSTFLAGS   : $RUSTFLAGS"
  echo "linker      : $CARGO_TARGET_MIPSEL_UNKNOWN_LINUX_MUSL_LINKER"
  echo
  # no prebuilt std for a tier-3 target: build-std is mandatory
  rustup run nightly cargo build --release --target mipsel-unknown-linux-musl \
    -Z build-std=std,panic_abort 2>&1
  echo "exit=$?"
  echo
  echo "### artefacts:"
  ls -la target/mipsel-unknown-linux-musl/release/*.so \
         target/mipsel-unknown-linux-musl/release/*.a 2>&1
} >>"$OUT/13-mipsel-rust-probe.txt" 2>&1
echo "see $OUT/13-mipsel-rust-probe.txt"
