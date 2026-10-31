# Module-level e2e conformance matrix — gonuts (default) vs cdk (sidecar)

Driver: `run-module-e2e.sh` (one driver, one leg logic, two backends;
`run-cdk-backend-e2e.sh` is the BACKEND=cdk shim). The gonuts lane's
mint traffic is routed through the in-network killproxy service
(passthrough except when LEG D arms it). Run:

    BACKEND=cdk    ./run-module-e2e.sh full
    BACKEND=gonuts ./run-module-e2e.sh full

All rows below are MEASURED (2026-11-01; gonuts column 3 consecutive
green runs, cdk column 3+ — see the git history for the run counts at
land time).

| Leg | cdk (wallet.backend=cdk, cdk-walletd sidecar) | gonuts (default, in-process) |
|-----|-----------------------------------------------|------------------------------|
| Happy path (3 pytest: advertisement, payment→1022, replay refused) | PASS | PASS |
| Backend selection evidence | log: `Wallet backend selected: cdk` | log: legacy `TollWallet.New` init |
| LEG C — quote survives the money-dependency restart | PASS — sidecar restarted; post-restart poll ISSUED, access granted (allotment 6M) | PASS — MINT restarted; post-restart poll ISSUED, access granted |
| LEG A — payment during dependency outage | refused, code `payment-processing-unavailable` ("note was not spent, try again"); module survived; recovered on return | module survived; recovered on return. The refusal itself is measured in LEG B (the pay helper funds its wallet FROM the mint, so a mint outage kills the helper before any POST — the leg is survival-only on this backend; noted in the driver) |
| LEG B — dependency killed mid-payment, then replay (killed at STAGING: the event file appearing means the POST is in flight) | single-grant value verdict ✓ (first attempt settled nowhere; replay the single grant, allotment bound respected) | single-grant value verdict ✓ — first attempt refused 21023 through the module (the non-vacuous mint-outage refusal), replay refused, no double grant |
| LEG D — deterministic ambiguity (the money dependency's answer to the receive/swap is processed then dropped) | sock proxy drops cdk-walletd's answer: `payment-outcome-unknown` (do-not-resubmit); deterministically-spent replay refused; recovery on the direct socket | killproxy (the parity lane's TCP injector, promoted here) drops the MINT's swap response: `payment-outcome-unknown` (do-not-resubmit); deterministically-spent replay refused; passthrough recovery |

## Known flake (gonuts LEG D, filed)

The gonuts LEG D swallow is ~3/4 stable: in ~1 of 4 runs the swallowed
payment still GRANTED (kind 1022) — a swap response reached the module
despite the armed proxy (the marker proves a swallow fired; which swap
it ate vs which one got through is the open question — candidate: the
helper's own funding send-swap races the arm, or gonuts' client retry
path lands a response between swallow cycles). The product's response
to a real swallow was correct in every observed instance; the flake is
a rig-timing question. Tracked in the follow-up issue; the leg is left
ON because a genuine regression in the ambiguity handling still fails
it in the other 3 runs.

## What the matrix pins

1. The SAME business-transaction invariants hold on both backends at the
   module level: no session without a confirmed receive, no double grant
   for one token's value, quote/payment recognition survives dependency
   restarts, and the module recovers from every outage tested.
2. The backend-difference the legs EXPOSE is the outage's CUSTOMER-FACING
   identity: cdk's decode is an RPC so a sidecar outage surfaces
   pre-receive as `payment-processing-unavailable` (safe-retry); gonuts'
   decode is local so a mint outage surfaces from Receive as a
   mint-unreachable refusal. Both are correct for their layer; neither
   may masquerade as a token-validity verdict (the d4ff8e6 fix).
3. LEG D is the sidecar-specific ambiguity proof; the in-process
   ambiguity handling is pinned at the wallet layer
   (the parity lane, tests/cloud-lab/parity on PR #1137) and in unit
   tests — the coverage statement is written here so nobody re-derives it.

Wallet-layer conformance (same WalletPort money-cycle assertions on
gonuts in-process / cdk-walletd sidecar / cdk-go in-process): the parity
lane under tests/cloud-lab/parity/ — PR #1137 until it merges.
