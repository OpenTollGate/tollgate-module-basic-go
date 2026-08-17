#!/usr/bin/env python3
"""Check that Go source files don't import from stale module paths.

Catches issues like importing github.com/Origami74/gonuts-tollgate
when the module has been renamed to github.com/OpenTollGate/gonuts-tollgate.
"""

import re
import sys
from pathlib import Path

SRC_DIR = Path(__file__).resolve().parent.parent.parent / "src"

STALE_PATTERNS = [
    (r"github\.com/Origami74/", "github.com/OpenTollGate/ (renamed in PR #304)"),
    (r"github\.com/elnosh/gonuts", "github.com/OpenTollGate/gonuts-tollgate (forked)"),
]

def check_file(filepath):
    issues = []
    try:
        content = filepath.read_text(encoding="utf-8", errors="replace")
    except Exception:
        return issues

    for line_num, line in enumerate(content.splitlines(), 1):
        if line.strip().startswith("//") or line.strip().startswith("*"):
            continue
        for pattern, replacement in STALE_PATTERNS:
            if re.search(pattern, line):
                issues.append((filepath, line_num, line.strip(), pattern, replacement))
    return issues

def main():
    go_files = sorted(SRC_DIR.rglob("*.go"))
    if not go_files:
        print("No .go files found")
        return 0

    all_issues = []
    for filepath in go_files:
        all_issues.extend(check_file(filepath))

    if not all_issues:
        print(f"✅ All {len(go_files)} Go source files use correct import paths.")
        return 0

    print("=" * 60)
    print("  STALE IMPORT PATHS DETECTED")
    print("=" * 60)
    for filepath, line_num, line, pattern, replacement in all_issues:
        rel = filepath.relative_to(SRC_DIR.parent)
        print(f"\n🔴 {rel}:{line_num}")
        print(f"   {line}")
        print(f"   Should be: {replacement}")
    print(f"\n{len(all_issues)} stale import(s) found in {len(go_files)} files.")
    print("Fix: update import paths to match current module names.")
    return 1

if __name__ == "__main__":
    sys.exit(main())
