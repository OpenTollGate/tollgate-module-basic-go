#!/usr/bin/env bash
# ESP32-C3 build (the real target) of both proof-store revisions, in one
# attributable run: the same ESP-IDF project and build directory, swapping only
# the two storage-layer files, recording the sha256 of the files actually
# compiled and the resulting nucula.bin for each.
#
# Part of the nucula persistence work. OUR script; no nucula source is copied
# into this repository. Point the variables at checkouts you obtained yourself:
#
#   BEFORE_SRC=/path/to/nucula-8ad0812          (upstream main)
#   AFTER_SRC=/path/to/nucula@feat-incremental  (the change)
#   IDF_PROJECT=/path/to/esp-idf/project        (CMakeLists.txt + sdkconfig +
#                                                main/; the port spike's tree)
#
# The project's main/ is swapped, never edited in place: the two files that are
# replaced are backed up at the start and restored at the end (success or not).
#
# Usage:
#   BEFORE_SRC=... AFTER_SRC=... IDF_PROJECT=... ./idf-before-after.sh [out-dir]
set -uo pipefail

BEFORE_SRC=${BEFORE_SRC:?set BEFORE_SRC to the unmodified nucula source root}
AFTER_SRC=${AFTER_SRC:?set AFTER_SRC to the nucula source root with the per-proof store}
IDF_PROJECT=${IDF_PROJECT:?set IDF_PROJECT to the native ESP-IDF project to build}
HERE=$(cd "$(dirname "$0")" && pwd)
OUT=${1:-$HERE/measurements/$(date -u +%Y%m%dT%H%M%SZ)}
case "$OUT" in /*) ;; *) OUT="$(pwd)/$OUT" ;; esac
mkdir -p "$OUT"

: "${IDF_PATH:?set IDF_PATH (or export it before running)}"
# shellcheck disable=SC1091
source "$IDF_PATH/export.sh" >/dev/null 2>&1 || exit 2
cd "$IDF_PROJECT" || exit 2

BAK=$(mktemp -d)
cleanup() {
  for f in wallet_nvs.cpp wallet_internal.hpp; do
    [ -f "$BAK/$f" ] && cp "$BAK/$f" "main/$f"
  done
  rm -rf "$BAK"
}
trap cleanup EXIT INT TERM

for f in wallet_nvs.cpp wallet_internal.hpp; do cp "main/$f" "$BAK/$f"; done

LOG="$OUT/40-idf-build.log"
{
  echo "# ESP-IDF build of both proof-store revisions, same project + build dir"
  echo "# date:         $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "# idf:          $(idf.py --version 2>/dev/null | tail -1)"
  echo "# idf_project:  $IDF_PROJECT"
  echo "# before_src:   $BEFORE_SRC"
  echo "# after_src:    $AFTER_SRC"
  { git -C "$BEFORE_SRC" rev-parse HEAD 2>/dev/null
    git -C "$AFTER_SRC"  rev-parse HEAD 2>/dev/null; } | sed 's/^/# git:          /'
} > "$LOG"

build() { # build <label> <source-root>
  local label=$1 src=$2
  echo "" >> "$LOG"
  echo "============ $label ============" >> "$LOG"
  cp "$src/main/wallet_nvs.cpp"      main/wallet_nvs.cpp
  cp "$src/main/wallet_internal.hpp" main/wallet_internal.hpp
  sha256sum main/wallet_nvs.cpp main/wallet_internal.hpp >> "$LOG"
  idf.py build >> "$LOG" 2>&1
  echo "IDF_BUILD_EXIT=$?" >> "$LOG"
  sha256sum build/nucula.bin >> "$LOG"
  stat -c "size=%s  %n" build/nucula.bin >> "$LOG"
  # The binaries themselves are NOT copied anywhere: the hash and size above
  # identify them, and they stay in the project's build/ directory (nucula is
  # unlicensed -- no nucula artifact belongs in this repository).
}

build "upstream-8ad0812"    "$BEFORE_SRC"
build "per-proof-dbd1eb0"   "$AFTER_SRC"

grep -hE 'binary size|IDF_BUILD_EXIT|^size=' "$LOG"
echo "log: $LOG"
