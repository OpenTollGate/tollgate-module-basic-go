#!/usr/bin/env python3
"""check-release-fold-state: the release-fold fence (docs/release-process.md).

The changelog is folded BEFORE a release (changelog-assemble.py fold --target
<vX.Y.Z>). rc2/rc3/rc4 were cut by VERSION bumps alone — the fold was skipped
each time, and the final cut then has to reconstruct what shipped when from
fragments. This gate makes the discipline enforceable instead of remembered:

- A FINAL version (vX.Y.Z, no pre-release suffix) must:
    * carry its own `## [vX.Y.Z]` section in CHANGELOG.md,
    * leave changelog.d/ empty of fragments (README.md is not a fragment),
    * be named in RELEASE-NOTES.md (the notes are rewritten per release).
- A PRE-RELEASE (vX.Y.Z-rcN/-alphaN/-betaN) may cut unfolded — folding at
  final is the documented batch shape — but the unfolded count is reported
  as a warning so the drift is visible, never silent.

Usage: check-release-fold-state.py <version> [repo-root]
Exit: 0 = gate satisfied (warnings allowed for pre-releases), 1 = refusal.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

FINAL_RE = re.compile(r"^v\d+\.\d+\.\d+$")
PRERELEASE_RE = re.compile(r"^v\d+\.\d+\.\d+-(rc|alpha|beta)\d+$")


def fragments(root: Path) -> list[Path]:
    d = root / "changelog.d"
    if not d.is_dir():
        return []
    return sorted(p for p in d.glob("*.md") if p.name != "README.md")


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: check-release-fold-state.py <version> [repo-root]", file=sys.stderr)
        return 1
    version = sys.argv[1]
    root = Path(sys.argv[2]) if len(sys.argv) > 2 else Path()

    if not (FINAL_RE.match(version) or PRERELEASE_RE.match(version)):
        print(f"FAIL: '{version}' is not a release version (vX.Y.Z or vX.Y.Z-<pre>N)")
        return 1

    frags = fragments(root)
    changelog = (root / "CHANGELOG.md").read_text() if (root / "CHANGELOG.md").exists() else ""
    notes = (root / "RELEASE-NOTES.md").read_text() if (root / "RELEASE-NOTES.md").exists() else ""

    if PRERELEASE_RE.match(version):
        if frags:
            print(f"WARN: {len(frags)} unfolded fragment(s) at pre-release {version} — fold at final")
        print(f"OK: {version} pre-release (fold-state not enforced)")
        return 0

    failures = []
    if frags:
        names = ", ".join(p.name for p in frags[:5])
        more = f" (+{len(frags) - 5} more)" if len(frags) > 5 else ""
        failures.append(f"changelog.d/ still holds {len(frags)} fragment(s): {names}{more} — run "
                        f"`python3 scripts/changelog-assemble.py fold --target {version}` first")
    if f"## [{version}]" not in changelog:
        failures.append(f"CHANGELOG.md has no `## [{version}]` section (fold not run)")
    if version not in notes:
        failures.append(f"RELEASE-NOTES.md does not name {version} (notes are rewritten per release)")

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1
    print(f"OK: {version} final — folded, fragments drained, notes current")
    return 0


if __name__ == "__main__":
    sys.exit(main())
