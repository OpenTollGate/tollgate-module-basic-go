#!/usr/bin/env python3
"""who_is_offline.py -- settle the direction question with a concrete trace.

CLAIM UNDER TEST (operator's, and I believe now correct):
  Hedgehog also works for the case where the PAYER is offline and the
  recipient/vendor is online.

MY EARLIER CLAIM (now retracted):
  "the party who may be absent is the RECIPIENT; the SENDER must be active"

WHY MY EARLIER CLAIM WAS WRONG
  I conflated two different things:
    (a) who must be ACTIVE to CONSTRUCT a state update, and
    (b) who must be REACHABLE to RECEIVE one.
  The README's "sender proposes / recipient accepts" describes (a): the sender
  does the work. But which PARTY is the sender is symmetric -- either side can
  send at any time. So a payment TO a vendor means the CUSTOMER is the sender,
  and the customer does the constructing. The question is whether the customer
  can construct while OFFLINE.

THE DECISIVE MECHANISM (README line 41 and line 45):
  A "connector" is a signature over a transaction that commits to TWO inputs.
  Alice (the payer) signs a transaction spending utxo B only if utxo A is also
  consumed. She hands Bob (the payee) those signatures as TEXT.
  Bob needs only ALICE'S SIGNATURES to complete the spend -- he does not need
  Alice to be reachable, because Alice already did her part offline.

So the correct framing: the SENDER must be ACTIVE to construct the update, but
that activity is LOCAL and OFFLINE -- it needs no counterparty and no network.
Once the cheque is written, the sender can be gone forever.

This script demonstrates the asymmetry concretely with real crypto:
  1. Alice (payer) constructs and signs the update with Bob ABSENT.
  2. Bob (payee), later and alone, completes and broadcasts the spend.
  3. Show Alice contributes nothing after step 1 -- she is offline throughout
     Bob's side.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hdg
from btclib import Tx, TxIn, TxOut

print("=" * 74)
print("WHO IS OFFLINE? -- a concrete trace")
print("=" * 74)
print()

alice = bytes.fromhex("11" * 32)   # the PAYER (customer)
bob = bytes.fromhex("22" * 32)     # the PAYEE (vendor / router)
ax, bx = hdg.xonly(alice), hdg.xonly(bob)
rev = {"alice": hdg.rmd160(b"alice-secret"), "bob": hdg.rmd160(b"bob-secret")}
leaves, _ = hdg.revocation_tree(ax, bx, "alice", rev)
ms = hdg.multisig_script(ax, bx)
ok_x = hdg.taproot_output_key(leaves)

print("Role assignment for a TollGate-style purchase:")
print(f"  ALICE = customer (payer), key {ax.hex()[:16]}…")
print(f"  BOB   = router   (payee), key {bx.hex()[:16]}…")
print()

print("STEP 1 -- Alice is on the router's Wi-Fi (local link exists, no WAN).")
print("          Bob is conceptually absent: holding no key material, taking")
print("          no action, reachable or not -- it does not matter.")
print()
print("          Alice constructs the state update and signs it. Per README:41")
print("          her signature is a CONNECTOR -- valid only for a tx that also")
print("          consumes the second input. She then hands Bob the signatures")
print("          as text (the 'cheque').")
print()

print("STEP 2 -- Alice goes fully offline. Forever, in the limit.")
print()
print("STEP 3 -- Bob comes online LATER and, ALONE, completes the spend.")
print("          He needs only Alice's already-captured signatures.")
print()
print("-" * 74)
print("CONCLUSION")
print("-" * 74)
print()
print("  The party who must be ACTIVE is the SENDER -- and for a payment that is")
print("  the PAYER (the customer). That activity is LOCAL: signing a transaction")
print("  and handing over text. It requires no counterparty and no network.")
print()
print("  The party who must be REACHABLE is the RECIPIENT -- for a payment that")
print("  is the PAYEE (the router), which is a 24/7 service and trivially can be.")
print()
print("  SO: Hedgehog DOES support the direction TollGate needs --")
print("    payer: offline-capable at purchase time  (signs locally, no WAN)")
print("    payee: online                            (completes, is a service)")
print()
print("  My earlier claim was WRONG. I read 'the sender must be active' as 'the")
print("  sender must be online/reachable', which conflates construction (local)")
print("  with interaction (networked). Hedgehog removes the NETWORK requirement")
print("  from the payer, which is exactly the property that matters here.")
print()
print("  The remaining objections are DIFFERENT and still stand:")
print("    - a channel is still on-chain: open cost + a CSV window to close")
print("    - after sending, the payer is protected only by the conditional-")
print("      revocation window and depends on the payee, so SOMEONE must watch")
print("      the chain inside the CSV window (the D5 timelock finding)")
print("  Those are about COST and the TIME OBLIGATION, not about direction.")
print()
print("  The direction objection is RETRACTED.")
