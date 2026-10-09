#!/usr/bin/env bash
# apk-install-resolution smoke — prove the built package's dependency closure
# resolves against a stock OpenWrt apk feed set, on the tooling that actually
# reads 25.12 indexes (apk-tools 3; apk 2.x silently resolves nothing against
# these feeds — measured while diagnosing #552 — do not "simplify" this away).
#
# Usage:
#   APK=/path/to/tollgate-wrt*.apk bash tests/packaging/apk-install-resolution_test.sh
#
# Without docker the closure lane skips cleanly (exit 0) with a stated reason —
# the same contract as the other cloud-lab lanes. Without $APK it runs the
# feed-shape control (resolving nodogsplash, the #552 dependency) so the
# harness itself is always exercised.
#
# The container runs with --network host: apk3's fetcher intermittently
# attempts IPv6 and fails under the default docker bridge ("Operation not
# permitted"), which made the lane flap. The repositories written inside are
# the real https release URLs — the exact resolution path a stock router
# takes, signatures aside (--allow-untrusted; trust is not this lane's claim).
#
# WHAT THE TRUST CHECKS PROVE, AND WHAT THEY DELIBERATELY DO NOT
# --------------------------------------------------------------
# An earlier revision of this lane "proved" the CA-bundle declaration by
# grepping README.md and packaging/Makefile for literal strings. That was
# tautological: a README that told the reader NOT to use the untrusted
# override still passed, a dependency living only inside a comment still
# passed, and a formatting-only Makefile edit failed. Raw text is not the
# artefact. Both checks below assert BUILD METADATA instead, and both run
# BEFORE the docker gate, so a machine without docker still runs them.
#
#   (1) apk/SDK lane. packaging/Makefile is the SDK package definition. This
#       check expands it with `make -pn` — an empty TOPDIR/INCLUDE_DIR is
#       enough for make to print its variable database, which carries the
#       `define Package/tollgate-wrt` body — and requires the bundle in the
#       DEPENDS value of that define. Comment lines are dropped before
#       matching (make itself treats them as comments), so a dependency that
#       lives only in a comment cannot satisfy this. It proves the RECIPE
#       declares the dependency; it cannot prove the SDK build emitted the
#       field — that belongs to the CI/release lane.
#
#   (2) opkg/.ipk lane. Builds a real .ipk from packaging/local-build-ipk.sh's
#       DEPENDS value with packaging/build-ipk.sh and reads the `Depends:`
#       field of the control file opkg actually consumes. This is the
#       load-bearing one: it is the byte-level artefact, so a dropped or
#       commented-out dependency cannot pass. It installs nothing and touches
#       no flash.
#
# Both checks FAIL CLOSED: dropping the bundle from either recipe, or leaving
# it only in a comment, fails this lane before its docker gate. They are not
# "skip offline" checks — the declaration is the thing under test.
#
# NOT proven (unchanged): apk-level signature trust of the artefact (it
# carries none; provenance is the signed manifest, verified by the release
# workflow's own acceptance step), install-time script behaviour, anything on
# real flash. Those belong to the vlab and labgrid lanes.
set -euo pipefail

APK="${APK:-}"
IMAGE="${APK_RESOLUTION_IMAGE:-openwrt/rootfs:x86_64-openwrt-25.12}"
RELEASE="${APK_RESOLUTION_RELEASE:-25.12.5}"

# ── declared trust prerequisites (build metadata: no docker, no network) ─────
# Run BEFORE the docker check so a machine without docker still asserts these.
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
TRUST_FAILED=0
trust_fail() { printf 'trust: FAIL — %s\n' "$1" >&2; TRUST_FAILED=1; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/apk-resolution.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# (1) apk/SDK lane: the DEPENDS value of the package definition, as make
# expands it. A dependency that exists only inside a comment is dropped here
# because comment lines are removed before the assignment is read.
: > "$WORK/rules.mk"
: > "$WORK/package.mk"
sdk_depends="$(
    make -C packaging -pn TOPDIR="$WORK" INCLUDE_DIR="$WORK" 2>/dev/null \
    | awk '
        /^define Package\/tollgate-wrt[[:space:]]*$/ { inb = 1; next }
        /^endef[[:space:]]*$/                        { inb = 0 }
        inb && !done && $0 !~ /^[[:space:]]*#/ && $0 ~ /^[[:space:]]*DEPENDS[:]?=/ {
            line = $0
            sub(/^[[:space:]]*DEPENDS[:]?=[[:space:]]*/, "", line)
            print line
            done = 1
        }' || true
)"
if printf '%s' "$sdk_depends" | grep -qE '(^|[^A-Za-z0-9_-])ca-bundle([^A-Za-z0-9_-]|$)'; then
    echo "trust: ok — SDK package definition DEPENDS declares ca-bundle ($sdk_depends)"
else
    trust_fail "packaging/Makefile's Package/tollgate-wrt DEPENDS declares no ca-bundle (expanded: '${sdk_depends:-<none>}'): an image with no CA store cannot verify TLS, so every Cashu and Lightning call fails and no payment can be taken"
fi

# (2) opkg/.ipk lane: build the real .ipk from the recipe's DEPENDS and read the
# control field opkg consumes — the byte-level check raw text cannot fake.
if [ -f packaging/build-ipk.sh ] && [ -f packaging/local-build-ipk.sh ]; then
    ipk_depends="$(sed -n 's/^[[:space:]]*DEPENDS="\(.*\)"[[:space:]]*\\\{0,1\}$/\1/p' packaging/local-build-ipk.sh | head -n1 || true)"
    mkdir -p "$WORK/payload/usr/bin"
    : > "$WORK/payload/usr/bin/tollgate-wrt"
    if [ -n "$ipk_depends" ] && \
       env PKG_NAME="tollgate-wrt" PKG_VERSION="0.0.0-test" ARCH="aarch64_cortex-a53" \
           MAINTAINER="TollGate <tollgate@tollgate.me>" LICENSE="GPL-3.0-only" \
           DEPENDS="$ipk_depends" PROVIDES="nodogsplash-files" REPLACES="base-files" \
           DESCRIPTION="TollGate Basic Module for OpenWrt" \
           sh packaging/build-ipk.sh "$WORK/payload" "$WORK/tollgate-wrt.ipk" >/dev/null 2>&1 && \
       ( cd "$WORK" && tar xzf tollgate-wrt.ipk ./control.tar.gz && \
         tar xzf control.tar.gz -O ./control > control.txt ) 2>/dev/null; then
        dep_field="$(grep -E '^Depends:' "$WORK/control.txt" || true)"
        if printf '%s' "$dep_field" | grep -qE '(^|, )[[:space:]]*ca-bundle([[:space:]]*(,|$))'; then
            echo "trust: ok — built .ipk control declares ca-bundle ($dep_field)"
        else
            trust_fail "built .ipk control does not declare ca-bundle (got: '${dep_field:-<none>}'): the installed gateway cannot verify TLS to a mint"
        fi
    else
        trust_fail "could not build and inspect the .ipk control (recipe DEPENDS was '${ipk_depends:-<none>}'; needs packaging/build-ipk.sh, gnu tar, gzip)"
    fi
else
    trust_fail "packaging/build-ipk.sh or packaging/local-build-ipk.sh is missing — cannot assert the .ipk control metadata"
fi

[ "$TRUST_FAILED" = 0 ] || exit 1

if ! command -v docker >/dev/null 2>&1; then
    echo "apk-resolution: docker not available — skipping the closure lane"
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
