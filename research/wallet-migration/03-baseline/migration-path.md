# Migration path: router wallet gonuts → CDK, with rollback (T6)

**Status:** mechanism implemented and **rehearsed green** on the local lab
(2026-09-16). Raw: `experiments/migration/output.txt`. This document is the
operator procedure; the rehearsal is the executable proof.

## Preconditions

- The destination sidecar (CDK) is **supported on the target tier** — large/aarch64
  per `05-architecture/integration-decision.md` (the small/16 MB mipsel tier stays
  in-process gonuts; it is not a migration target).
- The mint is reachable from the router (see Q3 in `04-reports/FINDINGS-2026-09-16.md`;
  on-device mint or upstream required — this is the open blocker for an *on-router* run).

## Mechanism A — token transfer (chosen; proven)

Implementation-agnostic: it moves **bearer tokens**, so it does not depend on the
two libraries' seed derivation agreeing. Cross-implementation receives are proven
in T2e (`gonuts → CDK` and `CDK → gonuts` both work, V4 `cashuB`).

1. **Quiesce** the router: stop `tollgate-wrt` (no new payments) and wait for any
   Lightning quote monitor to finish.
2. **Back up** `/etc/tollgate/wallet.db` (copy, do not move) — a safety net, not
   the rollback mechanism (see below).
3. **Empty gonuts:** `drain` (or `send` the full balance) → one token per mint.
   Record each token **off-device** before proceeding.
4. **Fund CDK:** `receive` each token into the CDK wallet.
5. **Verify:** gonuts balance 0; CDK balance == the drained amount; spot-check
   with NUT-07 (`checkstate`) that the transferred proofs are now settled.
6. **Switch:** set `wallet.backend: cdk` + `wallet.socket`; start the daemon; start
   `tollgate-wrt`.

Rehearsal: `100 → migrate (gonuts 0, cdk 100) → rollback (cdk 0, gonuts 100)`,
`no_value_lost: true`.

## Mechanism B — same-seed NUT-09 restore (preferred *if* verified)

If both wallets derive the same NUT-13 outputs from the same BIP-39 seed, pointing
CDK at gonuts' mnemonic restores the proofs **without moving funds**, and the
switch becomes a pure config flip.

- gonuts stores its mnemonic in its bbolt `wallet.db` (`wallet.Wallet.Mnemonic()`).
- \(\textbf{Not exercised here}\): extracting it needs a dedicated export — the store
  is locked while the service runs, and `wallet.LoadWallet` blocks on mint
  registration; and CDK/gonuts NUT-13 derivation-path compatibility is unverified.
- To adopt B: (i) write a tiny `gonuts-seed` export tool, (ii) on a lab mint, mint
  a known amount, start CDK with that mnemonic and confirm it **restores the same
  balance**, (iii) only then use it in production (still keep Mechanism A as fallback).

## Rollback

Funds are **bearer tokens**; rollback is the reverse transfer, not a database
restore:

- **Before CDK spends anything** (e.g. immediately after a bad switch): drain CDK
  → tokens → `receive` into gonuts, set `wallet.backend: gonuts`, restart.
- **After CDK has spent**: those proofs are gone from gonuts' perspective; roll
  back the *backend* and accept the balance difference, or migrate back only the
  remaining balance. Restoring the `wallet.db` backup does **not** help — the
  original proofs were spent by the drain.

## Edge cases

- **In-flight Lightning quotes:** complete or expire them before switching; a quote
  belongs to the wallet that created it. Do not switch mid-quote.
- **P2PK / HTLC-locked proofs:** locked outputs need their witness to spend; if the
  locks are known, drain them with the witness first, or leave them out of the
  migration and reconcile after. (P2PK/HTLC parity itself is proven — T15.)
- **Multi-mint:** drain yields one token **per mint**; migrate each, then verify
  the destination has every mint configured (`accepted_mints`).
- **Untrusted/foreign mints:** a token from a mint the destination does not accept
  fails on `receive`; migrate only trusted-mint balances, or add the mint first.

## On-device procedure (once the mint is reachable)

```
# 1. stop the service; back up
/etc/init.d/tollgate-wrt stop
cp /etc/tollgate/wallet.db /etc/tollgate/wallet.db.bak-$(date +%s)
# 2. drain gonuts to tokens (via the service CLI / module), capture them
#    (tollgate wallet drain → file, or the interop tool with the same wallet dir)
# 3. start cdk-walletd (/etc/init.d/cdk-walletd start) and receive each token
# 4. verify balances + NUT-07
# 5. flip config: wallet.backend=cdk, wallet.socket=/var/run/tollgate/wallet.sock
/etc/init.d/tollgate-wrt start
```

## Risks / limitations

- Not yet rehearsed **on the router** (needs mint reachability) — the local lab
  rehearsal is the current evidence.
- Larger balances → larger tokens; the per-mint token is a single string (fine for
  router-sized balances).
- The drain window is a point of no return for the *source* wallet: capture tokens
  durably before receiving.
