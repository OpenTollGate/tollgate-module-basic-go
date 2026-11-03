#!/usr/bin/env python3
"""Contract: FROM-line ARGs in cloud-lab Dockerfiles must be globally declared.

Buildkit only interpolates an ARG into a ``FROM`` line if the ARG is
declared **before the first FROM** (global scope). An ARG declared after a
FROM is stage-scoped — invisible to every later ``FROM``, which then
interpolates an empty string. On docker/buildkit 29 the build dies at parse
time: ``failed to parse stage name "golang:-bookworm": invalid reference
format``. That is exactly issue #724: ``Dockerfile.client`` declared
``ARG GO_VERSION`` inside the rust-builder stage, so the go-builder
stage's ``FROM golang:${GO_VERSION}-bookworm`` resolved to
``golang:-bookworm`` and the client (and killer) images were unbuildable.

This check catches that bug class statically, in milliseconds, without
docker: for every ``Dockerfile*`` under tests/cloud-lab, each ``${VAR}``
or ``$VAR`` referenced in a ``FROM`` line must be declared by an ``ARG``
before the first ``FROM``, or be one of buildkit's automatic platform
args (TARGETPLATFORM & friends), which are legal in FROM without
declaration.

Stage-scoped bare re-declarations mid-file (``ARG NAME`` without a value
— the documented way to consume a global ARG inside a ``RUN``) remain
legal and are not flagged.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
DOCKERFILES = sorted((REPO / "tests" / "cloud-lab").glob("Dockerfile*"))

FROM_INSTR = re.compile(r"^\s*FROM\b")
ARG_INSTR = re.compile(r"^\s*ARG\s+(.+?)\s*(?:#.*)?$")
INTERP_BRACED = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")
INTERP_BARE = re.compile(r"(?<![\w$])\$([A-Za-z_][A-Za-z0-9_]*)")
# Automatic platform args are available in FROM without any declaration
# (docker docs: "Pre-defined build variables / automatic platform args").
PLATFORM_BUILTINS = {"TARGETPLATFORM", "TARGETOS", "TARGETARCH", "TARGETVARIANT"}

failures: list[str] = []


def arg_names(decl: str) -> list[str]:
    """Names in an ARG instruction — ``ARG A B=1`` yields [A, B]."""
    return [tok.split("=")[0] for tok in decl.split()]


def check(df: Path) -> None:
    text = df.read_text()
    rel = df.relative_to(REPO).as_posix()
    code_lines = [
        ln
        for ln in text.splitlines()
        if ln.strip() and not ln.lstrip().startswith("#")
    ]
    global_args: set[str] = set()
    seen_from = False
    for ln in code_lines:
        if not seen_from and (m := ARG_INSTR.match(ln)):
            global_args.update(arg_names(m.group(1)))
            continue
        if FROM_INSTR.match(ln):
            seen_from = True
            for var in INTERP_BRACED.findall(ln) + INTERP_BARE.findall(ln):
                if var in PLATFORM_BUILTINS or var in global_args:
                    continue
                failures.append(
                    f"{rel}: FROM interpolates ${{{var}}} but ARG {var} is "
                    "not declared before the first FROM — a stage-scoped ARG "
                    "is invisible to FROM and buildkit fails the parse "
                    f"(line: {ln.strip()[:70]})"
                )


def main() -> int:
    if not DOCKERFILES:
        print(
            "❌ FROM-ARG SCOPE DRIFT — discovery found no tests/cloud-lab/"
            f"Dockerfile* under {REPO}; refusing to pass vacuously. Run the "
            "check from inside the repo tree — it resolves its subject "
            "relative to its own file location."
        )
        return 1
    for df in DOCKERFILES:
        check(df)
    if failures:
        print("\n❌ FROM-ARG SCOPE DRIFT — a FROM line uses an ARG it cannot see:")
        for f in failures:
            print(f"  🔴 {f}")
        print(
            "\nFix: hoist the ARG declaration above the first FROM (see "
            "Dockerfile.tollgate's GO_VERSION). A global ARG is visible to "
            "every stage's FROM; re-declare it bare inside a stage only to "
            "use it in RUN steps."
        )
        return 1
    print(
        f"✅ Every FROM-line ARG in the {len(DOCKERFILES)} cloud-lab "
        "Dockerfiles is declared at global scope."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
