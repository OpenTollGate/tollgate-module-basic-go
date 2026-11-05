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

The rule itself lives ONCE in tests/contract/pin_fence.py, shared with
check-cdkgo-pin.py (same doctrine, fenced module). This wrapper declares
only the gonuts facts.

Usage:
  python3 tests/contract/check-gonuts-pin.py [--root DIR] [--latest]

Exit codes: 0 = one pin, everywhere; 1 = drift, itemized.
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pin_fence

FENCE = pin_fence.PinFence(
    manifest_key="gonuts",
    module="github.com/OpenTollGate/gonuts-tollgate",
    repo="OpenTollGate/gonuts-tollgate",  # verified against .gonuts.repo
    fix_script="scripts/bump-gonuts.sh",
    drift_title="GONUTS PIN DRIFT",
    add_hint="Add the gonuts block (see #791)",
    carrierless_note=(
        "a wallet-less tree is not a state this repo has ever been in"
    ),
    history_note="(#791)",
    stale_note=(
        "(then read its changelog; this is how the #705 fix in v0.13.2 "
        "nearly missed a release)"
    ),
)


def manifest(root: Path) -> tuple[str, str]:
    return pin_fence.manifest(root, FENCE)


def carriers(root: Path) -> list[Path]:
    """Every go.mod in the tree that references the fork — by glob, not list."""
    return pin_fence.carriers(root, FENCE)


def main(argv: list[str] | None = None) -> int:
    return pin_fence.run(FENCE, argv)


if __name__ == "__main__":
    sys.exit(main())
