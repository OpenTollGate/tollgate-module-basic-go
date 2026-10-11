#!/usr/bin/env python3
"""check-changelog-fragments.py — every fragment names its own change.

`scripts/changelog-assemble.py check` validates a fragment's SHAPE (name
grammar, bullet body, no headings). It deliberately accepts a fragment
without a number, because the fold only needs a sortable key. This checker
validates the ENTRY'S IDENTITY instead, and every rule below exists because
a shape-valid fragment still managed to be untraceable:

  * ``changelog.d/vm-campaign.added.md`` carried no number at all — its
    change is merged PR #704, but nothing in the filename or the body says
    so, and the fold would have written a release bullet with no reference.
  * a fragment that links a FOREIGN pull request — the number of work the
    change relates to rather than the change itself (the #631/#743 class:
    research/audit numbers that are easy to paste because they are the
    numbers in your notes) — folds a confident-looking link to the wrong
    thing into the release notes.

Rules (exit 1 on any violation):

  1. the filename must carry a PR/issue number:
     ``<number>-<slug>.<type>.md`` (README of changelog.d/);
  2. the body must reference that same number at least once — as the
     markdown link the README asks for (``([#N](.../pull/N))`` or
     ``.../issues/N``) or as a bare ``#N`` mention (the #502 fragment's
     style, where the tracking issue IS the identity);
  3. every markdown pull/issue LINK in the body must point at the
    fragment's own number — bare ``#N`` mentions of other work are prose
    and stay free;
  4. no placeholder text anywhere in name or body: ``TBD``, ``TODO``,
     ``FIXME``, ``XXX``, a literal ``#NNN``, ``<pr>``, ``<number>``,
     ``PULL_REQUEST_NUMBER``, or a ``0-`` filename prefix.

With ``--online``, every referenced number is additionally verified to
EXIST on github.com (PR or issue — the API serves both under /issues/N).
Online mode is what CI runs (it has a token and a network); the default
offline mode is what the pre-commit hook runs. Online failures are loud,
never downgraded: a fragment citing a number nothing owns is exactly the
drift this fence exists to catch.

Usage:
  python3 tests/contract/check-changelog-fragments.py [--root DIR] [--online]

Exit codes: 0 = every fragment traceable; 1 = findings listed on stderr.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path

REPO = "OpenTollGate/tollgate-module-basic-go"
API = f"https://api.github.com/repos/{REPO}/issues"

# Kept in lockstep with scripts/changelog-assemble.py's TYPES (a fragment
# type this list does not know would silently skip the number rules).
TYPES = ("added", "changed", "internal", "deprecated", "fixed", "removed", "security")
NAME_RE = re.compile(r"^(\d+)-([A-Za-z0-9][A-Za-z0-9._-]*)\.(" + "|".join(TYPES) + r")\.md$")
NOT_FRAGMENTS = {"readme.md"}

LINK_RE = re.compile(
    r"\[#(\d+)\]\(https://github\.com/" + re.escape(REPO) + r"/(?:pull|issues)/(\d+)\)"
)
BARE_RE = re.compile(r"(?<![\w/[])#(\d+)\b")

# Placeholder vocabulary. Each pattern is anchored so legitimate prose
# cannot trip it by accident, and each maps to a real failure shape:
# a not-yet-known PR number, a template left unfilled, a filename that
# was never renamed off its template.
PLACEHOLDER_RES: tuple[tuple[re.Pattern[str], str], ...] = (
    (re.compile(r"\bTBD\b"), "TBD"),
    (re.compile(r"\bTODO\b"), "TODO"),
    (re.compile(r"\bFIXME\b"), "FIXME"),
    (re.compile(r"\bXXX\b"), "XXX"),
    (re.compile(r"#NNN\b"), "the literal '#NNN'"),
    (re.compile(r"<(?:pr|issue|number)>", re.IGNORECASE), "a <pr>/<issue>/<number> template token"),
    (re.compile(r"\bPULL_REQUEST_(?:NUMBER|ID)\b"), "a CI template variable"),
)


class Finding:
    def __init__(self, fragment: str, problem: str):
        self.fragment = fragment
        self.problem = problem


def fragments(root: Path) -> list[Path]:
    directory = root / "changelog.d"
    if not directory.is_dir():
        return []
    out = []
    for path in sorted(directory.iterdir()):
        if not path.is_file() or path.name.startswith(".") or path.name.startswith("_"):
            continue
        if path.name.lower() in NOT_FRAGMENTS:
            continue
        out.append(path)
    return out


def check_fragment(path: Path) -> tuple[list[Finding], set[int]]:
    """Structural checks for one fragment; returns findings + referenced numbers."""
    findings: list[Finding] = []
    referenced: set[int] = set()
    name = path.name
    rel = f"changelog.d/{name}"

    match = NAME_RE.match(name)
    if not match:
        findings.append(
            Finding(
                rel,
                "filename must be '<pr-or-issue-number>-<slug>.<type>.md' with a "
                f"real number — the fold sorts on it and the release notes cite it "
                f"(types: {'|'.join(TYPES)})",
            )
        )
        return findings, referenced
    number = int(match.group(1))
    if number == 0:
        findings.append(Finding(rel, "filename number is 0 — a placeholder, not a reference"))
        return findings, referenced
    referenced.add(number)

    body = path.read_text(encoding="utf-8")

    links = LINK_RE.findall(body)
    # A well-formed link's label and URL must agree (#N -> .../N); a
    # mismatch is a hand-edited link pointing somewhere its text does not.
    for label, target in links:
        if label != target:
            findings.append(
                Finding(rel, f"link label #{label} points at /{target} — label and target disagree")
            )
        referenced.add(int(target))

    own_references = [int(n) for n in BARE_RE.findall(body)] + [int(l) for l, _ in links]
    if number not in own_references:
        findings.append(
            Finding(
                rel,
                f"the body never references #{number} — cite it as the trailing link "
                f"([#{number}](https://github.com/{REPO}/pull/{number})) or as a bare "
                f"#{number} mention",
            )
        )

    for label, target in links:
        if int(target) != number:
            findings.append(
                Finding(
                    rel,
                    f"links #{target}, but this fragment is #{number}'s entry — a link "
                    f"to a foreign PR/issue folds the wrong reference into the release "
                    f"notes (bare mentions of other numbers are fine; links are not)",
                )
            )

    # Inline-code spans quote the naming grammar itself (`<pr>-<slug>.<type>.md`
    # in the #679 fragment) — that is prose, not an unfilled template, so the
    # scan runs on the body with code spans removed.
    prose = re.sub(r"`[^`]*`", "", body)
    for pattern, what in PLACEHOLDER_RES:
        hit = pattern.search(prose) or pattern.search(name)
        if hit:
            findings.append(Finding(rel, f"placeholder text ({what}) — a fragment is the release entry itself"))

    return findings, referenced


def verify_online(numbers: set[int]) -> list[Finding]:
    """Every number must exist as a PR or issue on GitHub."""
    findings: list[Finding] = []
    for number in sorted(numbers):
        exists, detail = number_exists(number)
        if not exists:
            findings.append(
                Finding(
                    "changelog.d/ (online)",
                    f"#{number} does not exist on GitHub ({detail}) — a fragment must "
                    f"cite a real PR or issue",
                )
            )
    return findings


def number_exists(number: int) -> tuple[bool, str]:
    """One GitHub API lookup, via gh when it is available (auth, rate limits),
    plain HTTPS otherwise. Any transport error is reported, never swallowed."""
    if shutil.which("gh"):
        proc = subprocess.run(
            ["gh", "api", f"repos/{REPO}/issues/{number}"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if proc.returncode == 0:
            return True, "gh"
        # gh speaks for GitHub: 404 is a verdict, anything else is transport.
        if "Not Found" in proc.stderr:
            return False, "gh: not found"
        return False, f"gh: {proc.stderr.strip()[:120]}"
    url = f"{API}/{number}"
    request = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            json.loads(response.read().decode("utf-8"))
            return True, "api"
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return False, "api: not found"
        return False, f"api: HTTP {error.code}"
    except (urllib.error.URLError, TimeoutError) as error:
        return False, f"api: {error}"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=None, help="repository root (default: this checkout)")
    parser.add_argument(
        "--online",
        action="store_true",
        help="also verify every referenced number exists on GitHub (CI mode)",
    )
    args = parser.parse_args(argv)

    root = Path(args.root).resolve() if args.root else Path(__file__).resolve().parent.parent.parent

    findings: list[Finding] = []
    referenced: set[int] = set()
    files = fragments(root)
    for path in files:
        path_findings, path_numbers = check_fragment(path)
        findings.extend(path_findings)
        referenced |= path_numbers

    if args.online:
        findings.extend(verify_online(referenced))

    if findings:
        print(f"❌ CHANGELOG FRAGMENT DRIFT — {len(findings)} finding(s):")
        for finding in findings:
            print(f"  🔴 {finding.fragment}: {finding.problem}")
        print(
            "\nFix: name the fragment after its own PR/issue "
            "(changelog.d/<number>-<slug>.<type>.md) and reference that number in "
            "the body. Open the PR or its tracking issue FIRST if the number is "
            "not known yet — that is why issue numbers are allowed."
        )
        return 1
    print(f"✅ {len(files)} fragment(s): every one names its own real PR/issue number.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
