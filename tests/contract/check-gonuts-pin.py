#!/usr/bin/env python3
"""check-gonuts-pin.py — the gonuts-tollgate pin is ONE version, everywhere.

The wallet fork is the repo's most fund-safety-critical dependency, and it
drifted three ways in one release week (#791):

  * a carrier was missed because docs enumerated "four go.mod files" while a
    fifth lived outside the enumeration (scripts/token-recovery at v0.10.0);
  * two open PRs pinned the same lines to different states (a tag vs a
    pseudo-version), a guaranteed conflict and a near-miss on shadowing;
  * a newer fork tag carrying an open-P2 fix existed for days unnoticed.

The rule this checker enforces is therefore deliberately dumb:

  **every `go.mod` in the tree that references
  `github.com/OpenTollGate/gonuts-tollgate` references THE version named in
  packaging/build-inputs.json (.gonuts.version), byte-identically.**

Carriers are DISCOVERED by glob (repo-root ``rglob("go.mod")``, the same
enumeration check-toolchain-parity.py uses for the go-directive ceiling) —
never by a hand-maintained list, because hand lists are exactly how the
drift happened. A module that does not reference the fork is not a carrier
and is not judged.

Two legs, on purpose:

  * offline (default): identity only — manifest vs every carrier, plus a
    go.sum presence check per carrier. Fast enough for hooks/pre-commit;
  * ``--latest`` (CI / release-check only, needs a token or plain HTTPS):
    the manifest version must be a REAL tag of the fork repository and must
    be its NEWEST stable tag. Staleness is a release-time verdict, not a
    per-commit one — failing every open PR the moment the fork tags
    something new would be noise; refusing to cut a release on a stale pin
    is the catch (#705's fix sat unnoticed in v0.13.2 this way).

Pseudo-version pins (the #771 integration pattern) are NOT refused offline:
if the manifest names the pseudo-version and every carrier matches it, the
identity rule holds — the branch is self-consistent. The ``--latest`` leg
is what demands a real, newest tag, so such a branch is release-gated, not
silently tolerated.

Fix for any refusal: `scripts/bump-gonuts.sh <version>` rewrites the
manifest and every carrier together — a half-update cannot happen.

Usage:
  python3 tests/contract/check-gonuts-pin.py [--root DIR] [--latest]

Exit codes: 0 = one pin, everywhere; 1 = drift, itemized.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
import urllib.request
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parent.parent.parent
MANIFEST_REL = "packaging/build-inputs.json"
MODULE = "github.com/OpenTollGate/gonuts-tollgate"
FORK_REPO = "OpenTollGate/gonuts-tollgate"  # verified against .gonuts.repo

# A require line for the fork, in BOTH shapes go.mod allows: the block form
# ('\tgithub.com/... v0.13.0 // indirect') and the single-line form
# ('require github.com/... v0.10.0' — the shape that hid token-recovery's
# stale pin from the first version of this checker). The version is the next
# whitespace-delimited token (a tag vX.Y.Z or a pseudo-version
# vX.Y.Z-0.timestamp-sha). replace/exclude directives do not match: their
# lines start with those keywords, not an optional 'require'.
PIN_RE = re.compile(r"^\s*(?:require\s+)?" + re.escape(MODULE) + r"\s+(v\S+)", re.MULTILINE)

failures: list[str] = []
notes: list[str] = []


def manifest(root: Path) -> tuple[str, str]:
    data = json.loads((root / MANIFEST_REL).read_text())
    gonuts = data.get("gonuts")
    if not isinstance(gonuts, dict) or not gonuts.get("version"):
        raise SystemExit(
            f"❌ {MANIFEST_REL} has no .gonuts.version — the pin's single source "
            "of truth is missing. Add the gonuts block (see #791) or run "
            "scripts/bump-gonuts.sh <version>."
        )
    return str(gonuts["version"]), str(gonuts.get("repo", ""))


def carriers(root: Path) -> list[Path]:
    """Every go.mod in the tree that references the fork — by glob, not list."""
    found = []
    for mod in sorted(root.rglob("go.mod")):
        if ".git" in mod.parts:
            continue
        if PIN_RE.search(mod.read_text(encoding="utf-8", errors="replace")):
            found.append(mod)
    return found


def check_identity(root: Path, truth: str) -> list[tuple[Path, str]]:
    """Every carrier pins `truth`; every carrier's go.sum knows that version."""
    drift: list[tuple[Path, str]] = []
    for mod in carriers(root):
        text = mod.read_text(encoding="utf-8", errors="replace")
        rel = mod.relative_to(root).as_posix()
        for pin in PIN_RE.findall(text):
            if pin != truth:
                drift.append((mod, f"{rel}: pins {MODULE} {pin}, manifest says {truth} — run scripts/bump-gonuts.sh {truth}"))
        # go.sum presence: a carrier whose sum file never recorded the truth
        # version is a half-update (the exact #780-era miss shape).
        sumfile = mod.parent / "go.sum"
        if sumfile.is_file():
            sumtext = sumfile.read_text(encoding="utf-8", errors="replace")
            if MODULE not in sumtext or truth not in sumtext:
                drift.append(
                    (
                        sumfile,
                        f"{sumfile.relative_to(root).as_posix()}: does not record {MODULE} {truth} — the bump never reached this module's sum file",
                    )
                )
    return drift


# --- the --latest leg -------------------------------------------------------


def version_key(tag: str) -> tuple:
    """Semver-ish sort key; refuses to order non-vX.Y.Z shapes (they sort low)."""
    m = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)(.*)", tag)
    if not m:
        return (-1, 0, 0, tag)
    return (int(m.group(1)), int(m.group(2)), int(m.group(3)), m.group(4) or "")


def fork_tags() -> list[str]:
    """All tags of the fork, via gh when available (auth, rate limits), else HTTPS."""
    if shutil.which("gh"):
        proc = subprocess.run(
            ["gh", "api", f"repos/{FORK_REPO}/tags", "--paginate", "--jq", ".[].name"],
            capture_output=True,
            text=True,
            timeout=60,
        )
        if proc.returncode == 0:
            return [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        raise SystemExit(f"❌ gh could not list {FORK_REPO} tags: {proc.stderr.strip()[:160]}")
    request = urllib.request.Request(
        f"https://api.github.com/repos/{FORK_REPO}/tags?per_page=100",
        headers={"Accept": "application/vnd.github+json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return [entry["name"] for entry in json.loads(response.read().decode())]
    except OSError as error:  # HTTPError/URLError/timeout — transport, not verdict
        raise SystemExit(f"❌ could not list {FORK_REPO} tags over HTTPS: {error}")


def check_latest(truth: str, repo_url: str) -> None:
    if FORK_REPO not in repo_url:
        failures.append(
            f"manifest .gonuts.repo is {repo_url!r} but this checker audits {FORK_REPO} — "
            "update one of them; auditing the wrong fork silently is worse than failing"
        )
        return
    tags = fork_tags()
    if truth not in tags:
        failures.append(
            f"manifest pins {MODULE} {truth}, which is NOT a tag of {FORK_REPO} "
            "(integration pseudo-version? branches only — tag the fork, then "
            "scripts/bump-gonuts.sh <tag>)"
        )
        return
    newest = max(tags, key=version_key)
    if newest != truth:
        failures.append(
            f"fork {FORK_REPO} has a newer stable tag {newest} than the pinned {truth} — "
            f"bump deliberately: scripts/bump-gonuts.sh {newest} (then read its changelog; "
            "this is how the #705 fix in v0.13.2 nearly missed a release)"
        )
    else:
        notes.append(f"manifest pin {truth} is the fork's newest stable tag")


# --- entry ------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="The gonuts-tollgate pin is one version, everywhere.")
    parser.add_argument("--root", default=None, help="repository root (default: this checkout)")
    parser.add_argument("--latest", action="store_true", help="also verify the pin is the fork's newest stable tag (CI/release mode; network)")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve() if args.root else DEFAULT_ROOT
    truth, repo_url = manifest(root)

    found = carriers(root)
    if not found:
        failures.append(
            "no go.mod in the tree references " + MODULE + " — a wallet-less "
            "tree is not a state this repo has ever been in; if it ever is, delete "
            "this check with the manifest key in the same commit"
        )
    else:
        notes.append(f"carriers discovered by glob: {len(found)} module(s)")

    for path, problem in check_identity(root, truth):
        failures.append(problem)

    if args.latest:
        check_latest(truth, repo_url)

    print(f"  manifest pin ({MANIFEST_REL} .gonuts.version): {truth}")
    for note in notes:
        print(f"  {note}")
    if failures:
        print(f"\n❌ GONUTS PIN DRIFT — {len(failures)} finding(s):")
        for problem in failures:
            print(f"  🔴 {problem}")
        print(
            "\nFix: scripts/bump-gonuts.sh <version> rewrites the manifest and every "
            "carrier together. Never edit one go.mod by hand — that is how the pin drifted (#791)."
        )
        return 1
    print(f"✅ one pin, everywhere: {MODULE} {truth} across {len(found)} carrier(s).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
