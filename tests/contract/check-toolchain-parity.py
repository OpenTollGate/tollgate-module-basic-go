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
  2. every literal ``go-version:`` in .github/workflows/*.yml equals the
     manifest (expression-derived pins are fine and skipped);
  3. the Docker build of the service binary carries the canonical flags
     (``-trimpath -buildvcs=false -ldflags=``) so path-parity hashing is
     meaningful.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
MANIFEST = REPO / "packaging" / "build-inputs.json"
DOCKERFILES = sorted((REPO / "tests" / "cloud-lab").glob("Dockerfile*"))
WORKFLOWS = sorted((REPO / ".github" / "workflows").glob("*.yml"))

GOLANG_FROM = re.compile(r"^FROM\s+golang:([^\s]+)", re.MULTILINE)
GO_VERSION_ARG = re.compile(r"^ARG\s+GO_VERSION=(\S+)", re.MULTILINE)
GO_BUILD_LINE = re.compile(r"^RUN\s+.*go build\b.*$", re.MULTILINE)
WORKFLOW_PIN = re.compile(r"go-version:\s*['\"]?(\d+\.\d+(?:\.\d+)?)['\"]?\s*$", re.MULTILINE)
EXPRESSION_PIN = re.compile(r"go-version:\s*\$\{")

failures: list[str] = []
notes: list[str] = []


def manifest_go() -> str:
    return json.loads(MANIFEST.read_text())["go"]["version"]


def check_dockerfiles(truth: str) -> None:
    for df in DOCKERFILES:
        text = df.read_text()
        rel = df.relative_to(REPO).as_posix()
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


def check_workflows(truth: str) -> None:
    for wf in WORKFLOWS:
        text = wf.read_text()
        rel = wf.relative_to(REPO).as_posix()
        for m in WORKFLOW_PIN.finditer(text):
            line_start = text.rfind("\n", 0, m.start()) + 1
            if EXPRESSION_PIN.search(text[line_start : m.end()]):
                continue  # derived from the manifest at runtime — correct by construction
            pinned = m.group(1)
            if pinned != truth and not truth.startswith(pinned + ".") and pinned != truth.rsplit(".", 1)[0]:
                failures.append(f"{rel}: go-version: {pinned} != manifest {truth}")


def main() -> int:
    truth = manifest_go()
    notes.append(f"manifest toolchain (packaging/build-inputs.json): go {truth}")
    check_dockerfiles(truth)
    check_workflows(truth)
    for n in notes:
        print(f"  {n}")
    if failures:
        print(f"\n❌ TOOLCHAIN DRIFT — the shortcut paths disagree with the manifest:")
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
