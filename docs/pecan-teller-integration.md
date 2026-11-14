# C (Pecan teller flow): why it does not work today, and what it needs

Investigated 2026-11-03 by reading Pecan (`github.com/zeugmaster/pecan`) and this
repo's wallet seam. Recorded so the next person does not re-derive it.

## What Pecan actually is

Not a module feature — a **mint-side** payment processor:

- Rust binary (`processor/`) implementing cdk's `PaymentProcessor` trait over
  **gRPC**: `get_settings`, `create_incoming_payment_request`,
  `get_payment_quote`, `make_payment`, `wait_payment_event`,
  `check_incoming_payment_status`, `check_outgoing_payment`.
- Pinned to `cdk-common` / `cdk-payment-processor` `=0.18.0-rc.0`, which must
  contain `cashubtc/cdk#2295` (propagate `quote_id` and `pubkey` on custom
  payment methods).
- A web console (`web/`) where a human teller matches a quote by its last 6
  characters and presses **Cash received**.
- It registers the custom payment method literally named **`"branch"`**.

So Pecan does not call the router at all. It makes a cdk mint able to issue
custom-unit ecash against a human-settled counter. The TollGate module is a
different participant: it is a *wallet* that asks the mint for a quote and mints
against it (`Merchant.IssueOnRequest`).

## The incompatibility: the method name does not match

`src/tollwallet/cdk_wallet.go:392` (`(*CdkWallet).RequestMintQuote`) already
does the right *shape* of thing — for a custom-unit mint it asks the mint for a
custom-method quote, not a bolt11 one:

```go
method := cdk_ffi.PaymentMethod(cdk_ffi.PaymentMethodBolt11{})
if <mint has a custom unit> {
    method = cdk_ffi.PaymentMethodCustom{Method: customUnitPaymentMethod(u)}
}
quote, err := wallet.MintQuote(method, &amt, nil, nil)
```

and `customUnitPaymentMethod` (`cdk_wallet.go:641`) returns:

```go
if m := os.Getenv("TOLLGATE_CDK_PURCHASE_METHOD"); m != "" { return m }
return "paypal"
```

**The default is `"paypal"`; Pecan answers on `"branch"`.** Attach a Pecan-backed
mint and every issuance quote is sent to a payment method the mint does not
have, so the mint refuses. Nothing in this repo mentions "branch".

That also explains the odd default: `"paypal"` is a leftover from the bench
harness (a method the test mint stubbed), promoted to a shipped default.

## What C actually needs

Three things, in order of size:

1. **Make the method name configuration, not a hidden env var with a wrong
   default.** The method belongs with the mint's config (`accepted_mints[].unit`
   already says what the mint issues; a sibling `payment_method` says who settles
   it). An env var per-process is the wrong scope for a per-mint fact — this repo's
   own "one declaration per fact" doctrine.
2. **Do not conflate "custom unit" with "operator settles by hand".** A custom unit
   can be settled by anything; the quote method is what selects the processor.
   Deriving the method from the unit (`customUnitPaymentMethod(unit)`) is the
   design error — it should be an explicit per-mint field with a documented
   default.
3. **The issuance flow must tolerate a quote that waits for a human.** This is
   already true and is the good news: `awaitQuotePaid` polls the mint's own quote
   state until it reports paid, with a bounded timeout, and the mint stays the
   authority. A teller taking 90 seconds shows up as a slow-but-valid quote. The
   timeout must be operator-tunable for counter use (the current default is far
   too short for "customer hands over cash").

## What is genuinely missing (not just config)

- **The reverse direction.** Pecan's wallet contract expects the *wallet* to
  create a NUT-20-locked quote and display the short code. `Merchant.IssueOnRequest`
  inverts that: the router creates the quote and blocks. For a teller to match on
  a customer's own phone, the client app would need to create and display the
  quote — that is a client-side feature, not this repo's.
- **NUT-20 locking on the issuance path.** Pecan rejects unlocked quotes
  ("branch mint quotes must be locked to a wallet key"). `RequestMintQuote` passes
  `nil` for the pubkey today, so a Pecan-backed mint would refuse it outright even
  with the method name fixed.

## Recommendation

Do not build a teller console here. The smallest honest step for C is:

1. add `payment_method` to `MintConfig` (default `bolt11`), stop deriving the
   method from the unit, retire `TOLLGATE_CDK_PURCHASE_METHOD`;
2. pass a NUT-20 pubkey on the custom-method issuance quote;
3. make the issuance wait operator-tunable.

That makes the module able to *use* a Pecan-backed mint. Anything beyond that
(short-code display, teller UI) belongs in the console Pecan already ships.
