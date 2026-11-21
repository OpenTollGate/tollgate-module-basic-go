#!/usr/bin/env bash
# Offline test for the ip-full dependency contract.
#
# ip-full is not optional for the MPTCP bond: BusyBox `ip` has no `mptcp`
# subcommand, so every endpoint/limits command fails with a usage error on a
# stock image. The bond is opt-in, but a router that enables it must not have
# to discover this from a shell error after boot. Declare it in every in-tree
# recipe, and keep the operator runbook correct for both package-manager eras.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

RECIPES=(
    "packaging/Makefile"
    "packaging/local-build-ipk.sh"
    ".github/workflows/build-package.yml"
    "scripts/ngit-gen-shards.py"
)

depends_line() { # first DEPENDS assignment of a recipe file
    grep -E '^[[:space:]]*DEPENDS[:]?=' "$1" 2>/dev/null | head -n1
}

echo "== per-recipe contract"
for recipe in "${RECIPES[@]}"; do
    if [ ! -f "$recipe" ]; then
        bad "recipe missing: $recipe"
        continue
    fi
    dline=$(depends_line "$recipe")
    case "$dline" in
        *ip-full*)
            ok "$recipe: DEPENDS names ip-full" ;;
        *)
            bad "$recipe: DEPENDS does not name ip-full (got: ${dline:-<no DEPENDS line>})" ;;
    esac
done

echo "== operator runbook"
RUNBOOK="docs/MPTCP-OPERATOR-RUNBOOK.md"
if grep -q 'apk add ip-full' "$RUNBOOK"; then
    ok "$RUNBOOK: documents the apk-era install"
else
    bad "$RUNBOOK: missing the apk-era install command"
fi
if grep -q 'opkg install ip-full' "$RUNBOOK"; then
    ok "$RUNBOOK: documents the opkg-era install"
else
    bad "$RUNBOOK: missing the opkg-era install command"
fi
if grep -q 'ip mptcp limits' "$RUNBOOK"; then
    ok "$RUNBOOK: includes the PATH verification"
else
    bad "$RUNBOOK: missing the 'ip mptcp limits' verification"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
