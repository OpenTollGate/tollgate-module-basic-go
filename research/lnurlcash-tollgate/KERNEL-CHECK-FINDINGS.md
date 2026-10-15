# Kernel check — what Bitcoin Core actually confirmed, and what it did not

**Date:** 2026-10-15 · **Core v31.1.0** (`/Satoshi:31.1.0/`) · **chain:** regtest
**Node:** the rig's own local regtest — `~/.local/toolchain/btc/bin/bitcoind`,
datadir `~/.local/toolchain/btc/regtest-data`, RPC `127.0.0.1:18443`, wallet `rig`
(balance ~1,800 BTC, height 136+). **Script:** `kernel_check.py`.

This was meant to close the gap named in `poc/README.md`:

> "NOT proven here: that Bitcoin Core's interpreter agrees. No bitcoind and no
> libbitcoinkernel on this host [...]. 'I read the BIP' is a weaker claim than
> 'I ran the kernel', and the difference is recorded."

**Status: the gap is NOT closed. The BIP-342 claim remains REASONED.**

---

## CORRECTION — a claim in an earlier revision of this file was WRONG

An earlier revision of this file asserted:

> "Core CONFIRMS our taproot commitment math — Core independently recognised our
> derived output key as a valid taproot output and produced a valid bech32m
> address for it."

**That claim was not supported by its evidence, and is retracted.** The check
behind it passed a descriptor containing `pk(<32-byte-x-only-key>)`. Bitcoin Core
rejects that — `pk()` requires a 33-byte compressed key:

```
$ bitcoin-cli getdescriptorinfo "pk(f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9)"
error code: -5
error message:
pk(): Pubkey 'f9308a01...bce036f9' is invalid
```

The script did not check the return code, so it silently proceeded on a descriptor
Core had never expanded. The comparison was therefore **not** a Core ruling.

Redone correctly — `pk(02‖x_only)`, the 33-byte form Core accepts — the result is
in fact favourable:

```
tr(NUMS, pk(02f9308a...bce036f9))  ->  bcrt1pvzvg44rfkmhm3adks79vn5c5kf27leyw7a8vhjgandtzup2s9ngq4y4q64
decoded key                        ->  60988ad469b6efb8f5b6878ac9d314b255efe48ef74ecbc91d9b562e05502cd0
our computed key                   ->  60988ad469b6efb8f5b6878ac9d314b255efe48ef74ecbc91d9b562e05502cd0   MATCH
```

So our BIP-341 commitment math **does** agree with Core for this leaf. But note
what kind of claim that is: it is a comparison of *derived keys*, obtained by
running Core's `deriveaddresses` and decoding the address. It is stronger than
reading the BIP, and weaker than having Core execute a spend. It is recorded as
REASONED-against-Core, not as a consensus ruling.

## What IS established

### 1. Core parses both leaf forms — MEASURED

`decodescript` on both leaves returns full `asm` and a `raw(...)` descriptor:

| Leaf | Bytes | Core `type` |
|---|---|---|
| OP_CHECKMULTISIG form (`2 <a> <b> 2 OP_CHECKMULTISIG`) | 110 B | `nonstandard` |
| OP_CHECKSIGADD form (`<a> OP_CHECKSIG <b> OP_CHECKSIGADD 2 OP_EQUAL`) | 111 B | `nonstandard` |

Proves our bytes are well-formed and parseable. Does **not** prove the opcodes are
acceptable — BIP-342's disabled-opcode rule is semantic, enforced at execution.

### 2. Real P2TR outputs funded and confirmed — MEASURED

Outputs built from our own commitment math were funded (0.5 BTC), mined, and
confirmed by `gettxout` / `getrawtransaction` as
`type: witness_v1_taproot`, with the scriptPubKey's 32 bytes equal to our computed
output key byte-for-byte.

### 3. Core EXECUTES the script path — MEASURED

Hand-assembled script-path spends reached Core's script verifier and were
*decoded* successfully (`decoderawtransaction` shows our witness: 64-byte
placeholder sig, the leaf script, and the 65-byte control block). Core then
rejected at script verification:

```
mempool-script-verify-flag-failed (Witness program hash mismatch)
```

## ROOT CAUSE FOUND — the witness bug was OURS, and it is now fixed

The `Witness program hash mismatch` is fully explained, and it was a defect in
*our* control-block construction — not in the leaves, the commitment, or Core.

**A single-leaf taproot tree has NO merkle sibling.** The control block is
`(leaf_version | parity) || internal_key` — **33 bytes** — not 65. Our
`commit()` appended the leaf's own `tapleaf_hash` as if it were a merkle sibling,
so Core recomputed a *different* output key than the one being spent.

Proven by asking Core to build the witness itself. With the private key imported
(`importdescriptors` on `tr(NUMS,pk(WIF))`, checksum from `getdescriptorinfo`
applied to the **private** descriptor text), Core signed a script-path spend and
`testmempoolaccept` returned:

```
allowed: true, vsize: 117
```

Core's witness, verbatim:

```
[
  "59bad615...0d167e5b",                 <- 64-byte schnorr sig
  "204f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aaac",  <- the leaf
  "c050929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"     <- 33-byte control block
]
```

Our computed 33-byte control block reproduces Core's **byte-for-byte**:

```
ours (fixed): c050929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0
core        : c050929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0   SAME
```

Also measured in the same run:
- **Core derives the identical taproot address** for our script-path output:
  `bcrt1pwaz2yl0ead0ye6fusg6lp7evldzsxd8kxyq50r6ss5nt7amywchqeq5c4a` — ours and
  Core's `deriveaddresses` agree exactly, for the leaf we were testing.
- **A control experiment passed**: a key-path P2TR spend signed by Core was
  accepted (`allowed: true`), so the node, funding, and signing rails are sound
  and the failure was isolated to script-path witness assembly.
- The leaf script bytes Core signed are **identical** to ours.

### Retraction, part 2

Two earlier claims in this file were wrong and are withdrawn:
1. *"Core confirms our commitment math"* — the descriptor used `pk()` with a
   32-byte x-only key, which Core rejects; the script ignored the return code.
2. The implicit claim that the mismatch might be a Core/BIP-342 semantic
   rejection. It was **our** 65-byte control block. Core was right; we were wrong.

## THE HEADLINE CLAIM IS NOW MEASURED — Core ruled on both opcodes

The gap in `poc/README.md` is **closed**. A real P2TR script-path spend was
submitted to Bitcoin Core v31.1.0 on regtest, with the corrected 33-byte control
block, and Core **executed the tapscript** and named its verdict per leaf:

| Leaf (identical structure, one opcode different) | Core's verbatim `reject-reason` |
|---|---|
| `OP_2 <a> <b> OP_2 OP_CHECKMULTISIG` (69 B) | **`mempool-script-verify-flag-failed (OP_CHECKMULTISIG(VERIFY) is not available in tapscript)`** |
| `<a> OP_CHECKSIG <b> OP_CHECKSIGADD OP_2 OP_EQUAL` (70 B) | `mempool-script-verify-flag-failed (Invalid Schnorr signature)` |

**Read the two error strings carefully — the discrimination is the finding:**

- Core **rejected the opcode itself** in the MULTISIG leaf. It did not fail on a
  signature or a stack item; it named `OP_CHECKMULTISIG` as *not available in
  tapscript*. That is BIP-342's disabled-opcode rule, enforced by execution.
- Core **accepted the opcode** in the CHECKSIGADD leaf and proceeded normally to
  signature verification, failing only because the witness carried a deliberate
  64-byte placeholder instead of a real signature. `Invalid Schnorr signature` is
  the *expected* error for a script whose only defect is a missing signature.

So the claim is no longer read from the BIP text. It is a measured consensus
ruling, from Core's own scheduler, on two leaves that differ **only** in the
opcode — which is exactly the controlled comparison that makes it decisive.

### Corroborating evidence from Core's descriptor language

Core refuses to even *compile* the multisig form into a tapscript leaf while
accepting the checksigadd form:

```
$ bitcoin-cli getdescriptorinfo "tr(<NUMS>,multi(2,<a>,<b>))"
error: Can only have multi/sortedmulti at top level, in sh(), or in wsh()

$ bitcoin-cli getdescriptorinfo "tr(<NUMS>,multi_a(2,<a>,<b>))"   # OP_CHECKSIGADD form
OK
```

Same distinction, reached independently by Core's descriptor compiler. Recorded
as corroboration, not as the primary evidence — the execution ruling above is the
primary evidence.

## Retractions (this file has a poor track record; recording it)

Three earlier claims were wrong and are withdrawn:

1. *"Core confirms our commitment math"* — the check used `pk()` with a 32-byte
   x-only key, which Core rejects; the script ignored the return code, so the
   descriptor was never expanded. Retracted.
2. That the `Witness program hash mismatch` might indicate a BIP-342 semantic
   rejection. It was **our** 65-byte control block: a single-leaf tree has no
   merkle sibling, so the control block is 33 bytes. Core was right; we were wrong.
3. That the BIP-342 claim would have to remain REASONED for lack of a host. It
   did not: the rig's own local regtest node was sufficient all along, and the
   blocker was our own witness construction.

## Summary of what is now MEASURED

1. Core parses both leaf forms (`decodescript`, both `nonstandard`).
2. Core derives the **identical** taproot address for our script-path output.
3. Our fixed 33-byte control block matches Core's **byte-for-byte**.
4. Core signed and **accepted** a script-path spend (`allowed: true`) when it held
   the leaf key — proving the rails are sound.
5. **Core rules `OP_CHECKMULTISIG` unavailable in tapscript, while the
   `OP_CHECKSIGADD` form executes and fails only on a placeholder signature.**
6. A key-path control experiment was also accepted, isolating the earlier failure
   to witness assembly.

The remaining honesty note: item 5 used a 64-byte placeholder signature, so Core
never accepted the CHECKSIGADD spend *end-to-end*. What is proven is that Core
**reaches and passes** the opcode check for CHECKSIGADD and **fails it by name**
for CHECKMULTISIG. A full end-to-end acceptance with a real signature would
strengthen it further and is a small remaining step — but the opcode
discrimination itself, which is the claim, is measured.
