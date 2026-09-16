#!/usr/bin/env bash
# T5 — flash budget for the wallet backends.
#
# Measures the *installed* sizes that matter to the sidecar-vs-embed decision:
#   1. local cross-built artifacts (CDK daemon both arches, nucula harness);
#   2. published tollgate-wrt package sizes (the service that stays on device);
#   3. optionally, on-device free space (`ROUTER=root@host ./measure.sh`).
#
# The decision uses the *marginal* cost: an in-process backend adds 0 bytes,
# a sidecar adds its whole binary (+ its runtime RSS, measured elsewhere).
set -euo pipefail

WORK="${WORK:-/home/c03rad0r/r2-work}"
CDK="$WORK/cdk-upstream"
FEED_RELEASE="${FEED_RELEASE:-v0.6.0-alpha2-pre3}"
FEED_REPO="${FEED_REPO:-FreedomTechFeed/packages}"

hr() { awk "BEGIN{printf \"%.2f MiB (%d B)\n\", $1/1048576, $1}"; }
size_of() { [ -f "$1" ] && hr "$(stat -c%s "$1")" || echo "missing"; }

echo "== local cross-built artifacts =="
echo "cdk-walletd aarch64 : $(size_of "$CDK/target-walletd-aarch64/aarch64-unknown-linux-musl/release-smaller/cdk-walletd")"
echo "cdk-walletd mipsel  : $(size_of "$CDK/target-walletd-mipsel/mipsel-unknown-linux-musl/release-smaller/cdk-walletd")"
echo "nucula harness aarch64: $(size_of "$WORK/nucula/deps/out-aarch64/nucula_core_harness")"
echo "nucula harness mipsel : $(size_of "$WORK/nucula/deps/out-mipsel/nucula_core_harness")"

echo
echo "== published tollgate-wrt package sizes ($FEED_RELEASE) =="
if command -v gh >/dev/null 2>&1; then
  gh release view "$FEED_RELEASE" --repo "$FEED_REPO" --json assets \
    -q '.assets[] | "\(.name) \(.size)"' 2>/dev/null | grep -E "mipsel_24kc|aarch64_cortex-a53" || \
    echo "(gh release unavailable)"
else
  echo "(gh not installed)"
fi

echo
echo "== in-process service (gonuts, for reference) =="
echo "tollgate-wrt binary (aarch64, installed): 12081696 B / 11.52 MiB  [03-baseline/gonuts.md]"
echo "tollgate CLI binary (aarch64, installed) :  7146944 B /  6.82 MiB  [03-baseline/gonuts.md]"

if [[ -n "${ROUTER:-}" ]]; then
  echo
  echo "== on-device: $ROUTER =="
  ssh -o BatchMode=yes -o ConnectTimeout=6 "$ROUTER" '
    echo "--- flash / overlay free space ---"; df -h / /overlay /tmp 2>/dev/null
    echo "--- tollgate binary sizes ---"; ls -la /usr/bin/tollgate-wrt /usr/bin/tollgate 2>/dev/null
    echo "--- installed tollgate package ---"; opkg list-installed 2>/dev/null | grep tollgate || apk info 2>/dev/null | grep tollgate
  ' || echo "(router unreachable)"
else
  echo
  echo "(set ROUTER=root@<ip> to capture on-device df/installed sizes)"
fi
