#!/usr/bin/env python3
"""check-cdkgo-pin.py — the cdk-go pin is ONE version, everywhere.

The cdk-go module (upstream github.com/cashubtc/cdk-go) is what the
cdk_wallet build tag compiles against, and it drifted on main exactly the
way the gonuts pin did before #791's fence: while #1190 converged every
carrier on v0.18.3, main itself split THREE carriers at v0.17.3 and one at
v0.18.1 — two spellings of "current cdk-go" coexisting, the same near-miss
on shadowing the gonuts fence was built to end.

The rule this checker enforces is deliberately dumb:

  **every `go.mod` in the tree that references
  `github.com/cashubtc/cdk-go` references THE version named in
  packaging/build-inputs.json (.cdk_go.version), byte-identically.**

Carriers are DISCOVERED by glob (repo-root ``rglob("go.mod")``), never by a
hand-maintained list. A module that does not reference cdk-go is not a
carrier and is not judged.

Two legs, on purpose (same policy as the gonuts fence — the full rationale
lives in check-gonuts-pin.py's docstring):

  * offline (default): identity only — manifest vs every carrier, plus a
    go.sum presence check per carrier;
  * ``--latest``: the manifest version must be a REAL tag of
    cashubtc/cdk-go and its NEWEST stable tag (release-time verdict, not
    per-commit).

Additionally — and unlike the gonuts fence — any `replace` directive on the
cdk-go module path is REFUSED outright. The Amperstrand fork replace
(tracked for retirement by #1149) makes the require-line pin a lie: the
fork is what actually builds. A pin fence that tolerates a replace is
decoration, so it doesn't.

Fix for any refusal: `scripts/bump-cdkgo.sh <version>` rewrites the
manifest and every carrier together — a half-update cannot happen.

The rule itself lives ONCE in tests/contract/pin_fence.py, shared with
check-gonuts-pin.py. This wrapper declares only the cdk-go facts.

Usage:
  python3 tests/contract/check-cdkgo-pin.py [--root DIR] [--latest]

Exit codes: 0 = one pin, everywhere; 1 = drift, itemized.
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pin_fence

FENCE = pin_fence.PinFence(
    manifest_key="cdk_go",
    module="github.com/cashubtc/cdk-go",
    repo="cashubtc/cdk-go",  # verified against .cdk_go.repo
    fix_script="scripts/bump-cdkgo.sh",
    drift_title="CDK-GO PIN DRIFT",
    add_hint="Add the cdk_go block (see #1190)",
    carrierless_note=(
        "the CDK backend is load-bearing for the cdk_wallet build tag"
    ),
    history_note=(
        "(#791's class, caught on main as the v0.17.3/v0.18.1 split that "
        "#1190 converged on v0.18.3)"
    ),
    stale_note=(
        "(then read its changelog and re-run the cdk-walletd parity lane — "
        "upstream CDK releases carry wallet-saga semantics the lane replays)"
    ),
    refuse_replaces=True,
    replace_note=(
        "Retire the fork replace (#1149) and pin upstream via "
        "scripts/bump-cdkgo.sh <tag>."
    ),
)


def manifest(root: Path) -> tuple[str, str]:
    return pin_fence.manifest(root, FENCE)


def carriers(root: Path) -> list[Path]:
    """Every go.mod in the tree that references cdk-go — by glob, not list."""
    return pin_fence.carriers(root, FENCE)


def main(argv: list[str] | None = None) -> int:
    return pin_fence.run(FENCE, argv)


if __name__ == "__main__":
    sys.exit(main())
