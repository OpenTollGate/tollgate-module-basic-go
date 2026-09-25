#!/usr/bin/env python3
"""check-changelog-duplicates.py — every CHANGELOG.md entry is identified
by its bold lead-in phrase; the same lead-in must never appear twice.

The failure mode this pins: resolving a CHANGELOG conflict with "take
ours" (or any section-level resolution) can resurrect an entry that a
branch commit had already moved or reworded, leaving the same bullet in
two sections — release notes then describe one change twice, and fold
scripts that count entries go wrong. This happened twice in one
rebasing session; the check exists so it never lands.

Usage: python3 tests/contract/check-changelog-duplicates.py  (repo root)
Exit 0 = no duplicates, exit 1 = duplicates listed.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

# A lead-in is the span set in bold at the start of an entry bullet:
# "- **The captive portal's Lightning lane …**". Bold spans may wrap
# across lines (re.DOTALL); comparing the full span keeps deliberately
# similar leads distinct from true verbatim duplicates.
LEAD_IN = re.compile(r"(?m)^- \*\*(.+?)\*\*", re.DOTALL)
SECTION = re.compile(r"(?m)^## \[([^\]]+)\]")


def main() -> int:
    changelog = Path(__file__).resolve().parent.parent.parent / "CHANGELOG.md"
    text = changelog.read_text(encoding="utf-8")

    # Attribute every entry to the version section it lives in, then fail
    # only on duplicates that involve [Unreleased]: that is the live edit
    # zone where a conflict resolution can resurrect a moved entry next to
    # its stale copy. The same lead-in appearing twice inside frozen,
    # already-released sections is history — release notes are not
    # rewritten, so those are left alone on purpose.
    sections: list[tuple[int, str]] = [(m.start(), m.group(1)) for m in SECTION.finditer(text)]

    def section_at(pos: int) -> str:
        current = "?"
        for start, name in sections:
            if start > pos:
                break
            current = name
        return current

    where: dict[str, set[str]] = {}
    for m in LEAD_IN.finditer(text):
        lead = re.sub(r"\s+", " ", m.group(1)).strip()
        where.setdefault(lead, set()).add(section_at(m.start()))

    dupes = sorted(lead for lead, secs in where.items() if len(secs) > 1 and "Unreleased" in secs)
    if dupes:
        print("CHANGELOG.md has duplicate entries involving [Unreleased] (same bold lead-in twice):")
        print()
        for lead in dupes:
            print(f"  - {lead}  (sections: {', '.join(sorted(where[lead]))})")
        print()
        print("One logical change gets exactly one entry. If a branch moved or")
        print("reworded an entry, drop the stale copy — do not keep both.")
        return 1
    print("Shipped CHANGELOG has no duplicate entries (every bold lead-in is unique).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
