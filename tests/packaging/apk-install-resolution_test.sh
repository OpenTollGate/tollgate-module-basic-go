#!/usr/bin/env bash
# apk-install-resolution smoke — prove the built package's dependency closure
# resolves against a stock OpenWrt apk feed set, on the tooling that actually
# reads 25.12 indexes (apk-tools 3; apk 2.x silently resolves nothing against
# these feeds — measured while diagnosing #552 — do not "simplify" this away).
#
# Usage:
#   APK=/path/to/tollgate-wrt*.apk bash tests/packaging/apk-install-resolution_test.sh
#
# Without docker the lane skips cleanly (exit 0) with a stated reason — the
# same contract as the other cloud-lab lanes. Without $APK it runs the
# feed-shape control (resolving nodogsplash, the #552 dependency) so the
# harness itself is always exercised.
#
# The container runs with --network host: apk3's fetcher intermittently
# attempts IPv6 and fails under the default docker bridge ("Operation not
# permitted"), which made the lane flap. The repositories written inside are
# the real https release URLs — the exact resolution path a stock router
# takes, signatures aside (--allow-untrusted; trust is not this lane's claim).
#
# What it proves, and what it deliberately does not:
#   - proves: every dependency name the package declares is selectable from
#     the standard 25.12.x arch feed set (base+packages+routing+luci+
#     telephony) plus the x86/64 target feed, with apk3 semantics — including
#     provider aliases, which is how the renamed iptables family satisfies
#     the old split names;
#   - asserts from source (no docker, no network) the DECLARED trust
#     prerequisites: that a CA bundle is a declared dependency, and that the
#     install docs carry the signed-manifest chain. A closure can resolve while
#     the install is still useless — a router with no CA store cannot verify a
#     single TLS connection, and this module's job is money over TLS.
#   - does NOT prove: apk-level signature trust of the artifact (it carries
#     none; provenance is the signed manifest, verified by the release
#     workflow's own acceptance step), install-time script behavior, anything on
#     real flash. Those belong to the vlab and labgrid lanes.
set -euo pipefail

APK="${APK:-}"
IMAGE="${APK_RESOLUTION_IMAGE:-openwrt/rootfs:x86_64-openwrt-25.12}"
RELEASE="${APK_RESOLUTION_RELEASE:-25.12.5}"

# ── declared trust prerequisites (source-level: no docker, no network) ──────
# Run BEFORE the docker check so a machine without docker still asserts these.
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
TRUST_FAILED=0
if grep -qE '^[[:space:]]*DEPENDS:=.*\+ca-bundle' packaging/Makefile; then
    echo "trust: ok — DEPENDS declares +ca-bundle"
else
    echo "trust: FAIL — DEPENDS declares no CA bundle: an image without one cannot verify TLS, so every mint and Lightning call fails (observed on a GL-MT3000: wget and apk update both died with 'SSL verify error')" >&2
    TRUST_FAILED=1
fi
# One chain makes an unsigned artifact safe to install: verify the signed
# manifest, then the bytes. If the docs lose a link, the chain is gone.
for needle in "--allow-untrusted" "SHA256SUMS.sig" "release-signing.pub"; do
    if grep -qF -- "$needle" README.md; then
        echo "trust: ok — README documents $needle"
    else
        echo "trust: FAIL — README does not document '$needle': a manual install would have no stated way to establish provenance" >&2
        TRUST_FAILED=1
    fi
done
[ "$TRUST_FAILED" = 0 ] || exit 1

if ! command -v docker >/dev/null 2>&1; then
    echo "apk-resolution: docker not available — skipping"
    exit 0
fi

RESOLVE_TARGET="nodogsplash"
MOUNT=""
if [ -n "$APK" ]; then
    [ -f "$APK" ] || { echo "apk-resolution: APK=$APK does not exist" >&2; exit 1; }
    APK_DIR=$(cd "$(dirname "$APK")" && pwd)
    APK_NAME=$(basename "$APK")
    RESOLVE_TARGET="/artifact/$APK_NAME"
    MOUNT="-v $APK_DIR:/artifact:ro"
    echo "apk-resolution: resolving the built artifact $APK_NAME"
else
    echo "apk-resolution: no APK set — running the feed-shape control (nodogsplash) only"
fi

WORK=$(mktemp -d /tmp/apk-resolution.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

docker run --rm -i --network host $MOUNT -e RESOLVE_TARGET="$RESOLVE_TARGET" -e RELEASE="$RELEASE" "$IMAGE" sh <<'CONTAINER' | tee "$WORK/resolve.log" | tail -6
set -eu
R="https://downloads.openwrt.org/releases/$RELEASE"
printf "%s\n" \
  $R/targets/x86/64/packages \
  $R/packages/x86_64/base \
  $R/packages/x86_64/packages \
  $R/packages/x86_64/routing \
  $R/packages/x86_64/luci \
  $R/packages/x86_64/telephony \
  > /etc/apk/repositories
echo "apk-resolution: resolving $RESOLVE_TARGET against $RELEASE (apk $(apk --version))"
# apk update returns nonzero while individual probes warn; a retry absorbs
# the transient fetch flakes. The resolve below is the actual gate.
apk update >/dev/null 2>&1 || apk update >/dev/null 2>&1 || true
exec apk add --simulate --allow-untrusted "$RESOLVE_TARGET"
CONTAINER

if grep -qE "no such package|^ERROR" "$WORK/resolve.log"; then
    echo "apk-resolution: FAIL — names unselectable from the $RELEASE feed set:" >&2
    grep -E "no such package|^ERROR" "$WORK/resolve.log" >&2
    exit 1
fi
echo "apk-resolution: PASS — closure resolves against $RELEASE (apk3 semantics)"
