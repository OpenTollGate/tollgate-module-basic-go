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
| 60 | locked token refused (demotion gate) | fork fix #324/#325 through WalletPort: P2PK-locked-to-unknown-key token must be refused with zero credit (cdk enforces mint-side: "Witness signatures not provided"; gonuts enforces locally via ErrLockedToken). The swap-proof-loss half of the demotion gate maps to cases 20/30's conservation invariants. |

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


## Backend comparison (measured 2026-10-30)

`PARITY_DAEMON=<binary> ./run-parity.sh` runs the lane against any
daemon speaking the sidecar protocol. First full comparison run:
cdk-walletd (sagas) vs micronuts-walletd (no saga, random secrets,
journal-based pending sends) — the crash-boundary experiment the lane
was built for:

| Case | cdk-walletd | micronuts-walletd | reading |
|---|---|---|---|
| 00 handshake | PASS | PASS (after arches + backend-name fix) | protocol parity reachable |
| 10 round-trip | PASS | PASS | full money cycle works on both |
| 20 melt-kill | PASS (700+300 conserved) | PASS (2026-11-12, crash journal + NUT-09 fix #165: the journal shape conserves 1000/0 — the mint's rollback returns inputs unspent and restore re-fetches; was balance=0 LOST before the journal) | two recovery models, both conserve: saga reservation vs journal+restore |
| 30 receive-kill | PASS (credited once) | PASS (2026-11-12, same journal: the reserved range is restored on boot via the fail-closed gate; was 600+0 LOST) | the journal closes the receive crash window |
| 40 duplicate-outputs | brick pinned (flip detector) | mint SUCCEEDS on the "burned range" — random (non-seed-derived) secrets mean no burned ranges exist | immunity by design, different mechanism |
| 50 fail-closed gate | PASS | PASS | the gate contract is daemon-side and portable |
| 60 locked token | PASS | PASS (after send_p2pk landed, 2026-10-31 — the lane caught a FAKE-LOCK bug on its first run: a malformed NUT-10 secret template made the "locked" token anyone-can-spend and the victim credited it, the #324 attack live; fixed + unit-pinned, Amperstrand/micronuts 2ef6a5c) | |

Verdict (updated 2026-11-12, after the crash journal + the #165 NUT-09
fix): the no-saga design NO LONGER loses value at the crash boundaries
— cases 20 and 30 both pass via journal + NUT-09 restore, the same
conservation the sagas achieve by a different mechanism. cdk-walletd
stays the shipping default (its sagas also cover quote/melt-state
transitions the journal does not yet model); micronuts's wins stand:
no duplicate-outputs class in normal operation, 3-8 MB footprint, and
now crash-boundary conservation. Case 40's "flip" was
adjudicated (2026-11-13) to a harness defect, not a behavior change:
the same-seed scenario never ran for micronuts (its daemon has no
--mnemonic-file; the flag was silently ignored), and with seed
injection fixed the case passes for real — FINAL BOARD: micronuts +
crash journal + NUT-09 restore passes 7/7.
