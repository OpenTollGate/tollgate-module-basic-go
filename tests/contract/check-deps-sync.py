#!/usr/bin/env python3
"""Check that shared Go dependencies are in sync across all go.mod files."""

import json
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

SRC_DIR = Path(__file__).resolve().parent.parent.parent / "src"

class ParseFailure(Exception):
    """The dependency graph was not verified; never substitute an empty graph."""

def parse_go_mod(modfile):
    relative = modfile.relative_to(SRC_DIR)
    modname = str(relative.parent) if relative.parent != Path('.') else 'root'
    try:
        result = subprocess.run(
            ["go", "mod", "edit", "-json", str(modfile)],
            capture_output=True, text=True, timeout=5
        )
    except (OSError, subprocess.TimeoutExpired):
        raise ParseFailure(f'{relative}: go mod parser unavailable or timed out') from None
    if result.returncode != 0:
        raise ParseFailure(f'{relative}: go mod parser exited {result.returncode}')
    try:
        data = json.loads(result.stdout)
        if not isinstance(data, dict) or not isinstance(data.get('Module'), dict) or not isinstance(data['Module'].get('Path'), str) or not data['Module']['Path']:
            raise ValueError('invalid module metadata')
        requirements = data.get('Require')
        if requirements is None:
            requirements = []
        if not isinstance(requirements, list):
            raise ValueError('invalid requirements')
        deps = []
        for item in requirements:
            if not isinstance(item, dict) or not all(isinstance(item.get(key), str) and item[key] for key in ('Path', 'Version')):
                raise ValueError('invalid dependency')
            deps.append((item['Path'], item['Version']))
        go_ver = data.get('Go')
        if go_ver is None or go_ver == '':
            go_ver = 'unspecified'
        if not isinstance(go_ver, str):
            raise ValueError('invalid Go directive')
    except (ValueError, TypeError):
        raise ParseFailure(f'{relative}: invalid go mod parser output') from None
    return modname, deps, go_ver

def main():
    go_mods = sorted(SRC_DIR.rglob("go.mod"))
    if not go_mods:
        print('DEPENDENCY CHECK NOT VERIFIED: no go.mod files found')
        return 1

    dep_map = defaultdict(dict)
    go_map = {}

    for modfile in go_mods:
        try:
            modname, deps, go_ver = parse_go_mod(modfile)
        except ParseFailure as error:
            # Parser stderr can contain environment-specific details. Report
            # only the relative module and failure class, never raw payloads.
            print(f'DEPENDENCY CHECK NOT VERIFIED: {error}')
            return 1
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
        print(f"✅ All {len(dep_map)} shared dependencies in sync ({len(go_mods)} modules parsed).")
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
    if sys.argv[1:] == ['--selftest-and-check']:
        test = Path(__file__).with_name('check-deps-sync_test.py')
        if not test.is_file() or subprocess.run([sys.executable, '-B', str(test)]).returncode:
            print('DEPENDENCY CHECK NOT VERIFIED: parser self-tests failed or missing')
            sys.exit(1)
    elif sys.argv[1:]:
        print('Usage: check-deps-sync.py [--selftest-and-check]')
        sys.exit(2)
    sys.exit(main())
