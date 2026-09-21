#!/usr/bin/env python3
"""check-mirror-sync.py — one mirror set, one relay set across the release lanes.

WHY
  The Blossom mirror list and the announce/verification relay list are
  carried by ~15 workflow files (hand-written stage 1 + the generated
  shards via scripts/ngit-gen-shards.py's template) and by script-side
  defaults (scripts/verify_publication.sh). They are copies: when a
  mirror dies (v0.5.0 lost two of three) or a relay is added, every copy
  must change together or a lane silently publishes/verifies against a
  different set than the others.

WHAT
  - Every `BLOSSOM_SERVERS: "..."` literal under .ngit/act/workflows,
    scripts/, and the shard-generator template must be byte-identical.
  - Every announce/verify relay list — `RELAYS: "..."` in the workflows
    and the `VERIFY_RELAYS` default in scripts/verify_publication.sh —
    must be byte-identical.
  - `COORDINATION_RELAYS` is deliberately out of scope: build-record
    coordination uses a different relay set for a different purpose.
  - The GitHub twin (.github/) is deliberately out of scope: it keeps
    blossom1.orangesync.tech on purpose (the ngit job containers could
    not reach it; see the comment in build-package-binaries.yml).

Exit codes: 0 = every copy identical; 1 = drift (each offending file and
its divergence is listed).
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent

SCAN_DIRS = [ROOT / ".ngit" / "act" / "workflows", ROOT / "scripts"]
SCAN_FILES_PATTERNS = ["*.yml", "*.sh", "*.py"]

BLOSSOM_RE = re.compile(r'BLOSSOM_SERVERS:\s*"([^"]+)"')
RELAYS_RE = re.compile(r'\bRELAYS:\s*"([^"]+)"')  # RELAYS: / VERIFY_RELAYS:
# ^VERIFY_RELAYS:-...^ in shell defaults:
RELAYS_SHELL_RE = re.compile(r'\bVERIFY_RELAYS(?:=[^"]*)?:-?"([^"]+)"')


def collect(regexes: list[re.Pattern[str]]) -> dict[str, list[str]]:
    """file -> list of extracted lists (in file order)."""
    found: dict[str, list[str]] = {}
    for d in SCAN_DIRS:
        if not d.is_dir():
            continue
        for pat in SCAN_FILES_PATTERNS:
            for f in sorted(d.glob(pat)):
                text = f.read_text(encoding="utf-8", errors="replace")
                hits = []
                for rx in regexes:
                    hits.extend(m.group(1) for m in rx.finditer(text))
                if hits:
                    found[str(f.relative_to(ROOT))] = hits
    return found


def check(name: str, copies: dict[str, list[str]]) -> int:
    all_hits = [h for hits in copies.values() for h in hits]
    # Majority vote for the canonical list, so a single divergent copy is
    # reported as the drift rather than flipping the baseline.
    counts: dict[tuple[str, ...], int] = {}
    for h in all_hits:
        counts[tuple(h.split())] = counts.get(tuple(h.split()), 0) + 1
    canonical = list(max(counts, key=counts.get))
    failures = 0
    for f, hits in sorted(copies.items()):
        for h in hits:
            if h.split() != canonical:
                print(
                    f"FAIL {name} drift in {f}:\n     has: {' '.join(h.split())}\n"
                    f"     want: {' '.join(canonical)}"
                )
                failures += 1
    if not failures:
        n = sum(len(h) for h in copies.values())
        print(f"ok    {name}: {n} copies identical ({' '.join(canonical)})")
    return failures


def main() -> int:
    blossom = collect([BLOSSOM_RE])
    relays = collect([RELAYS_RE, RELAYS_SHELL_RE])
    fails = 0
    if blossom:
        fails += check("BLOSSOM_SERVERS", blossom)
    else:
        print("FAIL no BLOSSOM_SERVERS literals found — the scan is broken", file=sys.stderr)
        fails += 1
    if relays:
        fails += check("relay list", relays)
    else:
        print("FAIL no relay-list literals found — the scan is broken", file=sys.stderr)
        fails += 1
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
