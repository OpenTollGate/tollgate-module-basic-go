#!/usr/bin/env python3
"""Contract: toolchain + build-flag parity across the two build paths.

The single source of truth for the Go toolchain is
``packaging/build-inputs.json`` (``.go.version``) — pinned to the official
golang of the digest-pinned OpenWrt SDK release (re-derived from the live
feeds by ``scripts/sdk-go-version.sh check``). Every OTHER build path —
the cloud-lab Docker images, CI workflow pins, any shortcut build — must
use the SAME toolchain and the SAME canonical build flags, so that:

  * the shortcut path reproduces the SDK path's binaries (same inputs,
    same flags, same toolchain ⇒ same SHA-256), and
    ``.github/workflows/repro-check.yml``'s path-parity job can prove it;
  * version/pinning drift between the two paths is caught HERE, in
    seconds, instead of in a divergent artifact.

Checks (exit 1 on any drift):
  1. every ``FROM golang:<tag>`` in tests/cloud-lab/Dockerfile* uses the
     manifest's exact version (directly or via the ``GO_VERSION`` ARG
     default — ARG defaults are allowed but must stay in lockstep);
  2. every literal ``go-version:`` in .github/workflows/*.yml and the
     .ngit/act/workflows twin equals the manifest (expression-derived pins
     are fine and skipped);
  3. the Docker build of the service binary carries the canonical flags
     (``-trimpath -buildvcs=false -ldflags=``) so path-parity hashing is
     meaningful;
  4. every ``go.mod`` in the tree declares a ``go`` directive at or below
     the manifest version. The directive is a toolchain FLOOR, but a floor
     above the pinned toolchain is a build that cannot run: the SDK lane
     compiles every module with the pinned Go, so a module that demands a
     newer language silently excludes itself from the shipped artifact
     (and from the in-SDK builds the manifest's go_per_release table
     documents). The ceiling, like the pins, is the manifest's call;
  5. every workflow job that runs IN a golang image (``container:
     golang:<tag>`` or its ``image:`` block form) pins the manifest's
     exact version, optionally with a distro suffix (``1.26.8-bookworm``)
     — a container pin is as much a build path as a setup-go pin.

Usage:
  python3 tests/contract/check-toolchain-parity.py [--root DIR]

Exit codes: 0 = every build path agrees; 1 = drift, itemized.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parent.parent.parent
MANIFEST_REL = "packaging/build-inputs.json"

GOLANG_FROM = re.compile(r"^FROM\s+golang:([^\s]+)", re.MULTILINE)
GO_VERSION_ARG = re.compile(r"^ARG\s+GO_VERSION=(\S+)", re.MULTILINE)
GO_BUILD_LINE = re.compile(r"^RUN\s+.*go build\b.*$", re.MULTILINE)
WORKFLOW_PIN = re.compile(r"go-version:\s*['\"]?(\d+\.\d+(?:\.\d+)?)['\"]?\s*$", re.MULTILINE)
EXPRESSION_PIN = re.compile(r"go-version:\s*\$\{")
# A job-level container pin, inline (`container: golang:1.26.8-bookworm`)
# and block form (`container:\n  image: golang:1.26.8-bookworm`).
CONTAINER_INLINE = re.compile(r"^\s*container:\s*golang:([^\s]+)\s*$", re.MULTILINE)
CONTAINER_IMAGE = re.compile(r"^\s*image:\s*golang:([^\s]+)\s*$", re.MULTILINE)
GO_DIRECTIVE = re.compile(r"^go\s+(\d+)(?:\.(\d+))?(?:\.(\d+))?\s*$", re.MULTILINE)

failures: list[str] = []
notes: list[str] = []


def manifest_go(root: Path) -> str:
    return json.loads((root / MANIFEST_REL).read_text())["go"]["version"]


def version_tuple(version: str) -> tuple[int, int, int]:
    parts = version.split(".")
    while len(parts) < 3:
        parts.append("0")
    return (int(parts[0]), int(parts[1]), int(parts[2]))


def check_dockerfiles(root: Path, truth: str) -> None:
    for df in sorted((root / "tests" / "cloud-lab").glob("Dockerfile*")):
        text = df.read_text()
        rel = df.relative_to(root).as_posix()
        for tag in GOLANG_FROM.findall(text):
            if tag.startswith("${GO_VERSION"):
                continue  # parameterized — the ARG default is checked below
            if tag != truth and not tag.startswith(f"{truth}-"):
                failures.append(f"{rel}: FROM golang:{tag} != manifest {truth}")
        for default in GO_VERSION_ARG.findall(text):
            if default != truth:
                failures.append(
                    f"{rel}: ARG GO_VERSION={default} (the fallback pin) "
                    f"!= manifest {truth} — keep the default in lockstep"
                )
        # Canonical build flags: every `go build` of Go source in the
        # service image must be reproducibility-compatible with the SDK
        # path (scripts/build-sdk-package.sh uses -trimpath
        # -buildvcs=false -ldflags=...).
        for line in GO_BUILD_LINE.findall(text):
            if "-trimpath" not in line or "-buildvcs=false" not in line:
                failures.append(
                    f"{rel}: `go build` without canonical flags — a Docker "
                    "build cannot hash-match the SDK path without "
                    "-trimpath -buildvcs=false: " + line.strip()[:90]
                )


def check_workflow_container_pins(root: Path, wf: Path, truth: str) -> None:
    text = wf.read_text()
    rel = wf.relative_to(root).as_posix()
    for pattern in (CONTAINER_INLINE, CONTAINER_IMAGE):
        for tag in pattern.findall(text):
            if tag != truth and not tag.startswith(f"{truth}-"):
                failures.append(
                    f"{rel}: container golang:{tag} != manifest {truth} "
                    "(distro suffixes are allowed: golang:<version>-<distro>)"
                )


def check_workflows(root: Path, truth: str) -> None:
    workflows: list[Path] = []
    for directory in (root / ".github" / "workflows", root / ".ngit" / "act" / "workflows"):
        workflows.extend(sorted(directory.glob("*.yml")))
    for wf in workflows:
        text = wf.read_text()
        rel = wf.relative_to(root).as_posix()
        for m in WORKFLOW_PIN.finditer(text):
            line_start = text.rfind("\n", 0, m.start()) + 1
            if EXPRESSION_PIN.search(text[line_start : m.end()]):
                continue  # derived from the manifest at runtime — correct by construction
            pinned = m.group(1)
            if pinned != truth and not truth.startswith(pinned + ".") and pinned != truth.rsplit(".", 1)[0]:
                failures.append(f"{rel}: go-version: {pinned} != manifest {truth}")
        check_workflow_container_pins(root, wf, truth)


def check_go_mods(root: Path, truth: str) -> None:
    ceiling = version_tuple(truth)
    seen = 0
    for mod in sorted(root.rglob("go.mod")):
        if ".git" in mod.parts:
            continue
        seen += 1
        text = mod.read_text(encoding="utf-8", errors="replace")
        rel = mod.relative_to(root).as_posix()
        m = GO_DIRECTIVE.search(text)
        if not m:
            failures.append(f"{rel}: no 'go' directive — every module must declare its language floor")
            continue
        directive = version_tuple(".".join(g or "0" for g in m.groups()))
        if directive > ceiling:
            failures.append(
                f"{rel}: go {'.'.join(str(p) for p in directive)} > manifest {truth} — a "
                "language floor above the pinned toolchain cannot compile in the SDK "
                "lane; raise the manifest (a release decision) or lower the directive"
            )
    if seen == 0:
        failures.append("no go.mod found under the root — this check is observing nothing")
    else:
        notes.append(f"go.mod 'go' directives: {seen} module(s), ceiling {truth}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Toolchain parity across every build path.")
    parser.add_argument("--root", default=None, help="repository root (default: this checkout)")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve() if args.root else DEFAULT_ROOT

    truth = manifest_go(root)
    notes.append(f"manifest toolchain ({MANIFEST_REL}): go {truth}")
    check_dockerfiles(root, truth)
    check_workflows(root, truth)
    check_go_mods(root, truth)
    for n in notes:
        print(f"  {n}")
    if failures:
        print("\n❌ TOOLCHAIN DRIFT — the shortcut paths disagree with the manifest:")
        for f in failures:
            print(f"  🔴 {f}")
        print(
            "\nFix: derive from the manifest (jq -r '.go.version' packaging/build-inputs.json) "
            "or align the literal. The manifest is the single source of truth; bumping it is an "
            "intentional release decision (see docs/reproducible-builds.md)."
        )
        return 1
    print("✅ All build paths agree on the toolchain and canonical flags.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
