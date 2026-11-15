#!/usr/bin/env python3
"""pin_fence.py — the shared core for dependency-pin fences: ONE version, everywhere.

Generalized from check-gonuts-pin.py (#791) when the cdk-go pin needed the
same doctrine (main split three carriers at v0.17.3 and one at v0.18.1 while
#1190 converged every carrier on v0.18.3). The rule is deliberately dumb and
identical for every fenced module:

  **every `go.mod` in the tree that references the module references THE
  version named in packaging/build-inputs.json (.<key>.version),
  byte-identically.**

Carriers are DISCOVERED by glob (repo-root ``rglob("go.mod")``, the same
enumeration check-toolchain-parity.py uses) — never by a hand-maintained
list, because hand lists are exactly how the gonuts pin drifted (#791).

Two legs, on purpose (see the gonuts checker's docstring for the full
history; the same policy governs every fence built on this core):

  * offline (default): identity only — manifest vs every carrier, plus a
    go.sum presence check per carrier. Fast enough for hooks/pre-commit;
  * ``--latest`` (CI / release-check only, needs a token or plain HTTPS):
    the manifest version must be a REAL tag of the upstream repository and
    must be its NEWEST stable tag.

A fence may also refuse `replace` directives on the fenced module
(``refuse_replaces=True``): a replace voids the require-line pin the
manifest declares — the fork is what actually builds — so silently
tolerating one turns the identity leg into decoration (the Amperstrand
fork-replace class, #1149).

The per-fence wrappers (check-gonuts-pin.py, check-cdkgo-pin.py) hold the
facts (manifest key, module path, repo, fix script) and the incident
narrative in their docstrings; this module holds the rule ONCE.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
import urllib.request
from dataclasses import dataclass
from pathlib import Path

MANIFEST_REL = "packaging/build-inputs.json"


@dataclass(frozen=True)
class PinFence:
    """Every fact one pin fence declares. The wrappers own these; nothing else."""

    manifest_key: str  # build-inputs.json key, e.g. "gonuts" / "cdk_go"
    module: str  # the fenced Go module path
    repo: str  # "owner/name" the --latest leg audits (must appear in .repo)
    fix_script: str  # the one-run rewriter, e.g. "scripts/bump-gonuts.sh"
    drift_title: str  # e.g. "GONUTS PIN DRIFT" — names the failure block
    add_hint: str  # manifest-key-missing remedy, incident-cited
    carrierless_note: str  # why a tree with zero carriers is refused
    history_note: str  # closing Fix line citation — the incident this fence exists for
    stale_note: str  # --latest staleness remedy tail
    refuse_replaces: bool = False  # refuse replace directives on the module
    replace_note: str = ""  # remedy tail when a replace is found


def pin_pattern(fence: PinFence) -> re.Pattern[str]:
    # A require line, in BOTH shapes go.mod allows: the block form
    # ('\tmodule v0.13.0 // indirect') and the single-line form
    # ('require module v0.10.0' — the shape that hid token-recovery's stale
    # gonuts pin from the first version of this checker). The version is the
    # next whitespace-delimited token (a tag vX.Y.Z or a pseudo-version).
    return re.compile(
        r"^\s*(?:require\s+)?" + re.escape(fence.module) + r"\s+(v\S+)", re.MULTILINE
    )


def replace_pattern(fence: PinFence) -> re.Pattern[str]:
    # A replace on the fenced module, in BOTH shapes: 'replace M => X v1'
    # and the block form where the line starts with the module path itself.
    # The optional pre-'=>' version pins the 'replace M v1 => X v2' form.
    return re.compile(
        r"^\s*(?:replace\s+)?" + re.escape(fence.module) + r"(?:\s+v\S+)?\s*=>\s*(\S+)",
        re.MULTILINE,
    )


def manifest(root: Path, fence: PinFence) -> tuple[str, str]:
    data = json.loads((root / MANIFEST_REL).read_text())
    block = data.get(fence.manifest_key)
    if not isinstance(block, dict) or not block.get("version"):
        raise SystemExit(
            f"❌ {MANIFEST_REL} has no .{fence.manifest_key}.version — the pin's "
            f"single source of truth is missing. {fence.add_hint} or run "
            f"{fence.fix_script} <version>."
        )
    return str(block["version"]), str(block.get("repo", ""))


def carriers(root: Path, fence: PinFence) -> list[Path]:
    """Every go.mod in the tree that references the module — by glob, not list."""
    pins = pin_pattern(fence)
    found = []
    for mod in sorted(root.rglob("go.mod")):
        if ".git" in mod.parts:
            continue
        text = mod.read_text(encoding="utf-8", errors="replace")
        if pins.search(text) or (fence.refuse_replaces and replace_pattern(fence).search(text)):
            found.append(mod)
    return found


def check_identity(root: Path, fence: PinFence, truth: str) -> list[tuple[Path, str]]:
    """Every carrier pins `truth`; every carrier's go.sum knows that version."""
    drift: list[tuple[Path, str]] = []
    pins = pin_pattern(fence)
    for mod in carriers(root, fence):
        text = mod.read_text(encoding="utf-8", errors="replace")
        rel = mod.relative_to(root).as_posix()
        for pin in pins.findall(text):
            if pin != truth:
                drift.append(
                    (
                        mod,
                        f"{rel}: pins {fence.module} {pin}, manifest says {truth} — run {fence.fix_script} {truth}",
                    )
                )
        # go.sum presence: a carrier whose sum file never recorded the truth
        # version is a half-update (the exact #780-era miss shape).
        sumfile = mod.parent / "go.sum"
        if sumfile.is_file():
            sumtext = sumfile.read_text(encoding="utf-8", errors="replace")
            if fence.module not in sumtext or truth not in sumtext:
                drift.append(
                    (
                        sumfile,
                        f"{sumfile.relative_to(root).as_posix()}: does not record {fence.module} {truth} — the bump never reached this module's sum file",
                    )
                )
    return drift


def check_replaces(root: Path, fence: PinFence) -> list[tuple[Path, str]]:
    """A replace on the fenced module voids the pin — refuse it, named."""
    if not fence.refuse_replaces:
        return []
    drift: list[tuple[Path, str]] = []
    pattern = replace_pattern(fence)
    for mod in sorted(root.rglob("go.mod")):
        if ".git" in mod.parts:
            continue
        match = pattern.search(mod.read_text(encoding="utf-8", errors="replace"))
        if match:
            rel = mod.relative_to(root).as_posix()
            drift.append(
                (
                    mod,
                    f"{rel}: replaces {fence.module} with {match.group(1)} — a replace "
                    f"voids the pin this fence guards. {fence.replace_note}",
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


def repo_tags(fence: PinFence) -> list[str]:
    """All tags of the fenced repo, via gh when available (auth, rate limits), else HTTPS."""
    if shutil.which("gh"):
        proc = subprocess.run(
            ["gh", "api", f"repos/{fence.repo}/tags", "--paginate", "--jq", ".[].name"],
            capture_output=True,
            text=True,
            timeout=60,
        )
        if proc.returncode == 0:
            return [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        raise SystemExit(f"❌ gh could not list {fence.repo} tags: {proc.stderr.strip()[:160]}")
    request = urllib.request.Request(
        f"https://api.github.com/repos/{fence.repo}/tags?per_page=100",
        headers={"Accept": "application/vnd.github+json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return [entry["name"] for entry in json.loads(response.read().decode())]
    except OSError as error:  # HTTPError/URLError/timeout — transport, not verdict
        raise SystemExit(f"❌ could not list {fence.repo} tags over HTTPS: {error}")


def check_latest(fence: PinFence, truth: str, repo_url: str, failures: list[str], notes: list[str]) -> None:
    if fence.repo not in repo_url:
        failures.append(
            f"manifest .{fence.manifest_key}.repo is {repo_url!r} but this checker audits "
            f"{fence.repo} — update one of them; auditing the wrong repository silently "
            "is worse than failing"
        )
        return
    tags = repo_tags(fence)
    if truth not in tags:
        failures.append(
            f"manifest pins {fence.module} {truth}, which is NOT a tag of {fence.repo} "
            f"(integration pseudo-version? branches only — tag upstream, then "
            f"{fence.fix_script} <tag>)"
        )
        return
    newest = max(tags, key=version_key)
    if newest != truth:
        failures.append(
            f"{fence.repo} has a newer stable tag {newest} than the pinned {truth} — "
            f"bump deliberately: {fence.fix_script} {newest} {fence.stale_note}"
        )
    else:
        notes.append(f"manifest pin {truth} is {fence.repo}'s newest stable tag")


# --- entry ------------------------------------------------------------------


def run(fence: PinFence, argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=f"The {fence.module} pin is one version, everywhere.")
    parser.add_argument("--root", default=None, help="repository root (default: this checkout)")
    parser.add_argument(
        "--latest",
        action="store_true",
        help="also verify the pin is the upstream repository's newest stable tag (CI/release mode; network)",
    )
    args = parser.parse_args(argv)

    root = Path(args.root).resolve() if args.root else Path(__file__).resolve().parent.parent.parent
    truth, repo_url = manifest(root, fence)

    failures: list[str] = []
    notes: list[str] = []

    found = carriers(root, fence)
    if not found:
        failures.append(
            f"no go.mod in the tree references {fence.module} — {fence.carrierless_note}; "
            "if it ever is, delete this check with the manifest key in the same commit"
        )
    else:
        notes.append(f"carriers discovered by glob: {len(found)} module(s)")

    for _, problem in check_identity(root, fence, truth):
        failures.append(problem)
    for _, problem in check_replaces(root, fence):
        failures.append(problem)

    if args.latest:
        check_latest(fence, truth, repo_url, failures, notes)

    print(f"  manifest pin ({MANIFEST_REL} .{fence.manifest_key}.version): {truth}")
    for note in notes:
        print(f"  {note}")
    if failures:
        print(f"\n❌ {fence.drift_title} — {len(failures)} finding(s):")
        for problem in failures:
            print(f"  🔴 {problem}")
        print(
            f"\nFix: {fence.fix_script} <version> rewrites the manifest and every "
            f"carrier together. Never edit one go.mod by hand — that is how the "
            f"pin drifted {fence.history_note}."
        )
        return 1
    print(f"✅ one pin, everywhere: {fence.module} {truth} across {len(found)} carrier(s).")
    return 0
