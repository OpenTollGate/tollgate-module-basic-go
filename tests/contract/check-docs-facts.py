#!/usr/bin/env python3
"""Doc-fact contract: documentation claims that state facts about the code
must agree with the code.

The 0.6.0 cycle shipped three regressions of this class (docs describing a
decode path the code no longer had; a tester guide stamped to a dead version
era; a code sample superseded by a generalized implementation), each caught
by hand. This check mechanizes the bindings:

  1. gonuts version: the compatibility matrix's re-verification note must
     name the version the module actually pins (src/tollwallet/go.mod).
  2. module count: every "N modules" / "N nested go.mod files" claim in the
     agent/contributing docs must equal the actual count under src/.
  3. schema version: README's "current schema version" must equal the
     config schema's default.
  4. notice codes: the Payment-outcomes table in docs/merchant.md and the
     code strings merchant.go emits must match bidirectionally — every
     documented code exists in code, every emitted code is documented.

Usage: python3 tests/contract/check-docs-facts.py   (from the repo root)
Exit 0 = facts agree. Non-zero names every disagreement.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent


def fail(msgs: list[str], msg: str) -> None:
    msgs.append(msg)


def main() -> int:
    msgs: list[str] = []

    # 1. gonuts version pin vs the compat matrix's re-verification note
    go_mod = (ROOT / "src" / "tollwallet" / "go.mod").read_text()
    m = re.search(r"github\.com/OpenTollGate/gonuts-tollgate (v[0-9.]+)", go_mod)
    if not m:
        fail(msgs, "src/tollwallet/go.mod: cannot find the gonuts-tollgate require line")
    else:
        pinned = m.group(1)
        matrix = (ROOT / "docs" / "cashu-compatibility-matrix.md").read_text()
        note = re.search(
            r"Re-verified against `gonuts-tollgate` (v[0-9.]+)", matrix
        )
        if not note:
            fail(
                msgs,
                "docs/cashu-compatibility-matrix.md: no 'Re-verified against "
                "`gonuts-tollgate` vX' note — add (or refresh) it after every "
                "fork bump",
            )
        elif note.group(1) != pinned:
            fail(
                msgs,
                f"compat matrix re-verified against {note.group(1)} but go.mod "
                f"pins {pinned} — re-verify the matrix rows or fix the note",
            )

    # 2. module-count claims vs the actual count under src/
    actual = sum(1 for p in (ROOT / "src").rglob("go.mod"))
    claim_files = [
        ROOT / "AGENTS.md",
        ROOT / "CONTRIBUTING.md",
        ROOT / "scripts" / "go-battery.sh",
        ROOT / "scripts" / "release-check.sh",
    ]
    pattern = re.compile(r"(\d+)(?: nested)? (?:go\.mod files|modules)", re.IGNORECASE)
    claims = 0
    for cf in claim_files:
        if not cf.exists():
            continue
        for mm in pattern.finditer(cf.read_text()):
            claims += 1
            if int(mm.group(1)) != actual:
                fail(
                    msgs,
                    f"{cf.relative_to(ROOT)}: claims {mm.group(0)} but src/ has "
                    f"{actual} go.mod files — update the claim (this check "
                    "exists so the number can never drift silently)",
                )
    if claims == 0:
        fail(msgs, "no module-count claims found where the check expects them")

    # 3. schema version: README vs the config schema default
    schema = (ROOT / "src" / "config_manager" / "config_schema.go").read_text()
    sm = re.search(r'Description: "Configuration file version", Default: "(v[0-9.]+)"', schema)
    if not sm:
        fail(msgs, "config_schema.go: cannot read the config-version default")
    else:
        readme = (ROOT / "README.md").read_text()
        rm = re.search(r"current schema version is \*\*`(v[0-9.]+)`\*\*", readme)
        if not rm:
            fail(msgs, "README.md: 'current schema version' sentence not found")
        elif rm.group(1) != sm.group(1):
            fail(
                msgs,
                f"README says schema {rm.group(1)} but the schema default is "
                f"{sm.group(1)}",
            )

    # 4. notice codes: docs/merchant.md table vs merchant.go (bidirectional)
    merchant_src = (ROOT / "src" / "merchant" / "merchant.go").read_text()
    emitted = set(re.findall(r'"((?:payment|mint-rate-limited|client-not-registered|session-error|invalid-mac-address)[a-z-]*)"', merchant_src))
    # A bare "payment" is not a notice code (it is a log prefix / event kind)
    emitted.discard("payment")
    doc = (ROOT / "docs" / "merchant.md").read_text()
    documented: set[str] = set()
    table_found = False
    if "## Payment outcomes" in doc:
        section = doc.split("## Payment outcomes", 1)[1]
        section = section.split("\n## ", 1)[0]
        rows = re.findall(r"^\|.*\|$", section, re.M)
        for code in re.findall(r"`([a-z][a-z-]+)`", "\n".join(rows)):
            if code.startswith(("payment-", "mint-", "client-", "session-", "invalid-")):
                documented.add(code)
        table_found = bool(rows)
    if not table_found:
        fail(msgs, "docs/merchant.md: the Payment outcomes table was not found")
    else:
        missing_doc = sorted(emitted - documented)
        missing_code = sorted(documented - emitted)
        if missing_doc:
            fail(
                msgs,
                "docs/merchant.md Payment outcomes table is missing codes that "
                f"merchant.go emits: {', '.join(missing_doc)}",
            )
        if missing_code:
            fail(
                msgs,
                "docs/merchant.md documents codes merchant.go never emits "
                f"(stale table): {', '.join(missing_code)}",
            )

    if msgs:
        print("doc-facts: FAIL")
        for msg in msgs:
            print(f"  - {msg}")
        return 1
    print("doc-facts: PASS (gonuts pin, module count, schema version, notice codes all agree)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
