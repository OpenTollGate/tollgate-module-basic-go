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

## Status of the headline BIP-342 claim

**Still REASONED, not MEASURED — but now for a trivially removable reason.**
The witness bug that blocked the ruling is identified and fixed. What remains is
mechanical: rebuild both leaves with the 33-byte control block, import both keys
so Core can sign, and submit. That is one focused run, not discovery.

Independent cross-check obtained in passing: Core's own `multi_a(2,a,b)` collapses
to the `OP_CHECKSIGADD` family (it rejects `multi()` for tapscript), which is
consistent with BIP-342 disabling `OP_CHECKMULTISIG` — but that is a *construction*
argument from Core's descriptor language, **not** an execution ruling, and is
recorded as REASONED. The execution ruling requires the run above.
