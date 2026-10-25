#!/usr/bin/env bash
#
# bump-gonuts.sh — the ONLY supported way to move the gonuts-tollgate pin.
#
# Rewrites the single source of truth (packaging/build-inputs.json .gonuts.
# version) and EVERY go.mod carrier the fence discovers by glob, in one run,
# so a half-update cannot happen (#791: the hand-bumped PR #780 moved four
# carriers and left a fifth at v0.10.0).
#
# Usage:
#   scripts/bump-gonuts.sh <tag>        # e.g. scripts/bump-gonuts.sh v0.13.2
#
# Requires: go on PATH (each carrier gets `go get` + `go mod tidy` so its
# go.sum is recomputed honestly). Refuses anything that is not vMAJOR.MINOR.
# PATCH — pseudo-version integration pins (#771 pattern) are edited by hand
# on their branch, never through this script, and must not outlive their tag
# on main.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$ROOT/packaging/build-inputs.json"
FENCE="tests/contract/check-gonuts-pin.py"

if [ $# -ne 1 ] || ! printf '%s' "$1" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "usage: scripts/bump-gonuts.sh <tag>   (e.g. v0.13.2 — a real tag of OpenTollGate/gonuts-tollgate)" >&2
    exit 1
fi
NEW_VERSION="$1"

command -v go >/dev/null 2>&1 || {
    echo "FATAL: go is not on PATH — every carrier's go.sum must be recomputed by the toolchain, not hand-edited" >&2
    exit 1
}

OLD_VERSION="$(python3 -c "import json; print(json.load(open('$MANIFEST'))['gonuts']['version'])")"
if [ "$OLD_VERSION" = "$NEW_VERSION" ]; then
    echo "manifest already pins $NEW_VERSION — nothing to do"
    exit 0
fi
echo "gonuts-tollgate: $OLD_VERSION -> $NEW_VERSION"

# 1. The truth moves first; the fence below judges everything against it.
python3 - "$MANIFEST" "$NEW_VERSION" <<'PY'
import collections, json, sys
path, version = sys.argv[1], sys.argv[2]
with open(path) as fh:
    data = json.load(fh, object_pairs_hook=collections.OrderedDict)
if "gonuts" not in data:
    raise SystemExit(f"FATAL: {path} has no gonuts key — see #791 for the block to add")
data["gonuts"]["version"] = version
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
spec = importlib.util.spec_from_file_location("cgp", "tests/contract/check-gonuts-pin.py")
cgp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cgp)
for mod in cgp.carriers(Path(".")):
    print(mod.parent)
PY
)"
for module_dir in $carriers; do
    echo "  bumping $module_dir"
    (cd "$module_dir" && go get "github.com/OpenTollGate/gonuts-tollgate@$NEW_VERSION" && go mod tidy)
done

# 3. The verdict: identity must hold everywhere, and the pin must be a real,
#    newest stable tag of the fork (network; this script is a maintainer
#    action, not a hook).
python3 "$ROOT/$FENCE" --latest

echo "done: gonuts-tollgate $NEW_VERSION everywhere. Commit manifest + carriers together."
