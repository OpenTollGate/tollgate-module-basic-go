# D5 retest — does Hedgehog give a payer who is non-custodial AND chain-free?

**Date:** 2026-10-15 · **Core:** v31.1.0 regtest · **Salvage note:** the delegation
that built this POC timed out at 3600 s after 121 API calls, *after* finishing and
validating the library but *before* writing the runner. `poc/run_poc.py` is the
last-mile salvage, written against the real API in the surviving modules.

## VERDICT: **D5 IS NOT MET**

Hedgehog is a genuine advance over V0/V1/V2/V4 — but it **relocates** the time
obligation rather than removing it, exactly the pattern the prior spike predicted.

| D5 sub-condition | Met? | Basis |
|---|---|---|
| non-custodial (no third party holds funds) | **YES** | 2-of-2 P2TR; NUMS internal key means no key path, so only script paths can spend |
| sender can act while recipient is offline | **YES** | leg B: sender produced the update with zero recipient input |
| recipient may be offline when sender proposes | **YES** | leg B modelled recipient as holding no key material |
| payer needs **no chain state ever** | **NO** | the escape hatch is a CSV timelock; safety needs someone to observe the chain inside the window |
| **no liveness/timelock obligation on any party** | **NO** | timeout leaf is a liveness backstop; a sender that has sent cannot force-close, so it depends on the counterparty returning |

### The exact trust assumption Hedgehog requires

For Hedgehog to be safe, **both** must hold:

1. **the recipient eventually returns** — to countersign, or to take the timeout
   path; and
2. **SOME party observes the chain inside the CSV window** — to stop an outdated
   state being mined. This is a **watchtower-equivalent duty**.

Condition (2) is the load-bearing one. Hedgehog does not eliminate the
reconciler; it moves it from the sender to the counterparty.

### What is genuinely new

The prior spike found that "a channel does not DELETE a time obligation, it
RELOCATES it and removes the reconciler." Hedgehog **relocates the reconciler to
the counterparty** — that is what makes *propose-while-offline* work. So the prior
spike's framing holds, with a refinement: the reconciler is not removed, it is
**relocated to the other party**, and whether that is acceptable depends on
whether you can rely on that party returning. That is the open question, not a
settled property.

## What is PROVEN by execution

Every check in `poc/run_poc.py` is labelled by evidence class. 13 checks pass.

**`[CORE]` — Bitcoin Core itself accepted or rejected something:**
- Core recognises our taproot output key for the real 3-leaf Hedgehog tree as a
  valid `witness_v1_taproot` key.
- The 2-of-2 P2TR output was funded and confirmed (`getrawtransaction`).
- Core **verified and ACCEPTED the fully-signed script-path spend**
  (`allowed=True`, broadcast `d9ddaa98…`) — after we fixed two bugs of our own;
  see `SIGHASH-FIXED.md` and the corrected honest-boundary section below.

**`[CORE + JS]` — the strongest validation, from `poc/xcheck.py` (10/10):**
- X1 BIP-340 official test vector: pubkey, signature, verify.
- X2 Core's `getdescriptorinfo`/`deriveaddresses` agrees with our output key for
  1-leaf and 2-leaf trees.
- **X3 the SHIPPED `tapscript.js` (hedgehog-advanced, executed under node) agrees
  with our tree root, output key, control block, and p2tr address byte-for-byte.**
  This is load-bearing: it proves the Python models the tree *the deployed site
  actually builds*, not merely "a" BIP-341 tree.
- X4 `OP_RIPEMD160` behaviour.

**`[OURS]` — corroboration only (our own verifier, NOT consensus evidence):**
- sender produces a valid signature with no recipient input;
- recipient produces their half with no sender assistance;
- the 3-leaf tree carries both a revocation leaf and a timeout leaf;
- the timeout leaf uses a block-based `OP_CSV`;
- **the control block is 97 bytes** (33 + a 64-byte sibling path) for the 2-of-2
  leaf in a 3-leaf tree — contrast the sibling single-leaf POC, where the correct
  control block was exactly **33 bytes** with no path. Two POCs, two different
  correct answers, both now measured.

## What is NOT proven — honest boundary

1. ~~**No end-to-end `[CORE]` acceptance.**~~ **RESOLVED — now accepted.**
   The reject was **our bug**, twice over, and both are fixed. Our BIP-341
   sighash omitted `sha_sequences` (32 bytes) and used the ANYONECANPAY per-input
   form plus double-SHA256 streams; separately the witness stack order was
   `[sig_A, sig_B]` where `<A> CHECKSIG <B> CHECKSIGADD 2 EQUAL` needs
   `[sig_B, sig_A]`. With both fixed:

   ```
   Core testmempoolaccept -> allowed=True reason=''
   broadcast: d9ddaa98a8d754cb30d5d1c7baeffcbd2121f28157cf719b2cc72c73f8a4234d
   ```

   Note what this cost: our harness and a hand-written "independent"
   implementation **agreed with each other and both disagreed with Core**,
   because both were written from the same misreading. Self-agreement is not
   evidence. See `SIGHASH-FIXED.md`.
2. **The override race is not executed.** Demonstrating that a sender's rollback
   can be overridden inside the CSV window needs a reorg or a time-warp; this
   harness does not model either. That game-theoretic claim stays **REASONED**.
3. **The shipped timelock constant is inconsistent, and we reproduce it rather
   than hide it:** `hdg.py` uses `CSV_TIMEOUT_LEAF = 10` blocks while the README
   claims 4032 (the code carries `//TODO: change the 10 to 4032`). Reported as
   found.
4. **Node.js was available but the shipped JS was used only as an oracle** (X3),
   not as a full second implementation of the flow.

## Comparison against the prior variants

| | non-custodial | sender offline | recipient offline | needs chain state | needs watchtower | finality delay | removes or relocates? |
|---|---|---|---|---|---|---|---|
| **V0** naive payee-signs-alone | ✗ | — | — | — | — | — | removes, but payee can steal |
| **V1** Spilman + per-granule cosign | ✓ | ✗ | ✓ | minimal | no | none | removes (at cost of interactivity) |
| **V2** pre-signed ladder | ✓ | ✓ | ✓ | exit only | no | none | removes, but over-provisioning is stealable |
| **V4** duplex / LN-penalty | ✓ | ✗ | ✗ | yes | **yes** | `to_self_delay` | relocates (watchtower) |
| **Hedgehog** | **✓** | **✓** | **✓** | **yes (CSV window)** | **watchtower-equivalent duty** | CSV window | **relocates to the counterparty** |

Hedgehog is the first construction here that achieves non-custodial **and**
sender-offline **and** recipient-offline simultaneously. It pays for that with a
timelock window that somebody must watch — which is why D5 is still not met.

## Reproduce

```bash
cd poc
python3 xcheck.py     # 10/10 independent cross-checks (Core + shipped JS)
python3 run_poc.py    # the flow; prints the D5 verdict and self-labels each check
```
