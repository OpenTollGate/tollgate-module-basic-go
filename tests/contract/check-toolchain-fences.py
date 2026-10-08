#!/usr/bin/env python3
"""check-toolchain-fences.py — the Go versioning fences, as a contract.

The two-era SDK doctrine (packaging/build-inputs.json) pins one host Go
toolchain (go.version) that compiles every shipped binary for BOTH eras;
the SDK images only package. Dependabot and `go get` cannot see that
layer — they only read go.mod — so this check keeps the boundary they CAN
touch from drifting:

  1. no `toolchain` directives, anywhere — toolchain selection is enforced
     at build time by providing the pinned toolchain (TG_TOOLS; the Go
     team's own guidance for hermetic builds), not by directives that
     `go get` silently writes and GOTOOLCHAIN=auto then follows;
  2. every go.mod declares the SAME `go` directive — 17 modules with one
     language floor, so no module can drift ahead of the sweep discipline;
  3. that directive is <= the manifest's pinned go.version — a dependency
     bump whose fix requires a newer language version must fail HERE, at
     review time, instead of inside a packaging lane three layers down;
  4. any go_per_release entry below the directive is reported as
     "in-SDK builds unavailable for that era" — informational, matching
     the manifest comment; it is a property of the era, not an error.

Run by scripts/check-version-sync.sh, so every place that already gates
versions (pre-commit, release lanes) gates the fences too.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
MANIFEST = ROOT / "packaging" / "build-inputs.json"
GO_DIRECTIVE = re.compile(r"^go\s+(\d+)\.(\d+)(?:\.(\d+))?\s*$", re.M)
TOOLCHAIN_DIRECTIVE = re.compile(r"^toolchain\s+\S+", re.M)


def go_mods():
    return sorted(
        p
        for p in ROOT.rglob("go.mod")
        if ".git" not in p.parts and "node_modules" not in p.parts
    )


def main() -> int:
    failures = []
    directives = {}
    for mod in go_mods():
        rel = mod.relative_to(ROOT)
        text = mod.read_text()
        if TOOLCHAIN_DIRECTIVE.search(text):
            failures.append(f"{rel}: carries a toolchain directive — toolchains are provided by the build (TG_TOOLS), never declared")
        match = GO_DIRECTIVE.search(text)
        if not match:
            failures.append(f"{rel}: no go directive found")
            continue
        directives[str(rel)] = match.group(0).split()[1]

    manifest = json.loads(MANIFEST.read_text())
    pinned = manifest["go"]["version"]
    pinned_parts = tuple(int(x) for x in pinned.split("."))
    per_release = manifest["openwrt_sdk"]["go_per_release"]

    unique = set(directives.values())
    if len(unique) > 1:
        failures.append(
            "go directives disagree across modules: "
            + ", ".join(f"{k}={v}" for k, v in sorted(directives.items()))
        )

    for rel, directive in sorted(directives.items()):
        parts = tuple(int(x) for x in directive.split(".")) + (0,) * (3 - directive.count(".") - 1)
        if parts > pinned_parts:
            failures.append(
                f"{rel}: go directive {directive} exceeds the pinned toolchain go{pinned} "
                "(packaging/build-inputs.json) — bump the pinned toolchain or the dependency"
            )

    for series, version in sorted(per_release.items()):
        if not re.match(r"^\d+\.\d+$", str(series)):
            continue
        version_parts = tuple(int(x) for x in str(version).split("."))
        floor = min(
            tuple(int(x) for x in d.split(".")) + (0,) * (3 - d.count(".") - 1)
            for d in directives.values()
        )
        if version_parts < floor:
            print(
                f"note: era {series} (go {version}) is below the language floor "
                f"({ '.'.join(str(x) for x in floor) }) — in-SDK Go builds unavailable for that era; "
                "host prebuild with the pinned toolchain covers it"
            )

    if failures:
        for failure in failures:
            print(f"FAIL: {failure}")
        return 1
    print(
        f"ok: {len(directives)} go.mod files — uniform go directive, no toolchain lines, "
        f"floor <= pinned go{pinned}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
