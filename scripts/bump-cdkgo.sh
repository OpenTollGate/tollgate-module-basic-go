#!/usr/bin/env bash
#
# bump-cdkgo.sh — the ONLY supported way to move the cdk-go pin.
#
# Rewrites the single source of truth (packaging/build-inputs.json
# .cdk_go.version) and EVERY go.mod carrier the fence discovers by glob, in
# one run, so a half-update cannot happen (the #791 doctrine; main's
# v0.17.3/v0.18.1 carrier split while #1190 converged on v0.18.3 is the
# cdk-go instance of it).
#
# Usage:
#   scripts/bump-cdkgo.sh <tag>      # e.g. scripts/bump-cdkgo.sh v0.18.3
#
# Requires: go on PATH (each carrier gets `go get` + `go mod tidy` so its
# go.sum is recomputed honestly). Refuses anything that is not
# vMAJOR.MINOR.PATCH — pseudo-version integration pins are edited by hand on
# their branch, never through this script, and must not outlive their tag on
# main. A cdk-go REPLACE directive (the Amperstrand fork class, #1149) is
# refused by the fence, not bumped past: retire the replace first.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$ROOT/packaging/build-inputs.json"
FENCE="tests/contract/check-cdkgo-pin.py"
MODULE="github.com/cashubtc/cdk-go"

if [ $# -ne 1 ] || ! printf '%s' "$1" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "usage: scripts/bump-cdkgo.sh <tag>   (e.g. v0.18.3 — a real tag of cashubtc/cdk-go)" >&2
    exit 1
fi
NEW_VERSION="$1"

command -v go >/dev/null 2>&1 || {
    echo "FATAL: go is not on PATH — every carrier's go.sum must be recomputed by the toolchain, not hand-edited" >&2
    exit 1
}

OLD_VERSION="$(python3 -c "import json; print(json.load(open('$MANIFEST'))['cdk_go']['version'])")"
if [ "$OLD_VERSION" != "$NEW_VERSION" ]; then
    echo "cdk-go: $OLD_VERSION -> $NEW_VERSION"
else
    # The manifest is already at the target; carriers may not be (the
    # reconciliation case: the fence landed naming the target version
    # while the tree still split). Only a full agreement is "nothing to
    # do" — a disagreement is a half-update this script exists to heal.
    echo "manifest already pins $NEW_VERSION — checking carriers for drift"
fi

# 1. The truth moves first (a no-op rewrite when already there); the fence
#    below judges everything against it.
python3 - "$MANIFEST" "$NEW_VERSION" <<'PY'
import collections, json, sys
path, version = sys.argv[1], sys.argv[2]
with open(path) as fh:
    data = json.load(fh, object_pairs_hook=collections.OrderedDict)
if "cdk_go" not in data:
    raise SystemExit(f"FATAL: {path} has no cdk_go key — see the block in #1190's fence PR")
data["cdk_go"]["version"] = version
with open(path, "w") as fh:
    json.dump(data, fh, indent=2)
    fh.write("\n")
PY

# 2. Every discovered carrier moves, via the toolchain (never hand-edited sums).
carriers="$(python3 - <<'PY'
import sys
from pathlib import Path
sys.path.insert(0, "tests/contract")
import importlib.util
spec = importlib.util.spec_from_file_location("cgp", "tests/contract/check-cdkgo-pin.py")
cgp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cgp)
for mod in cgp.carriers(Path(".")):
    print(mod.parent)
PY
)"
for module_dir in $carriers; do
    echo "  bumping $module_dir"
    (cd "$module_dir" && go get "$MODULE@$NEW_VERSION" && go mod tidy)
done

# 3. The verdict: identity must hold everywhere (including NO replaces on
#    the module path — #1149), and the pin must be a real, newest stable
#    tag of upstream cdk-go (network; this script is a maintainer action,
#    not a hook).
python3 "$ROOT/$FENCE" --latest

echo "done: cdk-go $NEW_VERSION everywhere. Commit manifest + carriers together."
