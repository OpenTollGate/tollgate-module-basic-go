# Wallet parity lane — the gate before gonuts demotion

Phase 2 of the gonuts→CDK wallet migration (campaign skill:
`tollgate-wallet-migration`; case table:
`references/parity-suite-cases.md`). Runs the fund-safety acceptance
cases against cdk-walletd, driven through the module's `WalletPort`
client (`tollwallet.NewSidecarWallet` via `client/cdkinterop`), with
deterministic crash placement via `killproxy.py`.

## Run

```
./run-parity.sh              # suite
./run-parity.sh --mutation   # + mutation check (suite must detect a
                             #   gate-bypassed daemon — RED is PASS)
```

Host-based by design: cdk-mintd 0.18.2 (docker, host netns, zero-fee
fakewallet, fresh per run) behind killproxy on 127.0.0.1:3340 (the
advertised mint URL = the wallet identity), cdk-walletd on-host, the
WalletPort client built from THIS worktree.

## Cases

| # | Case | Boundary / class |
|---|---|---|
| 00 | handshake | info manifest, 0600 socket |
| 10 | happy round-trip | quote→mint→send(swap)→check_state→receive→check_state |
| 20 | kill melt @ acceptance | mint accepted, response swallowed, SIGKILL; value preserved pre-recovery (reserved or unreserved shape — both measured), melt saga resolved after recovery (compensated: fakewallet cannot pay its own invoice; two-sided saga coherence) |
| 30 | kill receive @ swap acceptance | credited EXACTLY once after recovery; token provably spent via WalletPort |
| 40 | duplicate outputs (same seed) | #257/#266/#480 class: unpatched cdk-mintd maps the collision to InvoiceAlreadyPaid (not NUT-19 11003) and the wallet bricks — pinned as a flip detector for Amperstrand/cdk fix/duplicate-outputs-error-identity (fixed mint: wallet walks past the burn) |
| 50 | fail-closed recovery gate | mint down at restart with a PENDING saga → money ops refused; mutation check bypasses the gate and the suite must see the difference |

## Mutation check

`--mutation` builds a daemon with every `ensure_recovered` gate call
rewritten to a plain wallet open (source patched, built, restored in
the daemon repo), then runs case 50 expecting the fail-closed refusal
to be ABSENT. A suite that stays green under the mutation is
decoration — this lane proves it is not.

## Recorded follow-ups

- Structural NUT-19 error codes through the sidecar protocol (the
  daemon currently surfaces error STRINGS; case 40 records the verbatim
  identity; codes-for-machines is a daemon protocol extension).
- The HTLC signature-enforcement case (fork fix 296c7bf) and the
  gonuts-vs-cdk comparison matrix on the same mint zoo.
- PRTA integration: the bench/QEMU legs with the daemon packaged.
