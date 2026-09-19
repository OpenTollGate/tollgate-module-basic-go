#!/usr/bin/env bash
# T5c — build the Rust half of the in-process cgo path for an OpenWrt router arch.
#
# The cgo adapter links `libcdk_ffi.so` (cdk-go's prebuilt native lib, glibc-only).
# This script asks the other question: can that shared object be produced from
# CDK's own source for a musl/DYNAMIC target, and at what size?
#
# Key measured fact (see 11-rust-cdylib-aarch64-musl.txt): on musl targets rustc
# defaults to `crt-static=on`, and then it SILENTLY DROPS the cdylib crate type
# ("warning: dropping unsupported crate type `cdylib`"). Passing
# `-C target-feature=-crt-static` is what makes a musl `.so` possible at all.
#
# Usage: bash run-cdkffi-rust-build.sh <cdk-checkout> <openwrt-sdk-dir> <outdir>
# Requires: rustup with the target's rust-std (aarch64-unknown-linux-musl is tier 2),
# an extracted OpenWrt 25.12.0 SDK, and network for the crates.io fetch.
set -uo pipefail

CDK=$(readlink -f "${1:?cdk checkout at the pinned tag}")
SDK=$(readlink -f "${2:?openwrt SDK dir}")
OUT=$(readlink -f "${3:?output dir}")
mkdir -p "$OUT"

TC=$SDK/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
TARGET=aarch64-unknown-linux-musl


export STAGING_DIR=$SDK/staging_dir
export CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER=$TC/aarch64-openwrt-linux-musl-gcc
export CC_aarch64_unknown_linux_musl=$TC/aarch64-openwrt-linux-musl-gcc
export CXX_aarch64_unknown_linux_musl=$TC/aarch64-openwrt-linux-musl-g++
export AR_aarch64_unknown_linux_musl=$TC/aarch64-openwrt-linux-musl-ar
export RANLIB_aarch64_unknown_linux_musl=$TC/aarch64-openwrt-linux-musl-ranlib
# the one flag that turns "dropping unsupported crate type `cdylib`" into a .so
export RUSTFLAGS="-C target-feature=-crt-static"

{
  echo "### env"
  echo "host        : $(uname -srm)"
  echo "rustc       : $(rustc -V)"
  echo "cargo       : $(cargo -V)"
  echo "cdk         : $(git -C "$CDK" rev-parse HEAD)  ($(git -C "$CDK" describe --tags --always))"
  echo "sdk         : $(basename "$SDK")"
  echo "toolchain   : $($TC/aarch64-openwrt-linux-musl-gcc --version | head -1)"
  echo "target      : $TARGET"
  echo "RUSTFLAGS   : $RUSTFLAGS"
  echo "CC_aarch64_unknown_linux_musl    : $CC_aarch64_unknown_linux_musl"
  echo "AR_aarch64_unknown_linux_musl    : $AR_aarch64_unknown_linux_musl"
  echo "cargo linker                     : $CARGO_TARGET_AARCH64_UNKNOWN_LINUX_MUSL_LINKER"
  echo
  date -u +"### build started %FT%TZ"
} >"$OUT/12-cdkffi-aarch64-musl-build.txt"

cd "$CDK" || exit 1
# The repo's rust-toolchain.toml pins a specific channel (1.96.0 at v0.17.3), and
# that pin OVERRIDES the default toolchain — so rust-std must be installed for the
# PINNED toolchain, not for `stable` (first run of this script failed with
# "can't find crate for `core`: the aarch64-unknown-linux-musl target may not be
# installed" for exactly this reason).
PINNED=$(sed -n 's/^channel *= *"\(.*\)"/\1/p' rust-toolchain.toml)
[ -n "$PINNED" ] && rustup target add "$TARGET" --toolchain "$PINNED" 2>&1 | tail -2
# --release mirrors cdk-go's rust/Cargo.toml [profile.release] (lto, strip, codegen-units=1)
cargo build -p cdk-ffi --release --target "$TARGET" \
  >>"$OUT/12-cdkffi-aarch64-musl-build.txt" 2>&1
rc=$?

{
  echo
  echo "### build finished $(date -u +%FT%TZ)  exit=$rc"
  echo "### artefacts in target/$TARGET/release/:"
  ls -la "target/$TARGET/release/" 2>/dev/null | grep -E '\.(so|a)$' || echo "(none)"
  for f in "target/$TARGET/release/libcdk_ffi.so" "target/$TARGET/release/libcdk_ffi.a"; do
    [ -f "$f" ] && echo "$f  $(stat -c%s "$f") bytes  sha256=$(sha256sum "$f" | cut -d' ' -f1)"
  done
} >>"$OUT/12-cdkffi-aarch64-musl-build.txt" 2>&1
echo "EXIT=$rc"
