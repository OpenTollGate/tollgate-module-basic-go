#!/usr/bin/env python3
"""Fold changelog fragments into CHANGELOG.md.

Every pull request used to append its entry to the same anchor of
``CHANGELOG.md``, so two pull requests in flight always conflicted on the same
lines and the maintainer resolved those hunks by hand (about a dozen of them on
the 0.6.0 train, several resolved wrong). A fragment is one file per change:

    changelog.d/<pr-number>-<slug>.<type>.md

``<type>`` is one of added, changed, internal, deprecated, fixed, removed,
security — the section the entry belongs in. The file body *is* the changelog
bullet, verbatim, starting with ``- ``. Two pull requests can therefore never
touch the same lines: filenames are unique, and git merges unique new files
without conflict. The maintainer folds the accumulated fragments in one step
before a release (or whenever the changelog should read current):

    python3 scripts/changelog-assemble.py fold [--target Unreleased] [--dry-run]
    python3 scripts/changelog-assemble.py check

``fold`` writes one wave of subsections immediately below the target release
heading — newest PR number first, subsections in canonical order — deletes the
folded fragments, and touches nothing else in the file. ``check`` validates
fragment names and bodies and is what CI and the pre-commit hook run.

The decision record is docs/architecture/changelog-fragments-decision.md.
"""

from __future__ import annotations

import argparse
import difflib
import re
import sys
from pathlib import Path

# Fragment type -> the subsection heading the entry lands under, in the order
# subsections are written when a fold creates a new wave.
TYPES: tuple[tuple[str, str], ...] = (
    ("added", "Added"),
    ("changed", "Changed"),
    ("internal", "Changed / Internal"),
    ("deprecated", "Deprecated"),
    ("fixed", "Fixed"),
    ("removed", "Removed"),
    ("security", "Security"),
)
HEADING_BY_TYPE = dict(TYPES)

FRAGMENT_DIR = "changelog.d"
CHANGELOG = "CHANGELOG.md"
# Files the directory carries for humans, never folded.
NOT_FRAGMENTS = {"readme.md"}

NAME_RE = re.compile(
    r"^(?:(\d+)-)?([A-Za-z0-9][A-Za-z0-9._-]*)\.("
    + "|".join(re.escape(t) for t, _ in TYPES)
    + r")\.md$"
)

CONFLICT_RE = re.compile(r"^(<{7}|>{7}|={7})( |$)")


class Fragment:
    __slots__ = ("path", "number", "slug", "type", "heading", "body")

    def __init__(self, path: Path, number: int, slug: str, type_: str, body: list[str]):
        self.path = path
        self.number = number
        self.slug = slug
        self.type = type_
        self.heading = HEADING_BY_TYPE[type_]
        self.body = body

    @property
    def sort_key(self) -> tuple[int, str]:
        # Newest PR number first; an unnumbered fragment sorts last.
        return (-self.number, self.path.name)


def repo_root(explicit: str | None) -> Path:
    if explicit:
        return Path(explicit).resolve()
    here = Path.cwd().resolve()
    for candidate in (here, *here.parents):
        if (candidate / ".git").exists():
            return candidate
    return here


def collect(root: Path) -> tuple[list[Fragment], list[str]]:
    """Return (fragments, problems). Names and bodies are validated here."""
    fragments: list[Fragment] = []
    problems: list[str] = []
    directory = root / FRAGMENT_DIR
    if not directory.is_dir():
        return fragments, problems
    for path in sorted(directory.iterdir()):
        name = path.name
        if not path.is_file() or name.startswith(".") or name.startswith("_"):
            continue
        if name.lower() in NOT_FRAGMENTS:
            continue
        match = NAME_RE.match(name)
        if not match:
            problems.append(
                f"{FRAGMENT_DIR}/{name}: name must be "
                "'<pr-number>-<slug>.<added|changed|internal|deprecated|fixed|removed|security>.md'"
            )
            continue
        number = int(match.group(1)) if match.group(1) else 0
        lines = path.read_text(encoding="utf-8").splitlines()
        while lines and not lines[0].strip():
            lines.pop(0)
        while lines and not lines[-1].strip():
            lines.pop()
        if not lines:
            problems.append(f"{FRAGMENT_DIR}/{name}: empty fragment")
            continue
        if not lines[0].startswith("- "):
            problems.append(f"{FRAGMENT_DIR}/{name}: must start with '- ' (the bullet itself)")
            continue
        if any(line.startswith("#") for line in lines):
            problems.append(f"{FRAGMENT_DIR}/{name}: no headings inside a fragment")
            continue
        fragments.append(Fragment(path, number, match.group(2), match.group(3), lines))
    return fragments, problems


def wave(fragments: list[Fragment]) -> list[str]:
    """The block of subsections a fold inserts, newest PR first."""
    block: list[str] = []
    for type_, heading in TYPES:
        group = sorted((f for f in fragments if f.type == type_), key=lambda f: f.sort_key)
        if not group:
            continue
        if block:
            block.append("")
        block.append(f"### {heading}")
        block.append("")
        for index, fragment in enumerate(group):
            if index:
                block.append("")
            block.extend(fragment.body)
    return block


def target_index(lines: list[str], target: str) -> int | None:
    pattern = re.compile(r"^## \[" + re.escape(target) + r"\]")
    for index, line in enumerate(lines):
        if pattern.match(line):
            return index
    return None


def fold(root: Path, target: str, dry_run: bool) -> int:
    fragments, problems = collect(root)
    for problem in problems:
        print(f"error: {problem}", file=sys.stderr)
    if problems:
        return 1

    changelog = root / CHANGELOG
    if not changelog.is_file():
        print(f"error: {CHANGELOG} not found in {root}", file=sys.stderr)
        return 1

    if not fragments:
        print(f"{FRAGMENT_DIR}/: no fragments to fold — {CHANGELOG} left alone")
        return 0

    text = changelog.read_text(encoding="utf-8")
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if CONFLICT_RE.match(line):
            print(
                f"error: {CHANGELOG}:{index + 1}: unresolved conflict marker — "
                "resolve the file before folding",
                file=sys.stderr,
            )
            return 1

    anchor = target_index(lines, target)
    if anchor is None:
        print(
            f"error: {CHANGELOG}: no '## [{target}]' release heading — "
            "nothing folded (run with --dry-run to see the entries)",
            file=sys.stderr,
        )
        return 1

    block = wave(fragments)
    tail = lines[anchor + 1 :]
    while tail and not tail[0].strip():
        tail.pop(0)
    updated = lines[: anchor + 1] + [""] + block + [""] + tail
    new_text = "\n".join(updated) + "\n"

    types: dict[str, int] = {}
    for fragment in fragments:
        types[fragment.heading] = types.get(fragment.heading, 0) + 1
    summary = ", ".join(
        f"{heading} ×{types[heading]}" for _, heading in TYPES if heading in types
    )

    if dry_run:
        diff = difflib.unified_diff(
            text.splitlines(keepends=True),
            new_text.splitlines(keepends=True),
            fromfile=f"a/{CHANGELOG}",
            tofile=f"b/{CHANGELOG}",
        )
        sys.stdout.writelines(diff)
        print(
            f"\ndry run: would fold {len(fragments)} fragment(s) into "
            f"'## [{target}]' ({summary}) and delete:"
        )
        for fragment in sorted(fragments, key=lambda f: f.path.name):
            print(f"  {FRAGMENT_DIR}/{fragment.path.name}")
        return 0

    changelog.write_text(new_text, encoding="utf-8")
    for fragment in fragments:
        fragment.path.unlink()
    print(
        f"folded {len(fragments)} fragment(s) into '## [{target}]' ({summary}); "
        f"deleted {len(fragments)} file(s) from {FRAGMENT_DIR}/"
    )
    return 0


def check(root: Path) -> int:
    fragments, problems = collect(root)
    for problem in problems:
        print(f"error: {problem}", file=sys.stderr)
    if problems:
        return 1
    print(f"{FRAGMENT_DIR}/: {len(fragments)} fragment(s), no findings")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Validate and fold changelog.d/ fragments into CHANGELOG.md."
    )
    # --root belongs to the subcommands, so it can be given where the caller
    # thinks it belongs: `changelog-assemble.py fold --root <dir> --dry-run`.
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--root", help="repository root (default: the git toplevel)")
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("check", parents=[common], help="validate fragment names and bodies")

    fold_parser = sub.add_parser(
        "fold", parents=[common], help="write the fragments into the changelog"
    )
    fold_parser.add_argument(
        "--target",
        default="Unreleased",
        help="release heading to fold into, without brackets (default: Unreleased)",
    )
    fold_parser.add_argument(
        "--dry-run", action="store_true", help="print the diff; write and delete nothing"
    )

    args = parser.parse_args(argv)
    root = repo_root(args.root)
    if args.command == "check":
        return check(root)
    return fold(root, args.target, args.dry_run)


if __name__ == "__main__":
    raise SystemExit(main())
