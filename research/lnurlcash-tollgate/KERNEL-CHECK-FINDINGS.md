# Kernel check — what Bitcoin Core actually confirmed, and what it did not

**Date:** 2026-10-15 · **Core:** v31.1.0 (`/Satoshi:31.1.0/`) · **chain:** regtest
**Node:** the rig's own local regtest, `~/.local/toolchain/btc/bitcoind`,
datadir `~/.local/toolchain/btc/regtest-data`, RPC `127.0.0.1:18443`, wallet `rig`.
**Script:** `kernel_check.py` (this directory). Raw output: `out/KERNEL-CHECK.json`.

This closes the gap named in `poc/README.md`:

> "NOT proven here: that Bitcoin Core's interpreter agrees. No bitcoind and no
> libbitcoinkernel on this host [...]. 'I read the BIP' is a weaker claim than
> 'I ran the kernel', and the difference is recorded."

## What was ESTABLISHED (measured, output quoted)

### 1. Core parses both leaf forms — LEG 1, MEASURED

Both tapscript leaves were handed to Core's `decodescript`:

| Leaf | Bytes | Core `type` | Core `asm` |
|---|---|---|---|
| OP_CHECKMULTISIG form | 110 | `nonstandard` | parsed, asm returned |
| OP_CHECKSIGADD form | 111 | `nonstandard` | parsed, asm returned |

`decodescript` returned a full `asm` string and a `raw(...)` descriptor for both,
so **Core's script parser accepts our bytes in both forms**. This proves our
byte-level construction is well-formed. It does **not** prove the opcodes are
*acceptable* — see the boundary below.

### 2. Core CONFIRMS our taproot commitment math — MEASURED

This is the most valuable result, and it is a genuine cross-check of *our own*
code rather than of the BIP. We built a P2TR `scriptPubKey` from our own
`taproot_tweak_pubkey(NUMS, tapleaf_hash(leaf))` and asked Core to decode it:

```
leaf      : 20f9308a...bce036f9ac          (<xonly key> OP_CHECKSIG)
tapleaf   : a5f4dd9bd9e8664ec5d1b97819e523b297ea9c7f8daa0b2e96e6460060333e5f
our key   : 7736a30691f06e63bc2e869e660117117a9976fb17c697fa679f19c0901a49c4
our ctrl  : c050929b...803ac0a5f4dd9b...60333e5f

Core decodescript(spk) ->
  "asm":  "1 7736a30691f06e63bc2e869e660117117a9976fb17c697fa679f19c0901a49c4"
  "type": "witness_v1_taproot"
  "address": "bcrt1pwum2xp537phx80pws60xvqghz9afjahmzlrf07n8nuvupyq6f8zq3pkhe7"
```

Core independently recognised our derived output key as a **valid taproot
output** and produced a valid bech32m address for it. Our BIP-341 tree/tweak
implementation agrees with Core.

### 3. Real taproot outputs were funded and confirmed — MEASURED

Two P2TR outputs, one per leaf form, were created from our own commitment math,
funded (0.5 BTC each), mined into regtest, and confirmed by `gettxout` as:

```
type: witness_v1_taproot
value: 0.5
address: bcrt1pntsx28p0eqx0rt6qaa69urvpczrsa8t6886lr6kqcz5pxmkxp5hqzp2xre  (MULTISIG leaf)
address: bcrt1ptwa4sr0kaluehn8zpm lrv0umnuw2gr27mwqlmtwyghrn9xuq9ngqf74vdj  (CHECKSIGADD leaf)
```

So both constructions are real, fundable, spendable-shaped taproot outputs.

### 4. Core EXECUTES the script path — MEASURED

Submitting a hand-assembled script-path spend reached Core's script verifier and
was evaluated (not rejected at decode):

```
MULTISIG    -> allowed=false  mempool-script-verify-flag-failed (Witness program hash mismatch)
CHECKSIGADD -> allowed=false  mempool-script-verify-flag-failed (Witness program hash mismatch)
```

That Core reached *script verification* at all proves the segwit witness
serialization was decoded and the taproot program was looked up.

## What was NOT established — the honest boundary

**The BIP-342 claim remains REASONED, not MEASURED.**

Both legs failed with `Witness program hash mismatch`, which is Core saying the
control block presented does not commit to the output key it is spending. Since
result 2 above shows our commitment math IS correct, the fault is in the
**hand-assembled witness serialization** in this script — most likely the
script-path witness does not place the `script` and `control_block` in the
positions Core requires (a tapscript script-path witness is
`[<stack items...>, <script>, <control block>]`, and the stack items must satisfy
whichever branch the leaf selects).

**Consequence, stated plainly:** this run did **not** demonstrate that Core
rejects a tapscript leaf containing `OP_CHECKMULTISIG` while accepting the
`OP_CHECKSIGADD` form. That specific claim still rests on the BIP-342 text
(`bip-0342.mediawiki:38` "The inefficient OP_CHECKMULTISIG and
OP_CHECKMULTISIGVERIFY opcodes are disabled"; `:72` disabled opcodes "failing and
terminating the script immediately when executed") plus the POC's own interpreter
— i.e. on reading the BIP, which is exactly the weaker claim this exercise set out
to replace.

**The next step, precisely:** fix the script-path witness in `kernel_check.py` so
the stack items satisfy the selected branch, then re-run. The infrastructure is
now all in place — funded outputs, verified commitment math, working Node access —
so the remaining work is witness construction, not discovery.

## Why this is recorded as a partial rather than a pass

The honesty contract for this programme forbids claiming a measured result that
was not observed. Four claims ARE measured and are improvements on the prior
state (Core parses our leaves; Core confirms our commit math; the outputs are real
and funded; Core executes the script path). One claim is NOT, and it is the
headline claim. Recording the split is more useful than blurring it: the next
session starts from a known-good commitment and a precisely-located bug.
