# The Hedgehog [CORE]-accept gap, and who must be online

Two questions answered from the shipped sources. Written 2026-10-15.

---

## Q1. Is the `Invalid Schnorr signature` an implementation bug or a Hedgehog flaw?

**Verdict: an implementation bug in OUR harness — specifically in our BIP-341
sighash plumbing. It is NOT a flaw in Hedgehog.** But I did not finish finding it,
and this document says exactly where the search stopped.

### How we know it is ours, not Hedgehog's

Two independent pieces of evidence, both from Bitcoin Core:

1. **Core accepts script-path spends of the very same node.** In
   `research/router-taproot-verify`, Core signed and accepted
   (`allowed: true`, vsize 117) a P2TR script-path spend. So the node, the
   funding, the control-block shape and Core's own signing rails all work here.
2. **Our taproot tree agrees with Core byte-for-byte** — for 1-, 2- and 3-leaf
   trees, via `xcheck.py`'s X2/X3 cross-checks and `src/taprootverify`'s
   Core-derived fixtures. The commitment layer is right.

So the failure is confined to the one layer that Core *did* rule on and we did
*not* cross-check: **the BIP-341 sighash for the script path.**

### What I ruled OUT (each tested, so the next attempt does not repeat them)

| Hypothesis | Test | Result |
|---|---|---|
| Witness stack order (`[sig_A,sig_B]` vs `[sig_B,sig_A]`) | `wit_order.py` — both orderings submitted | **neither verifies**; not the cause |
| Leaf-version byte missing from the tapleaf hash | compared `leaf_hash()` to a hand-rolled BIP-341 tag | **correct** (0xc0 prefix present) |
| Doubling vs single SHA256 in the BIP-341 streams | built both variants, tested against Core's own accepted signature | **neither verifies** |
| Number of `sha_scriptpubkeys` bytes (bare vs length-prefixed) | brute-forced 24 combinations of stream-hash / spk-form / spend_type / suffix | **no combination verifies** |
| Control block length | measured: 33 B for a single-leaf tree, 97 B for a 3-leaf tree — both as Core requires | **correct** |

### The diagnosis that matters

Our harness implementation and a hand-written independent implementation **agree
with each other and both disagree with Core** (`sighash_pin.py`:
`hand == harness : True`, both `verifies=False`). Two agreeing implementations
that are jointly wrong means a **shared wrong assumption about the BIP-341 message
construction** — not a coding slip, a specification misreading. That is exactly the
class of error that a Core cross-check exists to catch, and it is why this leg must
not be reported as a pass.

### The highest-value next step, precisely

Do not extend the brute-force. Instead obtain a *known-good* sighash directly:

```bash
# 1. Fund a tr(NUMS, {<our leaf>}) output — our commitment math is already
#    proven to agree with Core.
# 2. Import the descriptor WITH the private key so Core holds the leaf key:
#      bitcoin-cli -rpcwallet=rig importdescriptors \
#        '[{"desc":"tr(<NUMS>,<privkey-desc>)#<checksum>","timestamp":"now","active":false}]'
#    (the checksum must be taken from getdescriptorinfo over the PRIVATE text —
#     this is the step that took several attempts; see kernel_check findings)
# 3. Let Core build and sign the spend:
#      signrawtransactionwithwallet
# 4. Dump the witness, take Core's signature, and test OUR sighash against it.
```

Step 4 is the decisive assertion: if our sighash reproduces the message Core
signed, the BIP-341 construction is right and the residual bug is a witness
detail; if not, diff the message byte-by-byte against
`src/taprootverify`'s now-trusted commitment code.

### Does it change the D5 verdict?

**No.** D5 rests on the **timelock structure** — the conditional-revocation third
tapleaf and the CSV window — which is directly observable in the tree and was
independently validated against the shipped `tapscript.js`. The sighash bug
affects whether *our harness* can complete a spend, not whether Hedgehog's design
relocates the time obligation. The D5 conclusion stands; what is withdrawn is any
implication that the harness demonstrated an end-to-end spend.

---

## Q2. Does Hedgehog let the SENDER pay while the sender is offline?

**Verdict: no — and this is a structural mismatch with what TollGate wants. It is
not a matter of our implementation.**

### What Hedgehog actually provides

From the README, verbatim:

> "State updates only require **the sender to propose an update and the recipient
> to accept it**" (line 9)

> "you can — **without their assistance** — create a payment for them, embed it in
> a piece of text (think of it like a cheque), and send it to them... Then **you
> can go offline**. When **they get online**, they can either accept the state
> change (the cheque)..." (line 12)

So the direction is: **the SENDER works while the RECIPIENT is away.** The sender
must be online (or at least active) to *construct* the update and sign it. The
recipient is the party who may be absent and return later.

### Why this is the opposite of TollGate's need

The brief asks for the case where **the sender pays while the sender is offline**.
Read the two roles in TollGate's actual flow:

- In a **bearer/micropayment purchase**, the party who must act without a live
  counterparty is the **customer** — who may well have no WAN, which is the entire
  premise of the product. The customer is the **paying** party.
- Hedgehog's non-interactive party is the **receiving** party.

Those are different parties, so Hedgehog's headline property does not supply the
one TollGate needs. Concretely: Hedgehog would let the **router** (the recipient of
the payment) be offline while the **customer** proposes — but a router selling
internet is a 24/7 service, so that is not the hard case. The hard case is the
customer with no connectivity, and Hedgehog requires that party to be active
*specifically when constructing the payment*.

### The second, independent structural problem

Even ignoring direction, sending costs the sender liveness. README line 73:

> "once a party sends money to their counterparty, they give up their ability to
> force close the channel... Therefore **a sender, once they send, cannot safely
> do anything**, and can lose their money forever."

The fix is conditional revocation — the third tapleaf with a CSV window — and that
is the "relocates, does not delete" finding already recorded in
`D5-RETEST.md`. So the sender's position after sending is *protected by a
timelock*, not by being offline-capable.

### Therefore

| Property wanted | Hedgehog |
|---|---|
| recipient offline while sender proposes | **yes** |
| **sender offline while sender pays** | **no — sender must construct and sign the update** |
| sender safe after sending, without liveness | **no — protected only by a CSV window somebody must watch** |

Hedgehog is a well-designed answer to *"how does a recipient accept a payment
while offline"*. It is **not** an answer to *"how does a payer pay with no
connectivity and no custodian"* — which was, and remains, D5.

This is a **conceptual** finding, independent of the sighash bug, and it is the
more important of the two: even a fully working Hedgehog harness would not have
changed the verdict.

---

## Summary

| Question | Answer |
|---|---|
| Implementation bug or Hedgehog flaw? | **Our harness** — a BIP-341 sighash misreading. Not finished; the exact next step is named above. Core proves the node and the trees are right. |
| Does it change D5? | **No** — D5 rests on the timelock structure, independently validated. |
| Does Hedgehog support sender-offline *sending*? | **No** — that is the recipient's property. It is the opposite party from the one TollGate needs offline. |
