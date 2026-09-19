#!/usr/bin/env bash
# Re-derive every headline number in FINDINGS-2026-09-19.md from the COMMITTED
# raw logs (i.e. from the branch, not from the author's scratch dir).
set -uo pipefail
cd "$(dirname "$0")"
W=/home/c03rad0r/worktrees/t_7a133499/raw

echo "=== are the two gz logs whitespace-only different from the live run? ==="
for f in 14-service-link-mipsel-musl mipsel-musl-link; do
  n=$(diff <(zcat "$f.txt.gz" | sed 's/[[:space:]]*$//') <(sed 's/[[:space:]]*$//' "$W/$f.txt") | wc -l)
  echo "$f: diff-after-stripping-trailing-space = $n line(s)"
done

echo
echo "=== headline numbers, re-derived from the committed files ==="
echo "aarch64 service link, distinct undefined symbols : $(grep -o 'undefined reference to .[^ ]*' 14-service-link-aarch64-musl.txt | sort -u | wc -l)"
echo "aarch64 service link, GLIBC versions referenced  : $(grep -o '@GLIBC_[0-9.]*' 14-service-link-aarch64-musl.txt | sort -u | tr '\n' ' ')"
echo "mipsel  service link, distinct undefined symbols : $(zcat 14-service-link-mipsel-musl.txt.gz | grep -o 'undefined reference to .[^ ]*' | sort -u | wc -l)"
echo "mipsel  service link, GLIBC refs (expect none)   : $(zcat 14-service-link-mipsel-musl.txt.gz | grep -c '@GLIBC' )"
echo "aarch64 baselines (no cdk tag) exit / size       : $(grep -o 'exit=0' 17-service-link-aarch64-musl-baseline.txt) / $(grep -o '16770936 bytes' 17-service-link-aarch64-musl-baseline.txt || file /home/c03rad0r/worktrees/t_7a133499/out/tollgate-wrt-aarch64-musl-baseline | grep -o '16770936' )"
echo "mipsel  baseline size                            : $(grep -o 'PRODUCED: 18073276 bytes' 17-service-link-baseline-summary.txt)"
echo "cdylib dropped by default (probe)                : $(grep -c 'dropping unsupported crate type' 11-rust-cdylib-aarch64-musl.txt) warning(s)"
echo "cdk-ffi musl build verdict                       : $(grep -o 'Finished .release. profile .optimized. target(s) in [0-9a-z ]*' 12-cdkffi-aarch64-musl-build.txt)"
echo "musl .so stripped size                           : $(grep -o 'stripped: [0-9]* bytes' 19-cdkffi-aarch64-musl-artifact.txt)"
echo "musl .so NEEDED                                  : $(grep -A2 'readelf -d (musl' 19-cdkffi-aarch64-musl-artifact.txt | grep -o 'lib[a-z0-9_.]*\.so[.0-9]*' | tr '\n' ' ')"
echo "patched link exit                                : $(grep -o 'exit=[0-9]*' 18-patched-link-probe.txt | head -1)"
echo "patched link binary size                         : $(grep -o 'PRODUCED: [0-9]* bytes' 18-patched-link-probe.txt)"
echo "patched link RPATH into build machine            : $(grep -c 'RPATH.*cdk-go-musl' 18-patched-link-probe.txt) hit(s)"
echo "mipsel full-graph blocker                        : $(grep -o 'could not compile .nostr-relay-pool. (lib) due to 3 previous errors' 20-mipsel-cdkffi-check.txt)"
echo "mipsel full-graph exit                           : $(grep -o 'EXIT=101' 20-mipsel-cdkffi-check.txt | head -1)"
echo "rustup tier-3 refusal                            : $(grep -o 'no prebuilt artifacts available for target' 04-rustup-targets.txt) ... $(grep -o 'low-tier target' 04-rustup-targets.txt)"
echo "prebuilt native dirs (no musl, no mips)          : $(sed -n '/prebuilt cdk-go native libs/,/link_/p' 02-prebuilt-libs.txt | grep -c 'native\|linux_\|darwin\|windows') lines"
echo "false green at src level                         : exit=$(grep -o 'EXIT=0' 05-false-green.txt | head -1)  nested-module proof: $(grep -c 'does not contain package' 05-false-green.txt)"
