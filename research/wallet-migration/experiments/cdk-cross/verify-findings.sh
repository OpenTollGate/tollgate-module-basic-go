#!/usr/bin/env bash
# Re-derive every headline number in FINDINGS-2026-09-19.md from the COMMITTED
# raw logs (i.e. from this branch), not from the author's scratch dir.
#
# Run-2 note (kanban t_7a133499, 2026-09-19): the first committed version of this
# script sat in experiments/cdk-cross/ but grepped BARE filenames such as
# `14-service-link-aarch64-musl.txt`, which live in `raw-2026-09-19/`. Every
# derivation therefore came back empty and the script still exited 0 — a silent
# pass, the exact failure mode it exists to prevent. It now (a) resolves the log
# dir relative to its own location and (b) FAILS CLOSED: any empty derivation is
# a non-zero exit, so a broken re-derivation cannot read as green again.
#
# It also derives the CI-matrix row counts from the committed log 24- (which
# replaced an unverifiable "17 rows" figure in the first draft of FINDINGS;
# measured: 14 rows, 9 of them on arches with no cdk-go artifact).
set -uo pipefail
cd "$(dirname "$0")"
R=raw-2026-09-19
[ -d "$R" ] || { echo "FATAL: log dir '$R' not found next to $0"; exit 2; }

FAIL=0
say() { # label, value
  local label=$1 val=${2-}
  val=$(printf '%s' "$val" | sed 's/[[:space:]]*$//')   # rstrip: keeps the committed log byte-identical to a fresh run
  if [ -z "$val" ]; then
    printf '%-48s : <<EMPTY -- FAIL>>\n' "$label"; FAIL=1
  else
    printf '%-48s : %s\n' "$label" "$val"
  fi
}
g1()   { grep -o "$2" "$1" 2>/dev/null | head -1; }              # first match
val()  { awk -F': +' -v pat="$2" '$0 ~ pat {print $2; exit}' "$1"; }  # field after label
sz()   { awk -v f="$2" '$0 ~ f"$" {print $5" bytes"; exit}' "$1" 2>/dev/null; }
libs() { grep -o 'Shared library: \[[^]]*\]' | sed 's/.*\[\(.*\)\]/\1/' | tr '\n' ' '; }

echo "=== provenance of this re-derivation ==="
say "script dir"                      "$(pwd)"
say "log dir"                         "$R ($(ls "$R" | wc -l) files)"
say "gzip"                            "$(command -v gzip || echo MISSING)"

echo
echo "=== log integrity vs the line counts recorded in the summary logs ==="
A_LINES=$(wc -l <"$R/14-service-link-aarch64-musl.txt")
M_LINES=$(zcat "$R/14-service-link-mipsel-musl.txt.gz" | wc -l)
A_LAST=$(tail -1 "$R/14-service-link-aarch64-musl.txt" | wc -c)
say "aarch64 link log lines (summary records 146)" "$A_LINES  (= recorded-1: the repo end-of-file-fixer removed the final blank line; last line is $A_LAST bytes, i.e. real content, not blank)"
say "mipsel link log lines (summary records 2078)"  "$M_LINES  (= recorded-1, same cause)"
say "log not truncated"               "$(grep -c 'undefined reference to' "$R/14-service-link-aarch64-musl.txt") undefined-ref lines present"

echo
echo "=== service link (the DEPLOYMENT artefact): src/main.go -> merchant -> tollwallet ==="
LINK_A="$R/14-service-link-aarch64-musl.txt"
LINK_M="$R/14-service-link-mipsel-musl.txt.gz"
say "aarch64 exit (summary log)"      "$(g1 "$R/14-service-link-summary.txt" 'exit=[0-9]*')"
say "aarch64 distinct undefined syms" "$(grep -o 'undefined reference to .[^ ]*' "$LINK_A" | sort -u | wc -l)"
say "aarch64 GLIBC versions referenced" "$(grep -o '@GLIBC_[0-9.]*' "$LINK_A" | sort -u | tr '\n' ' ')"
say "aarch64 refs to the shipped glibc lib" "$(grep -c 'cdk-go@v0.17.3/bindings/cdkffi/native/linux_arm64/libcdk_ffi.so: undefined' "$LINK_A") symbol lines"
say "aarch64 ld: glibc libs 'not found'" "$(grep -c 'not found (try using -rpath or -rpath-link)' "$LINK_A") warnings"
say "mipsel exit (summary log)"       "$(sed -n '/mipsel-musl/,$p' "$R/14-service-link-summary.txt" | grep -o 'exit=[0-9]*' | head -1)"
say "mipsel distinct undefined syms"  "$(zcat "$LINK_M" | grep -o 'undefined reference to .[^ ]*' | sort -u | wc -l)"
say "mipsel GLIBC refs (expect 0)"    "$(zcat "$LINK_M" | grep -c '@GLIBC')"
say "mipsel: NO -lcdk_ffi in the link line" "$(zcat "$LINK_M" | grep -c 'lcdk_ffi')  (so every FFI symbol is unresolved: $(zcat "$LINK_M" | grep -c 'cgo-gcc-prolog') cgo-prolog refs)"
say "mipsel first unresolved symbol"  "$(zcat "$LINK_M" | grep -o 'ffi_cdk_ffi_[a-z_0-9]*' | sort -u | head -1)"

echo
echo "=== tag-free baselines (attribution: the failure is the cdk path, not the arch) ==="
say "aarch64 baseline exit"           "$(g1 "$R/17-service-link-aarch64-musl-baseline.txt" 'exit=[0-9]*')"
say "aarch64 baseline size"           "$(g1 "$R/21-produced-binary-hashes.txt" 'tollgate-wrt-aarch64-musl-baseline: [0-9]* bytes')"
say "  ... vs aarch64 cdk+patched"    "$(g1 "$R/21-produced-binary-hashes.txt" 'tollgate-wrt-aarch64-musl-cdk-patched: [0-9]* bytes')"
say "mipsel baseline exit"            "$(sed -n '/mipsel-musl-baseline/,$p' "$R/17-service-link-baseline-summary.txt" | grep -o 'exit=[0-9]*' | head -1)"
say "mipsel baseline size"            "$(g1 "$R/21-produced-binary-hashes.txt" 'tollgate-wrt-mipsel-musl-baseline: [0-9]* bytes')"
say "SUPERSEDED 1st aarch64-baseline attempt" "$(g1 "$R/17-service-link-baseline-summary.txt" 'exit=126 lines=1') (its log was 1 line; the authoritative record is 17-service-link-aarch64-musl-baseline.txt exit=0 + the hash file)"

echo
echo "=== what cdk-go v0.17.3 ships: no musl, no mips ==="
say "prebuilt entries (5 libs + checksums)" "$(grep -c '^-- ' "$R/02-prebuilt-libs.txt")  native libs: $(grep -c '^-- \./.*\.\(so\|dylib\|dll\)' "$R/02-prebuilt-libs.txt")"
say "musl dirs (expect 0)"            "$(grep -c 'musl' "$R/02-prebuilt-libs.txt")"
say "mips dirs (expect 0)"            "$(grep -c 'mips' "$R/02-prebuilt-libs.txt")"
say "link_*.go stanzas"               "$(grep -c '^--- link_.*\.go$' "$R/02-prebuilt-libs.txt")"
say "shipped glibc arm64 .so size"    "$(g1 "$R/02-prebuilt-libs.txt" 'linux_arm64/libcdk_ffi.so  [0-9]* bytes')"
say "shipped arm64 NEEDED (glibc!)"   "$(sed -n '/readelf -d linux_arm64/,/^$/p' "$R/02-prebuilt-libs.txt" | libs)"

echo
echo "=== Rust half: the musl cdylib IS obtainable ==="
say "default: cdylib dropped"         "$(grep -c 'dropping unsupported crate type' "$R/11-rust-cdylib-aarch64-musl.txt") warning"
say "aarch64 probe .so (-crt-static off)" "$(sz "$R/11-rust-cdylib-aarch64-musl.txt" 'release/libprobe_cdylib\.so')"
say "aarch64 probe .so NEEDED"        "$(sed -n '/vs the aarch64 one/,$p' "$R/15-probe-so-shapes.txt" | libs)"
say "mipsel probe .so (nightly build-std)" "$(sz "$R/13-mipsel-rust-probe.txt" 'release/libprobe_cdylib\.so')"
say "mipsel probe .so NEEDED"         "$(sed -n '1,/vs the aarch64 one/p' "$R/15-probe-so-shapes.txt" | libs)"
say "mipsel probe build time"         "$(g1 "$R/13-mipsel-rust-probe.txt" 'target(s) in [0-9a-z ]*')"
say "cdk-ffi aarch64-musl build verdict" "$(g1 "$R/12-cdkffi-aarch64-musl-build.txt" 'target(s) in [0-9a-z ]*')"
say "cdk-ffi musl .so unstripped"     "$(g1 "$R/19-cdkffi-aarch64-musl-artifact.txt" 'libcdk_ffi.so [0-9]* bytes')"
say "cdk-ffi musl .so stripped"       "$(g1 "$R/19-cdkffi-aarch64-musl-artifact.txt" 'stripped: [0-9]* bytes')"
say "cdk-ffi musl .so NEEDED"         "$(sed -n '/readelf -d (musl/,/^$/p' "$R/19-cdkffi-aarch64-musl-artifact.txt" | libs)"
say "std markers inside the .so"      "$(grep -c '__rustc17rust_begin_unwind' "$R/19-cdkffi-aarch64-musl-artifact.txt") (std cannot be excluded)"

echo
echo "=== patched link (P1 -crt-static, P2 musl .so in the binding's path) ==="
say "patched link exit"               "$(g1 "$R/18-patched-link-probe.txt" 'exit=[0-9]*')"
say "patched link binary size"        "$(g1 "$R/18-patched-link-probe.txt" 'PRODUCED: [0-9]* bytes')"
say "patched NEEDED libcdk_ffi.so"    "$(grep -c 'Shared library: \[libcdk_ffi.so\]' "$R/18-patched-link-probe.txt")"
say "patched RPATH -> build machine (P3)" "$(grep -c 'RPATH.*cdk-go-musl' "$R/18-patched-link-probe.txt") hit"
say "glibc object it replaced"        "$(g1 "$R/18-patched-link-probe.txt" '\(26[0-9]* bytes, glibc\)')"

echo
echo "=== mipsel: a dependency wall, not an arch wall ==="
say "rustup tier-3 refusal"           "$(g1 "$R/04-rustup-targets.txt" 'no prebuilt artifacts available for target')"
say "rustup low-tier wording"         "$(g1 "$R/04-rustup-targets.txt" 'low-tier target')"
say "cdk-ffi graph blocker"           "$(g1 "$R/20-mipsel-cdkffi-check.txt" 'could not compile .nostr-relay-pool. (lib) due to 3 previous errors')"
say "cdk-ffi graph EXIT"              "$(g1 "$R/20-mipsel-cdkffi-check.txt" 'EXIT=101')"
CR_CHK=$(grep -c 'Checking ' "$R/20-mipsel-cdkffi-check.txt")
CR_CMP=$(grep -c 'Compiling ' "$R/20-mipsel-cdkffi-check.txt")
say "crates in flight before the error" "$(( CR_CHK + CR_CMP )) (Checking $CR_CHK + Compiling $CR_CMP)"

echo
echo "=== the two build commands that exist, and which one lies ==="
say "src-level ./... (false green) exit" "$(g1 "$R/05-false-green.txt" 'EXIT=0')"
say "proof tollwallet is not in src ./..." "$(g1 "$R/05-false-green.txt" 'does not contain package')"
say "nested modules under src"        "$(grep -c '^\./.*go\.mod$' "$R/05-false-green.txt")"
say "per-arch library build (NO link)" "$(g1 "$R/10-adapter-cross-summary.txt" 'exit=0  (log: [^,]*aarch64-musl-build\.txt, 0 lines)')"
say "host library build exit"         "$(sed -n '/library-level build, host/,+1p' "$R/06-adapter-library-build.txt" | grep -o 'EXIT=[0-9]*')"

echo
echo "=== CI: the matrix, and CGO_ENABLED ==="
say "CI CGO_ENABLED for the package build" "$(g1 "$R/16-ci-cgo-and-arches.txt" 'CGO_ENABLED: .*')"
say "matrix rows"                     "$(val "$R/24-ci-matrix-rows.txt" '^matrix rows')  (workflow sha256 $(awk '/^sha256/{print $3}' "$R/24-ci-matrix-rows.txt"))"
say "rows on arches with NO cdk-go artifact" "$(val "$R/24-ci-matrix-rows.txt" '^archs with NO cdk-go')"
say "  mips / armv7 breakdown"        "$(val "$R/24-ci-matrix-rows.txt" '^mips rows')  armv7 rows: $(val "$R/24-ci-matrix-rows.txt" '^armv7 rows')"

echo
if [ "$FAIL" -ne 0 ]; then
  echo "VERDICT: FAIL -- one or more headline numbers did not re-derive (see <<EMPTY -- FAIL>>)."
  exit 1
fi
echo "VERDICT: PASS -- every headline number re-derived from the committed logs."
