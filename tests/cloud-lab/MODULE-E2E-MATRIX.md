# Module-level e2e conformance matrix — gonuts (default) vs cdk (sidecar)

Driver: `run-module-e2e.sh` (one driver, one leg logic, two backends;
`run-cdk-backend-e2e.sh` is the BACKEND=cdk shim). Run:

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
| LEG B — dependency killed mid-payment, then replay | single-grant value verdict ✓ (first attempt settled nowhere; replay the single grant, allotment bound respected) | single-grant value verdict ✓ — first attempt refused 21023 through the module (the non-vacuous mint-outage refusal), replay refused, no double grant |
| LEG D — deterministic ambiguity (sock proxy drops the daemon's answer to receive) | `payment-outcome-unknown` (do-not-resubmit); deterministically-spent replay refused; recovery on the direct socket | N/A on this backend — no proxyable socket; the ambiguity class is covered for gonuts by the wallet-layer parity lane (three-backend, all green) and the purchasesession outcome-unknown unit tests |

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
   (tests/cloud-lab/parity) and in unit tests — the coverage statement is
   written here so nobody re-derives it.

Wallet-layer conformance (same WalletPort money-cycle assertions on
gonuts in-process / cdk-walletd sidecar / cdk-go in-process): see
tests/cloud-lab/parity/README.md.
