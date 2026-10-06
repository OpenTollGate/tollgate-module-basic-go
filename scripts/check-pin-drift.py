#!/usr/bin/env python3
"""Pin-drift policy for the internal test baseline.

RELEASE-NOTES pins the internal test baseline to one commit ("Internal test
baseline (pinned): commit `<sha>`"). main keeps moving; the policy (recorded
in the same section) is:

  - commits that touch ONLY non-production surfaces (docs, tests, CI,
    agent guidance) do not move the pin — the tested production tree is
    unchanged;
  - any commit that touches production code (src/ or packaging/) after the
    pin means the pin is STALE: either re-pin (and re-run the full gate) or
    revert the drift. This check fails loudly in that state so "verify
    owed" is visible instead of remembered.

Usage: python3 scripts/check-pin-drift.py   (repo root, full git history)
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

PRODUCTION_PREFIXES = ("src/", "packaging/")


def git(*args: str) -> str:
    return subprocess.run(
        ["git", "-C", str(ROOT), *args], check=True, capture_output=True, text=True
    ).stdout.strip()


def main() -> int:
    notes = (ROOT / "RELEASE-NOTES.md").read_text()
    m = re.search(
        r"test baseline \(pinned\)?: commit\s+`?([0-9a-f]{40})`?", notes, re.I
    )
    if not m:
        print("pin-drift: FAIL — RELEASE-NOTES carries no pinned baseline commit")
        return 1
    pin = m.group(1)
    head = git("rev-parse", "HEAD")

    if head == pin:
        print(f"pin-drift: PASS — main is exactly the pinned baseline ({pin[:12]})")
        return 0

    if git("merge-base", "--is-ancestor", pin, head) != "":
        # Non-zero exit from merge-base --is-ancestor means pin is NOT an
        # ancestor (history rewritten or pin from another line).
        print(f"pin-drift: FAIL — pin {pin[:12]} is not an ancestor of HEAD")
        return 1

    changed = git("log", "--name-only", "--pretty=format:", f"{pin}..{head}").split()
    production = sorted({c for c in changed if c.startswith(PRODUCTION_PREFIXES)})
    if production:
        print(
            f"pin-drift: FAIL — {len(production)} production path(s) changed since "
            f"the pin {pin[:12]} — re-pin with a fresh full gate run or revert:"
        )
        for path in production[:20]:
            print(f"  - {path}")
        return 1

    print(
        f"pin-drift: PASS — main moved past the pin {pin[:12]} on non-production "
        "paths only (pin-neutral per the recorded policy)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
