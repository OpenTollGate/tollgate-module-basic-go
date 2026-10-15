- **Add `src/taprootverify`, a bounded instrument-agnostic taproot commitment
  verifier, with a test suite validated against Bitcoin Core.** TollGate accepts
  one instrument family today, but every candidate alternative — an LNURLcash
  bearer note, a one-way-channel leaf, a Hedgehog-style revocable script — is
  ultimately a BIP-341 taproot output key plus a script path, and the router had
  no way to reason about one. This package verifies that an internal key, leaf
  script and control block reproduce an output key, and derives output keys from
  a script tree. It does **not** accept payments, keep a ledger, touch the wallet,
  or make a grant decision: verifying a commitment is necessary but not
  sufficient to accept value, and `AGENTS.md` forbids granting on a local check
  alone. Nothing in it knows what Cashu, LNURLcash or a channel leaf is, which is
  what makes it reusable by the CDK migration rather than a bespoke path for one
  prototype.

- **No new dependency and no new cgo.** `btcsuite/btcd` and `btcec/v2` were
  already present as `// indirect` requirements via the gonuts Lightning stack,
  and `txscript` already contains the full taproot machinery
  (`ComputeTaprootOutputKey`, `AssembleTaprootScriptTree`, `BaseLeafVersion`).
  This change promotes those three requires to direct — 4 insertions, 5
  deletions in `go.mod`, no module added — and is pure Go, so `CGO_ENABLED=0`
  multi-arch builds are unaffected.

- **Core-derived fixtures caught a real defect.** Every expected output key in
  the test suite was produced by Bitcoin Core v31.1.0, not by this package. The
  1- and 2-leaf cases agreed immediately; the **3-leaf case disagreed**, because
  a flat `MerkleRoot(scripts ...[]byte)` silently assumed a balanced tree and
  computed `br(br(hA,hB),br(hC,hC))` where Core's nested descriptor
  `tr(K,{pk(A),{pk(B),pk(C)}})` requires `br(hA,br(hB,hC))`. Same leaves,
  different shape, different key — and valid-looking either way. The defect was
  the API, not the arithmetic: a flat list cannot express the shape. Fixed with
  an explicit nested-tree API (`TreeRoot`/`BranchOf`/`Leaf`) plus a regression
  test that computes both the correct and the incorrect root and fails if they
  ever converge. RED and GREEN captures are in
  `research/router-taproot-verify/out/`.

- **Documented, in the test suite, the control-block trap that cost hours in
  sibling work.** A single-leaf taproot tree has no merkle sibling, so its
  control block is exactly 33 bytes — `(leaf_version|parity) || internal_key`.
  Appending the leaf's own hash (65 bytes) reconstructs a *different* output key
  and Core reports it as the misleading `Witness program hash mismatch`.

- **Not yet done:** cross-compiling for the router's aarch64 target, and wiring
  the verifier into a spend path. The `WalletPort` seam remains dead code
  (`src/merchant/merchant.go` calls `NewWalletPort` directly), so no behaviour on
  a real router changes yet.
