#!/usr/bin/env python3
"""Check that shared Go dependencies are in sync across all go.mod files."""

import json
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

SRC_DIR = Path(__file__).resolve().parent.parent.parent / "src"

def parse_go_mod(modfile):
    modname = modfile.parent.name
    if modname == "src":
        modname = "root"
    try:
        result = subprocess.run(
            ["go", "mod", "edit", "-json", str(modfile)],
            capture_output=True, text=True, timeout=5
        )
        data = json.loads(result.stdout)
    except Exception:
        return modname, [], ""
    deps = [(r["Path"], r["Version"]) for r in (data.get("Require") or [])]
    go_ver = data.get("Go", "")
    return modname, deps, go_ver

def main():
    go_mods = sorted(SRC_DIR.rglob("go.mod"))
    if not go_mods:
        return 0

    dep_map = defaultdict(dict)
    go_map = {}

    for modfile in go_mods:
        modname, deps, go_ver = parse_go_mod(modfile)
        for path, version in deps:
            dep_map[path][modname] = version
        if go_ver:
            go_map[modname] = go_ver

    drift = []
    go_versions = set(go_map.values())
    if len(go_versions) > 1:
        drift.append(("Go directive", go_map))
    for dep, modules in sorted(dep_map.items()):
        versions = set(modules.values())
        if len(versions) > 1:
            drift.append((dep, modules))

    if not drift:
        print(f"✅ All {len(dep_map)} shared dependencies in sync.")
        return 0

    print("=" * 60)
    print("  DEPENDENCY DRIFT DETECTED")
    print("=" * 60)
    for name, modules in drift:
        print(f"\n🔴 {name} — {len(set(modules.values()))} versions:")
        for mod in sorted(modules):
            print(f"   {mod}: {modules[mod]}")
    print("\nFix: update all go.mod files to the same version.")
    print("     Run 'go mod tidy' in each sub-module after updating.")
    return 1

if __name__ == "__main__":
    sys.exit(main())
