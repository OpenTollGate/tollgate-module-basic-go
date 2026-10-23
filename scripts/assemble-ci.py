#!/usr/bin/env python3
"""Render the Test workflow twins from ci.d/ job fragments.

WHY A GENERATOR
  `.github/workflows/test.yml` and `.ngit/act/workflows/test.yml` are two
  copies of one workflow with deliberate lane differences (the GitHub lane
  carries the happy-path and browser jobs; the ngit act lane carries the
  packaging suites, the hygiene gutter and release-check-fast; go-test's
  fail-fast policy differs per lane). They were maintained by hand, and
  during the 2026-10-11→13 queue campaign the twin drifted three times —
  every CI/test lane appended jobs and steps to the same two documents, so
  ~20 PR rebases fought them (#920).

  The cure is the one the changelog already proved (#679): lanes ADD
  FILES. A job is a fragment under ci.d/, discovered by glob:

    ci.d/both/NN-<slug>.yml    job identical on both lanes
    ci.d/github/NN-<slug>.yml  GitHub-lane job, or this lane's variant of a
                               job that deliberately differs per lane
    ci.d/ngit/NN-<slug>.yml    the ngit twin's counterpart

  Two lanes adding two different jobs touch two different files and can
  never conflict. This tool is the single writer of both rendered copies
  ("generators write every copy" — the bump-gonuts.sh property): a
  half-update cannot happen, and `--check` (run in CI on both lanes and in
  the pre-commit fence) re-renders and diffs, so a hand-edit to a rendered
  copy — or a fragment changed without re-rendering — fails the build.

  When one job id carries genuine per-lane variants (one file per lane
  directory, same job id), the variants must agree on `strategy.matrix`:
  the module list is one fact declared once per job, and a lane that
  silently drops a matrix row from its twin's copy is exactly the drift
  class this tool exists to kill.

  The rendered files ARE committed, like the ngit release shards: a
  reviewer can read the workflow that runs without running a generator,
  and `act`/Actions need static YAML.

Substitution is deliberately textual, not YAML round-trip: these bodies
are full of `${{ ... }}` expressions, `${...}` shell expansions and
hard-won comment prose that a serializer would mangle or drop.

Usage:
  scripts/assemble-ci.py [--root PATH] [--check] [--list]

Exit codes:
  0  wrote / validated every target
  1  drift or fragment validation failure (each offender named)
  2  the invocation is unusable
"""

from __future__ import annotations

import argparse
import difflib
import re
import sys
from pathlib import Path

LANES: dict[str, str] = {
    "github": ".github/workflows/test.yml",
    "ngit": ".ngit/act/workflows/test.yml",
}
SHARED_DIR = "both"
README = "README.md"
FRAGMENT_RE = re.compile(r"^\d{2,3}-[a-z0-9][a-z0-9-]*\.yml$")
JOB_KEY_RE = re.compile(r"^  ([a-z0-9][a-z0-9-]*):$")

# One declaration of the workflow preamble both lanes share (name and
# triggers were byte-identical in every hand-maintained revision).
HEADER = """\
name: Test

on:
  push:
    branches: [main, master, develop]
  pull_request:
    branches: [main, master, develop]

jobs:
"""


def banner(lane: str) -> str:
    return f"""\
# ---------------------------------------------------------------------------
# GENERATED FILE -- DO NOT EDIT BY HAND.
#
#   fragments : ci.d/{{both,github,ngit}}/NN-<slug>.yml  (one job per file)
#   render    : python3 scripts/assemble-ci.py
#   check     : python3 scripts/assemble-ci.py --check   (CI, both lanes;
#               a hand-edit here, or a fragment changed without
#               re-rendering, fails the build)
#
# Adding a CI job means adding a fragment (#920) — lanes never edit this
# document, so two lanes can never conflict in it. A job identical on both
# lanes lives in ci.d/both/; a lane-only job lives in ci.d/{lane}/; a job
# that deliberately differs per lane has one variant file per lane
# directory carrying the same job id, and assemble-ci.py refuses variants
# whose strategy.matrix diverges (the matrix is one fact).
#
# Rendered target for the '{lane}' lane: {LANES[lane]}
# ---------------------------------------------------------------------------
"""


class Problem(Exception):
    pass


def first_key_line(text: str, name: str) -> tuple[str, int]:
    for i, line in enumerate(text.splitlines()):
        if line.lstrip().startswith("#") or not line.strip():
            continue
        m = JOB_KEY_RE.match(line)
        if not m:
            raise Problem(
                f"{name}: first non-comment line must be '  <job-id>:', "
                f"found {line!r}"
            )
        return m.group(1), i
    raise Problem(f"{name}: no job key line found")


def extract_matrix(text: str, name: str) -> list[str] | None:
    """The strategy.matrix block's entry lines, comments stripped, or None."""
    lines = text.splitlines()
    start = None
    for i, line in enumerate(lines):
        if re.match(r"^\s+matrix:\s*$", line):
            start = i + 1
            break
    if start is None:
        return None
    entries: list[str] = []
    for line in lines[start:]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if re.match(r"^\s{0,6}\S", line):
            break
        entries.append(line.rstrip())
    return entries or None


def load(root: Path) -> dict[str, list[tuple[str, Path, str]]]:
    """lane -> ordered [(job-id, path, text)]; raises Problem on drift."""
    cidir = root / "ci.d"
    if not cidir.is_dir():
        raise Problem("ci.d/ not found under the root")
    for entry in sorted(cidir.iterdir()):
        if entry.is_dir():
            if entry.name not in (SHARED_DIR, *LANES):
                raise Problem(
                    f"ci.d/{entry.name}/: not a lane directory — fragments "
                    f"live in ci.d/{{{SHARED_DIR},github,ngit}}/ (a typo here "
                    "would silently drop a job from a lane)"
                )
        elif entry.name != README:
            raise Problem(
                f"ci.d/{entry.name}: only lane directories and README.md "
                "belong in ci.d/"
            )
    order: dict[str, list[tuple[str, Path, str]]] = {lane: [] for lane in LANES}
    seen: dict[str, dict[str, Path]] = {lane: {} for lane in LANES}
    prefixes: dict[str, dict[str, Path]] = {lane: {} for lane in LANES}
    variants: dict[str, dict[str, Path]] = {}
    for lane in LANES:
        for directory in (cidir / SHARED_DIR, cidir / lane):
            if not directory.is_dir():
                continue
            for path in sorted(directory.glob("*.yml")):
                if not FRAGMENT_RE.match(path.name):
                    raise Problem(
                        f"ci.d/{directory.name}/{path.name}: fragment names are "
                        "NN-<slug>.yml (numeric prefix = job order in the "
                        "rendered workflow)"
                    )
                text = path.read_text(encoding="utf-8")
                if not text.endswith("\n"):
                    raise Problem(f"ci.d/{directory.name}/{path.name}: no final newline")
                job_id, _ = first_key_line(text, f"ci.d/{directory.name}/{path.name}")
                if job_id in seen[lane]:
                    raise Problem(
                        f"ci.d/{directory.name}/{path.name}: job '{job_id}' is "
                        f"already declared by {seen[lane][job_id]} — one job, one "
                        "owner (merge the fragments or rename the job)"
                    )
                seen[lane][job_id] = path
                prefix = path.name.split("-", 1)[0]
                if prefix in prefixes[lane]:
                    raise Problem(
                        f"ci.d/{directory.name}/{path.name}: order prefix "
                        f"'{prefix}' already used by {prefixes[lane][prefix]} — "
                        "the numeric prefix is the job's position in the "
                        "rendered workflow; two jobs cannot share one"
                    )
                prefixes[lane][prefix] = path
                order[lane].append((job_id, path, text))
                if directory.name in LANES:
                    variants.setdefault(job_id, {})[directory.name] = path
        order[lane].sort(key=lambda item: item[1].name)
    for lane in LANES:
        if not order[lane]:
            raise Problem(f"lane '{lane}' has no fragments — refusing to render "
                          f"an empty {LANES[lane]}")
    for job_id, bylane in sorted(variants.items()):
        if len(bylane) < 2:
            continue
        github_path = bylane["github"]
        ngit_path = bylane["ngit"]
        gh_matrix = extract_matrix(github_path.read_text(), github_path.name)
        ng_matrix = extract_matrix(ngit_path.read_text(), ngit_path.name)
        if gh_matrix != ng_matrix:
            raise Problem(
                f"job '{job_id}' has per-lane variants with diverging "
                f"strategy.matrix: {github_path} vs {ngit_path} — the matrix "
                "is one fact; only lane policy (fail-fast, steps a lane can "
                "reach) may differ between variants"
            )
    return order


def render(lane: str, fragments: list[tuple[str, Path, str]]) -> str:
    return (
        banner(lane)
        + HEADER
        + "\n".join(text.rstrip("\n") + "\n" for _j, _p, text in fragments)
    )


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=None, help="repository root (default: cwd's git root)")
    ap.add_argument("--check", action="store_true", help="diff rendered targets against the committed copies")
    ap.add_argument("--list", action="store_true", help="list fragments per lane and exit")
    args = ap.parse_args(argv)

    root = Path(args.root).resolve() if args.root else Path.cwd().resolve()
    try:
        order = load(root)
    except Problem as p:
        print(f"assemble-ci.py: {p}", file=sys.stderr)
        return 1

    if args.list:
        for lane in LANES:
            print(f"{lane} -> {LANES[lane]}")
            for job_id, path, _ in order[lane]:
                print(f"  {path.relative_to(root)}  ({job_id})")
        return 0

    drift = False
    for lane in LANES:
        target = root / LANES[lane]
        want = render(lane, order[lane])
        if args.check:
            have = target.read_text(encoding="utf-8") if target.exists() else None
            if have != want:
                drift = True
                print(
                    f"{LANES[lane]}: DRIFTED from ci.d/ fragments — re-run "
                    "`python3 scripts/assemble-ci.py` and commit the result.",
                    file=sys.stderr,
                )
                if have is not None:
                    diff = difflib.unified_diff(
                        have.splitlines(), want.splitlines(),
                        fromfile=f"{LANES[lane]} (committed)",
                        tofile=f"{LANES[lane]} (rendered)",
                        lineterm="",
                    )
                    for line in list(diff)[:40]:
                        print(line, file=sys.stderr)
            else:
                print(f"{LANES[lane]}: matches ci.d/ fragments")
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(want, encoding="utf-8")
            print(f"wrote {LANES[lane]}")
    return 1 if drift else 0


if __name__ == "__main__":
    sys.exit(main())
