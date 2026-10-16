- **Add `research/seal-vetting/`, the reproducible evaluation code behind the
  seal analysis posted on PR #944.** The seal is a transferable off-chain
  ownership record anchored to a bearer note, proposed as a fit for TollGate. The
  written verdict is on the PR; this directory is the code behind it, so the
  claims can be re-run rather than taken on trust.

  `run-seal-vetting.sh` clones `lnurlcash/lnurl-wallet` at a **pinned revision**,
  installs its dependencies, installs the adversarial test into the project's own
  test tree, and runs it with the project's own `vitest`. Running it against
  upstream's real modules matters: reimplementing the validation function in a
  scratch file would only show that *our copy* fails, which proves nothing.

  The three tests record what `sealChainProblem` does not check. It consumes no
  signature, key or certificate, so an entirely fabricated history validates —
  demonstrated at 5 states and again at 500 to show depth is no constraint. And
  because the chain is append-only and validation never asks whether a state is
  the *current* one, every **prefix** of a consignment is also valid, with a
  distinct terminal hash: 6 states yield 6 distinct valid terminal hashes. That
  last result is why the replay-keying choice is load-bearing and why it needs to
  be keyed on genesis/`assetId` rather than the terminal state hash.

  Also records the root cause, which is one field wide: the mint certificate is
  already flowing through the code and being dropped —
  `rotateNoteWithHash` returns `{signature}`, `seal.transition` receives it and
  discards it, and `encodeSealConsignment` reads two of the four fields its own
  type declares, while `verifyNoteSignatureForKey` already exists and is already
  used for `Q` in `note.lockToPubkey`.

  Includes `PRELIMINARY-READING.md`, kept deliberately with a **partially wrong
  hypothesis** so the correction is auditable: we first suspected the
  redemption-time mint binding was absent, and it is present and correct — it
  simply constrains the wrong direction.
