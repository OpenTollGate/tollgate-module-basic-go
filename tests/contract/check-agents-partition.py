#!/usr/bin/env python3
"""check-agents-partition.py — AGENTS.md core and docs/agents/ cannot drift.

WHY
  AGENTS.md is a shared anchor every docs/doctrine lane used to append to
  (#920). The growing doctrine now lives in docs/agents/<topic>.md and
  AGENTS.md carries a stable index whose entries are LOAD DIRECTIVES —
  agents must explicitly open the topic file, not rely on link discovery.
  That contract only holds if the index and the directory agree in BOTH
  directions: a topic file that exists but is not indexed is unreachable
  doctrine, an index entry without a file is a dead directive, and an
  entry without a "Load before ..." clause is a suggestion pretending to
  be a directive.

WHAT
  - Every docs/agents/*.md file (README.md excluded) appears in AGENTS.md's
    "Topic doctrine" section.
  - Every docs/agents/... link in that section resolves to a real file.
  - Every index entry carries a load directive ("Load before ...").
  - The section exists at all.

Exit codes: 0 = the partition is consistent; 1 = drift (each offender
named); 2 = the invocation is unusable.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

INDEX_HEADING = "## Topic doctrine"
TOPIC_DIR = Path("docs/agents")
LINK_RE = re.compile(r"\]\((docs/agents/([A-Za-z0-9._-]+\.md))\)")
DIRECTIVE_RE = re.compile(r"load before", re.IGNORECASE)


def check(root: Path) -> list[str]:
    problems: list[str] = []
    agents = root / "AGENTS.md"
    if not agents.is_file():
        return [f"{agents}: not found under the root"]
    text = agents.read_text(encoding="utf-8")

    marker = INDEX_HEADING
    if marker not in text:
        return [
            "AGENTS.md: no '## Topic doctrine' section — the docs/agents/ "
            "index is what keeps topic files reachable (split doctrine out, "
            "do not append sections to the core)"
        ]
    section = text.split(marker, 1)[1].split("\n## ", 1)[0]

    on_disk: set[str] = set()
    topic_dir = root / TOPIC_DIR
    if topic_dir.is_dir():
        for path in sorted(topic_dir.glob("*.md")):
            if path.name == "README.md":
                continue
            on_disk.add(path.name)
    if not on_disk:
        problems.append(
            "docs/agents/: no topic files — the partition's topic half is "
            "missing"
        )

    indexed: dict[str, bool] = {}
    for match in LINK_RE.finditer(section):
        target, name = match.group(1), match.group(2)
        exists = (root / target).is_file()
        indexed[name] = exists
        if not exists:
            problems.append(
                f"AGENTS.md: index links {target}, which does not exist — a "
                "dead load directive is worse than none"
            )

    for name in sorted(on_disk - set(indexed)):
        problems.append(
            f"docs/agents/{name}: exists but is not in AGENTS.md's topic "
            "index — unreachable doctrine; add an index entry with its load "
            "directive"
        )

    # Every indexed entry must carry a load directive: split the section
    # into bullets and require the directive phrase in each that links a
    # topic file.
    for bullet in section.split("\n- ")[1:]:
        if not LINK_RE.search(bullet):
            continue
        if not DIRECTIVE_RE.search(bullet):
            name = LINK_RE.search(bullet).group(2)
            problems.append(
                f"AGENTS.md: the '{name}' index entry has no 'Load before' "
                "directive — entries are load directives, not link "
                "discovery"
            )

    return problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=None, help="repository root (default: cwd's git root)")
    args = ap.parse_args(argv)

    root = Path(args.root).resolve() if args.root else Path.cwd().resolve()
    problems = check(root)
    for p in problems:
        print(f"agents-partition: {p}", file=sys.stderr)
    if problems:
        return 1
    print("agents-partition: AGENTS.md core and docs/agents/ agree")
    return 0


if __name__ == "__main__":
    sys.exit(main())
