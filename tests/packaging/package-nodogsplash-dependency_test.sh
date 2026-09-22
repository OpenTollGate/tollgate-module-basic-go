#!/usr/bin/env bash
# Offline test for the captive-portal dependency contract of every in-tree
# packaging recipe — no router, no SDK, no network needed.
#
# The real bug this guards: the module's SDK package definition declared
# `REPLACES:=nodogsplash base-files` (and the equivalent Replaces: field in
# the .ipk recipes) while depending only on libc. Replaces means "my files
# supersede yours": opkg/apk removes the replaced package. Installing
# tollgate-wrt therefore PURGED nodogsplash — the captive-portal daemon the
# module gates the network with — leaving a router whose portal never came up
# until nodogsplash was reinstalled by hand. The feed/shipping-path definition
# (net/tollgate-wrt/Makefile in the OpenWrt feed) already had it right
# (`DEPENDS:=+nodogsplash +jq`, no REPLACES); this test pins the module's own
# recipes to the same contract so the divergence cannot come back.
#
# Every recipe that writes a package definition is covered:
#   * packaging/Makefile            (OpenWrt SDK / .apk path)
#   * packaging/local-build-ipk.sh  (deterministic .ipk path)
#   * .github/workflows/build-package.yml              (CI .ipk build)
#   * scripts/ngit-gen-shards.py + .ngit/act/workflows/*.yml (ngit CI shards,
#     generated — the generator is checked, and `--check` keeps the committed
#     copies honest)
#
# Usage: bash tests/packaging/package-nodogsplash-dependency_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

# Recipes that carry a package definition of their own.
RECIPES=(
    "packaging/Makefile"
    "packaging/local-build-ipk.sh"
    ".github/workflows/build-package.yml"
    "scripts/ngit-gen-shards.py"
)

# The package definition block of packaging/Makefile is what the SDK builds
# from; the DEPENDS/REPLACES pair there is the one the bug was reported on.
depends_line() { # first DEPENDS assignment of a recipe file
    grep -E '^[[:space:]]*DEPENDS[:]?=' "$1" 2>/dev/null | head -n1
}
replaces_line() { # first REPLACES assignment of a recipe file
    grep -E '^[[:space:]]*REPLACES[:]?=' "$1" 2>/dev/null | head -n1
}

echo "== per-recipe contract"
for recipe in "${RECIPES[@]}"; do
    if [ ! -f "$recipe" ]; then
        bad "recipe missing: $recipe"
        continue
    fi
    dline=$(depends_line "$recipe")
    case "$dline" in
        *nodogsplash*)
            ok "$recipe: DEPENDS names nodogsplash" ;;
        *)
            bad "$recipe: DEPENDS does not name nodogsplash (got: ${dline:-<no DEPENDS line>})" ;;
    esac
    case "$dline" in
        *jq*)
            ok "$recipe: DEPENDS names jq" ;;
        *)
            bad "$recipe: DEPENDS does not name jq (got: ${dline:-<no DEPENDS line>})" ;;
    esac

    rline=$(replaces_line "$recipe")
    case "$rline" in
        *nodogsplash*)
            bad "$recipe: REPLACES still lists nodogsplash — installing the module would purge the captive-portal daemon (got: $rline)" ;;
        *)
            ok "$recipe: REPLACES does not list nodogsplash" ;;
    esac
done

echo "== generated ngit CI shards"
shards=0
for shard in .ngit/act/workflows/build-package-ipk-*.yml; do
    [ -f "$shard" ] || continue
    shards=$((shards + 1))
    dline=$(depends_line "$shard")
    case "$dline" in
        *nodogsplash*) ok "$shard: DEPENDS names nodogsplash" ;;
        *) bad "$shard: DEPENDS does not name nodogsplash (got: ${dline:-<none>})" ;;
    esac
    rline=$(replaces_line "$shard")
    case "$rline" in
        *nodogsplash*)
            bad "$shard: REPLACES still lists nodogsplash (got: $rline)" ;;
        *) ok "$shard: REPLACES does not list nodogsplash" ;;
    esac
done
if [ "$shards" -eq 0 ]; then
    bad "no generated ngit CI shards found to check (expected .ngit/act/workflows/build-package-ipk-*.yml)"
else
    ok "$shards generated ngit CI shard(s) checked"
fi

# Drift guard: nothing anywhere under the packaging/CI trees may re-introduce
# Replaces: nodogsplash, in any recipe shape (Makefile, .ipk control field,
# workflow env block). Comment lines are exempt — they are where the bug is
# explained, not where it is declared.
echo "== repo-wide drift guard"
drifting=$(grep -rIn 'REPLACES' packaging scripts .github/workflows .ngit/act/workflows 2>/dev/null \
    | grep 'nodogsplash' \
    | grep -v -E ':[0-9]+:[[:space:]]*#' || true)
if [ -n "$drifting" ]; then
    while IFS= read -r line; do
        bad "Replaces: nodogsplash re-introduced: $line"
    done <<EOF
$drifting
EOF
else
    ok "no packaging/CI recipe declares Replaces: nodogsplash"
fi

# The virtual "nodogsplash-files" ownership must survive: the module ships
# files into nodogsplash's config/doc space, so it still Provides the virtual
# name. Only the REPLACES half was wrong.
echo "== virtual ownership preserved"
if grep -q 'PROVIDES[:]=\?.*nodogsplash-files' packaging/Makefile; then
    ok "packaging/Makefile still Provides nodogsplash-files"
else
    bad "packaging/Makefile lost PROVIDES:=nodogsplash-files"
fi

# Artifact-level check: run the deterministic .ipk builder with the DEPENDS /
# REPLACES values parsed straight out of the recipe, then read the generated
# control file. Static text can be right while the artifact is wrong (a stale
# quoted value, a builder that re-adds the field), so this asserts the thing
# opkg actually reads.
echo "== artifact-level check (.ipk control built from the recipe)"
dep_val=$(sed -n 's/^[[:space:]]*DEPENDS="\(.*\)"[[:space:]]*\\\{0,1\}$/\1/p' packaging/local-build-ipk.sh | head -n1)
rep_val=$(sed -n 's/^[[:space:]]*REPLACES="\(.*\)"[[:space:]]*\\\{0,1\}$/\1/p' packaging/local-build-ipk.sh | head -n1)
ipk_work=$(mktemp -d)
trap 'rm -rf "$ipk_work"' EXIT
mkdir -p "$ipk_work/payload/usr/bin"
: > "$ipk_work/payload/usr/bin/tollgate-wrt"
if [ -n "$dep_val" ] && [ -n "$rep_val" ] && \
   env PKG_NAME="tollgate-wrt" PKG_VERSION="0.0.0-test" ARCH="aarch64_cortex-a53" \
       MAINTAINER="TollGate <tollgate@tollgate.me>" LICENSE="GPL-3.0-only" \
       DEPENDS="$dep_val" PROVIDES="nodogsplash-files" REPLACES="$rep_val" \
       DESCRIPTION="TollGate Basic Module for OpenWrt" \
       sh packaging/build-ipk.sh "$ipk_work/payload" "$ipk_work/tollgate-wrt.ipk" >/dev/null 2>&1 && \
   ( cd "$ipk_work" && tar xzf tollgate-wrt.ipk ./control.tar.gz && tar xzf control.tar.gz -O ./control > control.txt ) 2>/dev/null; then
    control=$(cat "$ipk_work/control.txt")
    case "$control" in
        *"Depends: libc, nodogsplash, jq"*)
            ok "built .ipk control declares Depends: libc, nodogsplash, jq" ;;
        *)
            bad "built .ipk control has wrong Depends (got: $(printf '%s' "$control" | grep '^Depends:' || echo '<none>'))" ;;
    esac
    case "$control" in
        *"Provides: nodogsplash-files"*)
            ok "built .ipk control keeps Provides: nodogsplash-files" ;;
        *)
            bad "built .ipk control lost Provides: nodogsplash-files" ;;
    esac
    replaces_field=$(printf '%s' "$control" | grep '^Replaces:' || true)
    case "$replaces_field" in
        *nodogsplash*)
            bad "built .ipk control still replaces nodogsplash — install would purge the captive portal (got: $replaces_field)" ;;
        *)
            ok "built .ipk control does not replace nodogsplash (Replaces: ${replaces_field#Replaces: })" ;;
    esac
else
    bad "could not build and inspect a test .ipk (needs packaging/build-ipk.sh, gnu tar, gzip)"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
