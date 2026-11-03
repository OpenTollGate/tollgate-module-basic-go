#!/usr/bin/env python3
"""Contract: the build contract is regenerated, not hand-edited, and every
carrier ships the whole binary set.

The split-brain this exists for (twice in one week):
  * upstream: the CVM daemon was added to packaging/Makefile's PREBUILT_*
    set but not to scripts/build-sdk-package.sh — every apk build died at
    package/compile (#1020);
  * downstream: the FreedomTechFeed Gen-1 feed re-declares the same facts
    by hand and is three binary-sets behind (no CVM, wrong CLI version
    injection), while their Gen-2 fork fixed it — same defect, two
    lifetimes, because nothing shared the declaration.

The model (docs/reproducible-builds.md "build contract"):
  scripts/gen-build-contract.sh is the single WRITER of
  packaging/build-contract.json; packaging/Makefile's PREBUILT_* lines are
  the AUTHORITY for the binary set; carriers are CONSUMERS. Feeds consume
  the JSON straight out of the release tarball.

Checks (exit 1 on any failure):
  1. regeneration diff — running the generator must reproduce the
     committed contract byte-for-byte (a hand edit, a stale contract after
     a PREBUILT_*/go.mod/VERSION change, or a manifest bump without a
     regen all fail here);
  2. carrier coverage — every shell carrier that builds Go binaries must
     reference EVERY contract binary by name, or carry an explicit
     `# contract-exempt:` marker with a reason (discovery by content
     scan, not a hand list — a new builder is fenced the day it lands).

Mutation verdicts (both were real):
  * revert #1020's staging stanza -> build-sdk-package.sh loses
    tollgate-cvm -> carrier coverage fails;
  * bump a go.mod `go` directive without regenerating -> regeneration
    diff fails.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
CONTRACT = REPO / "packaging" / "build-contract.json"
GENERATOR = REPO / "scripts" / "gen-build-contract.sh"

# A "carrier" is any build script that compiles Go binaries for packaging.
# Discovered by content (never a filename list): scripts under packaging/
# or scripts/ that invoke a Go build.
CARRIER_GLOBS = ["packaging/*.sh", "scripts/*.sh"]
CARRIER_HINT = re.compile(r"\bgo\s+build\b|GO_BIN\b.*\bbuild\b|PREBUILT_")
EXEMPT_MARKER = re.compile(r"#\s*contract-exempt:\s*(.+)")

failures: list[str] = []
notes: list[str] = []


def fail(msg: str) -> None:
    failures.append(msg)


def contract_binaries() -> list[str]:
    import json

    doc = json.loads(CONTRACT.read_text())
    return sorted(b["name"] for b in doc["binaries"])


def check_regeneration() -> None:
    tmp = CONTRACT.with_suffix(".json.gen")
    proc = subprocess.run(
        ["sh", str(GENERATOR), "--out", str(tmp)],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        fail(f"generator exited {proc.returncode}: {proc.stderr.strip()}")
        return
    if tmp.read_bytes() != CONTRACT.read_bytes():
        fail(
            "packaging/build-contract.json is not what scripts/gen-build-contract.sh "
            "produces — regenerate (sh scripts/gen-build-contract.sh) and commit; "
            "hand edits are refused"
        )
    tmp.unlink(missing_ok=True)
    notes.append("contract regenerates byte-identical")


def check_carriers(bins: list[str]) -> None:
    carriers = []
    for pattern in CARRIER_GLOBS:
        for path in sorted(REPO.glob(pattern)):
            text = path.read_text(errors="replace")
            if CARRIER_HINT.search(text):
                carriers.append((path, text))

    for path, text in carriers:
        rel = path.relative_to(REPO).as_posix()
        m = EXEMPT_MARKER.search(text)
        if m:
            notes.append(f"{rel}: contract-exempt ({m.group(1).strip()})")
            continue
        missing = [b for b in bins if b not in text]
        if missing:
            fail(
                f"{rel} builds Go binaries but never mentions {missing} — a carrier "
                f"must build the whole contract set (packaging/Makefile PREBUILT_* is "
                f"the authority; add the build stanza or a reasoned '# contract-exempt:' line)"
            )
        else:
            notes.append(f"{rel}: covers {len(bins)}/{len(bins)} contract binaries")


def check_makefile_authority(bins: list[str]) -> None:
    text = (REPO / "packaging" / "Makefile").read_text()
    prebuilt = sorted(
        m.group(1)
        for m in re.finditer(r"^PREBUILT_[A-Z]+:=.*[/)](.+)$", text, re.MULTILINE)
    )
    if prebuilt != bins:
        fail(
            f"packaging/Makefile PREBUILT set {prebuilt} != contract binaries {bins} — "
            "the Makefile is the authority; regenerate the contract after changing it"
        )
    else:
        notes.append(f"PREBUILT authority matches the contract ({prebuilt})")


def check_arch_authority() -> None:
    """The contract's arch table must match scripts/repro-test.sh's arch case
    in BOTH directions: the case statement is the authority for which arches
    the release supports; the generator table adds the Go env + SDK target.
    Drift either way (an arch added to one and not the other) fails here —
    the fork's matrix and the fast lane's cross-compile consume the contract,
    so neither may re-list arches."""
    import json

    contract_arches = sorted(a["name"] for a in json.loads(CONTRACT.read_text())["arches"])
    repro = (REPO / "scripts" / "repro-test.sh").read_text()
    case_arches = sorted(
        m.group(1)
        for m in re.finditer(r"^\s{4}([a-z0-9_-]+)\)\s+GOARCH=", repro, re.MULTILINE)
    )
    if contract_arches != case_arches:
        only_contract = [a for a in contract_arches if a not in case_arches]
        only_case = [a for a in case_arches if a not in contract_arches]
        fail(
            f"contract arch table {contract_arches} != repro-test.sh arch case "
            f"{case_arches} (only-in-contract: {only_contract}, only-in-case: "
            f"{only_case}) — add the case arm and the generator row together, "
            "then regenerate"
        )
    else:
        notes.append(f"arch authority matches ({len(contract_arches)} arches)")


def main() -> int:
    if not CONTRACT.is_file():
        print("check-build-contract: FAIL — packaging/build-contract.json missing "
              "(run: sh scripts/gen-build-contract.sh)", file=sys.stderr)
        return 1
    if not GENERATOR.is_file():
        print("check-build-contract: FAIL — scripts/gen-build-contract.sh missing",
              file=sys.stderr)
        return 1

    bins = contract_binaries()
    check_regeneration()
    check_makefile_authority(bins)
    check_carriers(bins)
    check_arch_authority()

    print(f"  contract binaries: {' '.join(bins)}")
    for n in notes:
        print(f"  ok    {n}")
    if failures:
        for f in failures:
            print(f"  FAIL  {f}", file=sys.stderr)
        print(f"\n{len(failures)} contract check(s) failed.", file=sys.stderr)
        return 1
    print("✅ build contract regenerates clean and every carrier ships the full set.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
