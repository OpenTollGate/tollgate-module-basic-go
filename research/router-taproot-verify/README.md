# Router-side taproot commitment verifier

**Status:** implemented, tested, GREEN, validated against Bitcoin Core.
**Location:** `src/taprootverify/` (Go package, pure Go, no new dependency).
**Evidence:** `out/RED.txt`, `out/GREEN.txt` (47 assertions).

## What this is

A bounded, instrument-agnostic Go package that answers one question:

> Given a taproot output key, an internal key, a leaf script and a control
> block — does this leaf really commit to that output?

Plus the derivation side (what output key does this script tree produce?).

It deliberately does **not** accept payments, keep a ledger, touch the wallet, or
make any grant decision. Verifying a commitment is necessary but **not
sufficient** to accept value, and the repo's own doctrine (`AGENTS.md`) forbids
granting on a local check alone — consumption state is not established by a
commitment. This package stays on the safe side of that line.

## Why instrument-agnostic

Nothing in it knows what Cashu, LNURLcash, a one-way-channel leaf or a Hedgehog
revocable script is. It knows taproot. That makes it reusable by the CDK migration
the repo already wants, rather than a bespoke path for one prototype instrument —
which is what the T1 router-feasibility memo recommended ("ship it
instrument-agnostically").

## The two load-bearing findings it verifies

Both were claims in `docs/analysis/ROUTER-FEASIBILITY.md`, and both are now
checkable in code:

**1. No new dependency, no new cgo.** `btcsuite/btcd` and `btcec/v2` are already
in `src/go.mod` as **`// indirect`** (lines 48–52), pulled in by the gonuts
Lightning stack. The shipped binary even emits `txscript`'s error strings
(`ErrTaprootSigInvalid`, `ErrTaprootMerkleProofInvalid`, `ErrSchnorrHashValue`).
This package promotes an existing indirect requirement; it adds nothing, and it
is pure Go, so `CGO_ENABLED=0` multi-arch builds are unaffected.

**2. `txscript` already contains the full taproot machinery.** Confirmed by use,
not by reading docs: `ComputeTaprootOutputKey`, `ComputeTaprootKeyNoScript`,
`AssembleTaprootScriptTree`, `NewBaseTapLeaf(...).TapHash()`,
`BaseLeafVersion`, `TaggedHash(TagTapBranch, ...)`.

## The bug that Core's fixtures caught

The first implementation exposed only `MerkleRoot(scripts ...[]byte)` — a flat
list — which silently assumes a **balanced** tree.

- 1 leaf: agreed with Core ✅
- 2 leaves: agreed with Core ✅
- **3 leaves: DISAGREED with Core** ❌

A flat 3-leaf root promotes the odd leaf and computes
`br(br(hA,hB), br(hC,hC))`. Core's descriptor was `tr(K,{pk(A),{pk(B),pk(C)}})`,
a **nested** tree: `br(hA, br(hB,hC))`. Same leaves, different shape, different
root, different output key — and the result looks perfectly valid either way.

**The real defect was the API, not the arithmetic**: a flat list *cannot express*
the shape. Fixed by adding an explicit nested-tree API:

```go
root, _ := taprootverify.TreeRoot(
    taprootverify.BranchOf(
        taprootverify.Leaf(scriptA),
        taprootverify.BranchOf(taprootverify.Leaf(scriptB), taprootverify.Leaf(scriptC)),
    ),
)
```

`TestMerkleRootThreeLeafDivergence` guards this permanently: it computes both the
correct and the incorrect root and fails loudly if they ever converge, so nobody
can "simplify" `TreeRoot` back into the flat assumption.

This is exactly the class of error the Core cross-check exists to catch. Our own
arithmetic agreed with itself; only Core disagreed.

## Evidence standard

Every expected output key in the test suite was produced by **Bitcoin Core
v31.1.0** (`getdescriptorinfo` + `deriveaddresses` + `getaddressinfo` on
`tr(<NUMS>,<tree>)` descriptors), not by this package. The tests assert our Go
agrees with Core.

| Test | What it establishes |
|---|---|
| `TestOutputKeyMatchesCore` | derivation agrees with Core for 1-, 2- and 3-leaf trees |
| `TestIsTaproot` | witness-v1 recogniser, incl. rejecting v0/`OP_2`/wrong lengths |
| `TestVerifyCommitmentAcceptsRealSpend` | a genuine 33-byte control block verifies |
| `TestVerifyCommitmentRejectsWrongLeaf` | a mismatched leaf is rejected (`ErrCommitmentMismatch`) |
| `TestVerifyCommitmentRejectsMalformed` | error taxonomy: short block, bad leaf version, ragged path |
| `TestMerkleRootThreeLeafDivergence` | the nested-shape trap stays fixed |

## The control-block trap, recorded

A **single-leaf** tree has no merkle sibling, so its control block is exactly
**33 bytes**: `(leaf_version|parity) || internal_key`. Appending the leaf's own
hash (making 65) produces a valid-looking control block that reconstructs a
*different* output key, and Bitcoin Core reports it as the thoroughly misleading

```
mempool-script-verify-flag-failed (Witness program hash mismatch)
```

That cost several hours in the sibling LNURLcash POC work. It is documented in
the test comment so the next reader does not repeat it.

## What is NOT proven

- **Not run on router hardware.** The package compiles for the host and is pure
  Go, so the aarch64 `CGO_ENABLED=0` build should be unaffected — but "should be"
  is not "is". Cross-compiling for the router's target is the next step.
- **No end-to-end spend accepted by Core through this package.** The commitment
  check is validated against Core-derived keys, not by having Core execute a
  spend whose commitment this package verified. Wiring those together is a
  larger change.
- **No integration into the payment path.** The `WalletPort` seam remains dead
  code (`merchant.go:509` still calls `NewWalletPort` directly). Reviving that
  seam is a separate, larger change with its own fund-safety analysis.

## Reproduce

```bash
cd src
export PATH="$HOME/.local/toolchain/go/bin:$PATH"
GOTOOLCHAIN=local GOFLAGS=-mod=mod go test -v ./taprootverify/...
```

Note: the **root package** test (`go test .`) fails in this environment because
it needs to write `/etc/tollgate/`. That failure is pre-existing and independent
of this package — verified by stashing this package and re-running.
