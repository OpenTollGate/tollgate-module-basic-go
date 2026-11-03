# Custom-unit issuance: measured, and where it stops

Measured 2026-11-03 against a real `cdk-mintd 0.18.3` configured with
`unit = "mb"` and `custom_payment_methods = ["branch"]` — the same shape a
Pecan-fronted mint has. No funds moved (fakewallet); the keysets, quotes and
proofs are real.

## What was exercised

A mint on `127.0.0.1:3340`:

- `[payment_backend] unit = "mb"`
- `[fake_wallet] custom_payment_methods = ["branch"]`
- `/v1/keysets` → units `['mb']`
- NUT-04 methods → `bolt11/mb`, `bolt12/mb`, `onchain/mb`, **`branch/mb`**

A module config declaring that mint with `unit`, `price_unit` and the new
`payment_method`:

```json
{"url":"http://127.0.0.1:3340","price_per_step":1,"price_unit":"mb",
 "unit":"mb","payment_method":"branch","min_purchase_steps":1}
```

## Result: issuance works on the cdk backend

`IssueOnRequest(mint, "mb", 8)` completed:

```
issue: quote 01a1a1e4-… requested for 8 mb at http://127.0.0.1:3340
issue: issued 8 mb from quote 01a1a1e4-… (1830 byte token)
```

The mint's own log, for the same quote:

```
post_mint_custom_quote{method="branch"}:get_mint_quote: mint quote created
  and persisted quote_id=01a1a1e4-… method=branch unit=mb state=UNPAID
wait_for_processor_payments:handle_payment_notification: mint quote payment
  notification committed method=branch unit=mb previous_state=UNPAID
  new_state=PAID amount_paid=8
cache_post_mint_custom{method=Path("branch")}:process_mint_request: mint
  quote issuance committed new_state=ISSUED amount_issued=8
```

The returned token decodes to `mb` — and contains no `sat`:

```
cbor map version byte: 0xa3
contains b'mb'        : True
contains mint url     : True (127.0.0.1:3340)
contains b'sat'       : False
```

So the payment method is a real, distinct addressable thing, and a
`branch`/`mb` quote round-trips: quote → PAID → ISSUED → token in `mb`.

## Result: it does NOT work on the default gonuts backend

The same config, same mint, default build (`go build`, no tags — this is what
ships on a router):

```
WARNING: Wallet initialization failed (failed to create wallet: error adding
  new mint: could not find an active keyset for the unit) — starting in
  degraded mode
```

Cause, in the pinned `gonuts-tollgate` (`wallet/wallet.go:128`):

```go
wallet := &Wallet{db: db, unit: cashu.Sat, masterKey: masterKey, ...}
```

The unit is hardcoded at construction and there is no parameter or setter.
`AddMint` then calls `GetMintActiveKeyset(mintURL, w.unit)` with `sat`, so an
`mb`-only mint has no matching keyset and the wallet refuses to load.

**This is the blocker for Pecan on real hardware.** The custom-unit path is
reachable only through the `cdk_wallet` build tag, which the router build does
not use and cannot use in-process: cdk-go is CGO/FFI and the module must stay
`CGO_ENABLED=0` on mips/mipsel/arm/arm64 (AGENTS.md, "Pure-Go/OpenWrt
constraint"). The sanctioned route is the sidecar (`sidecar.go`), which is a
separate piece of work.

## What this means for C (Pecan teller)

Two independent gates, not one:

1. **Method name** — was hardcoded-ish (`TOLLGATE_CDK_PURCHASE_METHOD`,
   default `"paypal"`, derived from the *unit*). Now per-mint config
   (`accepted_mints[].payment_method`), and an unconfigured custom-unit mint is
   refused rather than sent to a guess. **Done.**
2. **Unit support in the shipping wallet** — absent in gonuts. Needs either
   gonuts to become unit-parametric (upstream fork work, and `w.unit` is used
   in many places, so it is not a one-line change) or custom-unit redemption
   routed through the CDK sidecar. **Not done.**

NUT-20 quote locking, noted separately, remains a third gap for a real Pecan
mint (it rejects unlocked quotes; `RequestMintQuote` passes `nil`).

## Reproducing

```
# mint
cd <this repo>/mbtest
CDK_MINTD_MNEMONIC="..." cdk-mintd -w ./db config init --new-mint --file ./config.toml
CDK_MINTD_MNEMONIC="..." cdk-mintd -w ./db --enable-logging

# issue (cdk backend; the default build refuses at wallet load, which is the
# finding above)
go build -tags cdk_wallet -o /tmp/issues /tmp/issue_test/main.go
/tmp/issues ./mbtest/tollgate/config.json
```
