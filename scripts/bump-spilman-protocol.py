#!/usr/bin/env python3
"""bump-spilman-protocol.py — move the Spilman protocol pin in ONE run.

AGENTS.md rule 5: "When copies must exist, one tool rewrites them all in a
single run ... so a half-update cannot happen." The pin lives in three places
(`packaging/build-inputs.json`, the Cargo requirement, `Cargo.lock`) and a
partial update is worse than a stale one: the manifest would name one revision
while the shipped binary is built from another, and channel ids derived from
different revisions are different channel ids.

What this does, in order:

  1. rewrite `.spilman_protocol.commit` (and `.cashu_commit`, if given) in the
     manifest;
  2. rewrite the `rev = "..."` in the sidecar's Cargo requirement;
  3. run `cargo update -p cdk-spilman` (and `-p cashu` if repinned) so
     Cargo.lock resolves what the requirement now says — a lock edit by hand is
     the drift this script exists to prevent;
  4. run the fence and print its verdict.

Refuses to run if the working tree has uncommitted changes to the three files
it owns, so a bump is never mixed with unrelated work.

Usage:
  python3 scripts/bump-spilman-protocol.py <new-commit> [--cashu-commit SHA] [--dry-run]
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "packaging" / "build-inputs.json"
CARGO = ROOT / "sidecar" / "spilman" / "Cargo.toml"
LOCK = ROOT / "sidecar" / "spilman" / "Cargo.lock"
FENCE = ROOT / "tests" / "contract" / "check-spilman-sidecar-surface.py"

HEX40 = re.compile(r"^[0-9a-f]{40}$")
SPILMAN_REPO = "https://github.com/SatsAndSports/cashu_spilman_channels.git"
REV_RE = re.compile(
    r'(git\s*=\s*"' + re.escape(SPILMAN_REPO) + r'"[^}]*?\brev\s*=\s*")(?P<value>[0-9a-f]{40})(")'
)


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def owned_paths() -> list[Path]:
    return [MANIFEST, CARGO, LOCK]


def ensure_clean() -> None:
    """Refuse to bump on top of unrelated edits to the files this owns."""
    rels = [str(path.relative_to(ROOT)) for path in owned_paths()]
    result = subprocess.run(
        ["git", "-C", str(ROOT), "status", "--porcelain", "--", *rels],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        fail(f"git status failed: {result.stderr.strip()}")
    dirty = [line for line in result.stdout.splitlines() if line.strip()]
    if dirty:
        fail(
            "these owned files already have changes; commit or revert them first so the "
            "bump is one attributable commit:\n  " + "\n  ".join(dirty)
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("commit", help="new cdk-spilman commit (40 hex)")
    parser.add_argument("--cashu-commit", help="new resolved cdk commit (40 hex)")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    if not HEX40.match(args.commit):
        fail(f"{args.commit!r} is not a 40-hex commit")
    if args.cashu_commit is not None and not HEX40.match(args.cashu_commit):
        fail(f"{args.cashu_commit!r} is not a 40-hex commit")

    ensure_clean()

    manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
    section = manifest.get("spilman_protocol")
    if not isinstance(section, dict):
        fail("no .spilman_protocol section in the manifest — add it before bumping")

    old_commit = section.get("commit")
    old_cashu = section.get("cashu_commit")
    section["commit"] = args.commit
    if args.cashu_commit:
        section["cashu_commit"] = args.cashu_commit

    cargo_text = CARGO.read_text(encoding="utf-8")
    if not REV_RE.search(cargo_text):
        fail(f"could not find the pinned rev for {SPILMAN_REPO} in {CARGO}")
    new_cargo = REV_RE.sub(lambda m: m.group(1) + args.commit + m.group(3), cargo_text)

    print(f"manifest .commit:        {old_commit} -> {args.commit}")
    if args.cashu_commit:
        print(f"manifest .cashu_commit:  {old_cashu} -> {args.cashu_commit}")
    print(f"Cargo.toml rev:          -> {args.commit}")
    print("Cargo.lock:              cargo update will resolve it")

    if args.dry_run:
        print("\n(dry run: nothing written)")
        return 0

    MANIFEST.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    CARGO.write_text(new_cargo, encoding="utf-8")

    update = ["cargo", "update", "-p", "cdk-spilman", "--manifest-path", str(CARGO.parent / "Cargo.toml")]
    print(f"\n$ {' '.join(update)}")
    result = subprocess.run(update, cwd=CARGO.parent, text=True)
    if result.returncode != 0:
        fail(
            "cargo update failed. The requirement and manifest are already rewritten; fix "
            "the cause and re-run, or `git checkout` the three files to abandon the bump."
        )

    if args.cashu_commit:
        cashu_update = [
            "cargo", "update", "-p", "cashu", "--manifest-path", str(CARGO.parent / "Cargo.toml")
        ]
        print(f"$ {' '.join(cashu_update)}")
        subprocess.run(cashu_update, cwd=CARGO.parent, text=True)

    print("\n$ python3 tests/contract/check-spilman-sidecar-surface.py")
    verdict = subprocess.run([sys.executable, str(FENCE)], text=True)
    if verdict.returncode != 0:
        fail("the fence refuses the result — do not commit this bump")
    print("\nBump complete. Review `git diff` on the three files, then commit once.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
