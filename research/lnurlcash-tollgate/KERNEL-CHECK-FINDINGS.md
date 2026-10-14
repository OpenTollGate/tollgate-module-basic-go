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

## What is NOT established, and the precise blocker

**The BIP-342 headline claim has no consensus ruling.** Every script-path attempt
was rejected with `Witness program hash mismatch` — including attempts whose
commitment math we verified matches Core's own derivation. That is a
contradiction, and it means **something about the spend is still wrong in a way
not yet identified.**

Ruled out (each tested, not assumed):
- leaf bytes — Core parses them and `raw()` descriptors round-trip;
- tapleaf hash — matches an independent BIP-341 computation;
- tweak — matches an independent BIP-340 tagged-hash computation;
- output key — matches Core's `deriveaddresses` for `tr(NUMS, pk(02‖x))`;
- control block length — 65 B, correct for a single-leaf tree;
- witness item ORDER — three orderings all produce the identical error;
- transaction wire format — Core decodes it without complaint.

Not yet tested:
- a *valid* signature over the real sighash (all attempts used a 64-byte
  placeholder), and whether Core reports `Witness program hash mismatch`
  preferentially over a signature error;
- a control block built by Core itself for a descriptor it can sign, to diff
  against ours.

**Next step, precisely:** import a `tr(<internal>, <leaf>)` descriptor into the
wallet so Core holds the leaf key, spend via the script path with Core signing,
and diff Core's witness against ours byte-by-byte. That resolves the contradiction
directly instead of by inference. Everything else is in place — funded outputs,
verified hashes, working Node access — so this is witness-construction work, not
discovery.

## Why this is recorded as a partial

The honesty contract forbids claiming a measured result that was not observed, and
this programme's most valuable output has been its own errata. One earlier claim
in this very file was wrong and is retracted above. Three claims ARE measured and
are genuine improvements on the prior state; the headline claim is not, and the
contradiction is stated rather than smoothed over.
