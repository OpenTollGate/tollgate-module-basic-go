# Seal — preliminary reading (MANAGER, before consultant results)

Written BEFORE the consultant batch returned, so my own errors are visible when
compared against their findings.

Source: `lnurlcash/lnurl-wallet` @ `f1cdb9e5`, `src/addons/seals/seals.ts` (509 lines)
plus `manifest.ts` and `taproot.ts`.

---

## 1. What a seal actually is

An **off-chain non-fungible asset** anchored to a bearer note.

- **Genesis** fixes `assetId` (random 32 bytes), `name`, `description`.
- **Each transfer** increments `stateIndex`, sets `ownerPubkeyHex` to a **named**
  next owner, and sets `prevStateHash = sha256(prior state)`.
- **State** is committed into a taproot leaf via the *existing* `hashlock`
  template — `OP_SHA256 <hash> OP_EQUALVERIFY <pubkey> OP_CHECKSIG`
  (`seals.ts:74-91`, `taproot.ts:121-133`). No new script template.
- **Owner** either redeems the underlying value as an ordinary `cw1`
  (`redeemCurrentStateCw1`, `seals.ts:299-336`) or rotates into a new state.
- **Consignment** = mint note url/amount + full state history, one bech32m string,
  HRP `seal` (`seals.ts:410-456`).

Client-side validation (`sealChainProblem`, `seals.ts:343-373`) checks: identity
fixed at genesis, `stateIndex` increments by exactly 1, and each `prevStateHash`
really chains. Pure, offline, free.

The author is explicit that this borrows the **idea** from RGB/Taproot Assets
(state off-chain, taproot commitment as anchor, client-side validation instead of
trusting a sender) and is **not** RGB-compatible.

---

## 2. My preliminary finding: the bearer anchor is not committed

`encodeSealState` (`seals.ts:134-143`) hashes exactly:

```
domain tag ‖ assetId ‖ len‖name ‖ len‖description ‖ stateIndex ‖ ownerPubkey ‖ prevStateHash
```

`urlTemplate` and `amountMsat` are **not inputs**. They are written as a *header*
outside the per-state parts (`seals.ts:394-401`, `410-456`), and
`sealChainProblem` consumes **only** the `states` array (`seals.ts:343`).

I verified the encoder reproduces this: two consignments with byte-identical
`states` but different `urlTemplate`/`amountMsat` produce different payload hashes
while the state portion is unchanged — so `sealChainProblem` returns `''` (valid)
for **both**.

**Impact caveat I must check, not assume.** `redeemCurrentStateCw1` passes the mint
into `signScriptPathSpend` (`seals.ts:320-328`), and that function binds the domain
into the sighash (`taproot.ts:565-593`, `spendDomainOf(domain)` at :590). So the
signature IS bound to a specific mint at redemption time. That may mean an
attacker can get a recipient to *accept* a substituted anchor but still cannot make
it *redeemable*. The adversarial consultant is tasked to settle this precisely.

Also relevant: `encodeSealConsignment` **strips** `k1` and `c` from the
`urlTemplate` (`seals.ts:417-430`) as "this ONE claim's own one-time secrets, not
part of a reusable template". So the consignment is deliberately not itself
redeemable — which changes the substitution impact and needs working out.

---

## 3. Preliminary read on fit

**The instrument is non-fungible; TollGate sells fungible metered access.**
That is a real tension, and it is my main doubt about direct use.

But `name` / `description` are free text fixed at genesis, and `amountMsat` rides
along in the consignment. So a seal could plausibly model **"an access pass to a
named holder"** rather than "N bytes of data". Which of those is the right
modelling is exactly what I asked the fit consultant to argue.

**Against the prior research framing.** The established position is *two ends of
one dial* — metered Cashu micropayments at one end, prepaid high-volume
subscription where an on-chain channel becomes plausible at the other, with the
threshold a **purchase-size** property.

A seal needs **no channel open**, so on cost alone it is cheaper than Hedgehog. But
it is **custodial at the mint** — it sits on the same side as Cashu on the custody
axis, and adds non-fungibility. That would make it a *third* position on the dial
(cheap + custodian + non-fungible), not an extension of either existing end.

**What I do not yet have a view on**, and what the consultants should settle:
whether a named-recipient transfer is an asset or a liability for a router (what
happens when the named recipient never shows up), and whether client-side
validation of an ownership *history* matters at all for internet access.

---

## 4. What the author deserves credit for

- Reusing an already-proven template rather than inventing one.
- Choosing HRP `seal` over a 2-letter `c*` prefix, explicitly so it is not
  misrepresented as a standardized LUD-25 wire type (`seals.ts:54-62`).
- Canonical length-prefixed encoding rather than JSON, with the domain tag in the
  bytes because the leaf checks with plain `OP_SHA256`, not a BIP340 tagged hash
  (`seals.ts:109-115`).
- Decoder rejects trailing garbage and never throws (`seals.ts:176-213`).
- **Stating the limits himself** before anyone asked: named-recipient only, no
  race-to-claim, unredeemed transition is a promise, and history is not yet
  mint-certified. That is unusually honest and it made this review faster.
