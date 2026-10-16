# Retraction: Hedgehog DOES support the direction TollGate needs

**My earlier answer was wrong. The operator's recollection was correct.**

I previously claimed:

> "The party who may be absent is the RECIPIENT. Hedgehog requires the SENDER to
> be active... that is the opposite party from the one TollGate needs offline."

**That claim is retracted.** Executed against Bitcoin Core regtest:

```
PAYER SIDE (Alice) -- Bob takes NO action and is NOT contacted:
  payer signs the update locally: c0b9b0658f508ad9ba2f64e7aaedffc1…
  ...and hands it over as text. The payer's work is now DONE.
PAYEE SIDE (Bob) -- alone, later, using ONLY the captured signature:
  Core testmempoolaccept: allowed=True reason=''
  BROADCAST: bd18f7c39d06b9bb76806d34d69bb64e486c955edc1d4c101223b1a65cb59f0e
```

Reproduce: `poc/prove_offline_payer.py`.

## Where my reasoning went wrong

I conflated **two different properties**:

| | Meaning | Requires |
|---|---|---|
| **Construction** | who does the work of building and signing an update | local computation only |
| **Interaction** | who must be reachable for the protocol to complete | a network path |

The README says *"State updates only require the sender to propose an update and
the recipient to accept it."* I read "the sender must propose" as **"the sender
must be online"** — that was the error. Proposing is **construction**, and
construction is **local**: sign a transaction, hand over text. It needs no
counterparty and no network.

And critically, **"sender" is not a fixed role.** Either party can send at any
time, so in a purchase the *customer* is the sender. The customer does the
constructing — offline, on the router's Wi-Fi — and the *router* is the recipient
who completes later. That is exactly the shape TollGate needs.

## Why the mechanism allows it (the part I misread)

README line 41, on connector outputs:

> "Bob can create a signature that sends money from that address to Alice, but,
> since signatures can commit to multiple inputs, he can make his signature
> conditional... The second input is called a connector."

The payer's signature is **conditional on a second input existing** — so it can be
captured as inert data and used by the counterparty whenever they please. The
README states the consequence outright:

> "And they can send the new state change (the new cheque) to you **even if you
> are still offline**."

The text explicitly contemplates the counterparty being offline. I anchored on the
*sender-proposes* framing and did not follow the asymmetry through to "who is the
sender in this transaction."

## What is NOT retracted

The direction objection is withdrawn. **Two separate objections survive**, and
neither is about direction:

1. **Cost.** A channel is still an on-chain object: open cost, and the update
   cycle costs a dust UTXO plus fees per state change. At the shipped TollGate
   price of **1 sat = 21 MiB**, this is orders of magnitude too expensive for
   micropayments, though it becomes plausible for a prepaid bulk purchase
   (`docs/architecture/one-way-channel-spike.md` §10).

2. **The time obligation — the actual D5 finding.** README line 73: once a party
   sends, it *"cannot safely do anything"* and is protected only by the
   **conditional-revocation CSV window**. So someone must observe the chain inside
   that window. Note the operator's point interacts with this: **the router is
   online and can observe the chain**, which is a much better answer than "the
   customer must." That materially strengthens Hedgehog's fit — the watchtower
   duty lands on the party that is reliably present.

## Corrected D5 assessment

| Property | Before my error | Corrected |
|---|---|---|
| payer offline-capable | ✗ (I claimed no) | **✓ yes** |
| payee online to complete | — | ✓ trivially, it is a service |
| router can watch the chain | not considered | **✓ yes — so the timelock duty lands on the present party** |
| non-custodial | ✓ | ✓ |
| still on-chain cost | ✓ | ✓ (unchanged) |
| **D5: non-custodial AND payer-offline** | "no" | **the direction objection is gone; the remaining obstacle is COST and the CSV window, not interactivity** |

This is a **material improvement to Hedgehog's standing** in the programme and it
changes the recommendation's emphasis: the blocker is no longer "the wrong party
can be offline" but "an on-chain channel costs more than the instrument it
replaces, unless the purchase is large."

## Lesson

I answered a *protocol capability* question from prose framing instead of
executing the capability. The claim was checkable in one script — and the
operator's recollection was the signal I should have treated as a reason to test
rather than to restate. Recorded because it is the same failure mode as the
sighash bugs: reasoning about a specification instead of reading or running it.
