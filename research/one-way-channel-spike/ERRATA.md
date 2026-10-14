# ERRATA — what running this rig against real consensus actually caught

Every entry below was a **bug in this rig**, found only because the asserted
claim was checked against Bitcoin Core's consensus instead of against our own
code. They are kept because each one is a trap a contributor re-implementing a
one-way channel will hit, and because they are the evidence that the GREEN
result is a test result and not a self-report.

If this rig had used a *simulator* for these legs, all four would have passed
silently and the write-up would have contained four false claims.

---

## E1 — DER SEQUENCE length double-counted the tag bytes

**Symptom:** `mempool-script-verify-flag-failed (Non-canonical DER signature)`
for every spend, including ones whose ECDSA arithmetic was correct.

**Cause:** `_der_int()` returns a complete TLV (`02 <len> <value>`). The
signature wrapper added the two 0x02 tag+length bytes *again*:

```python
# WRONG
return b"\x30" + bytes([len(_der_int(r)) + len(_der_int(s)) + 2]) + _der_int(r) + _der_int(s)
```

That emits `30 46` wrapping 68 bytes of content — a malformed SEQUENCE.

**Why it cost a debugging round:** `CHECKSIG` fails while *parsing*, so the
error is reported as a signature failure. It reads exactly like a wrong key or a
wrong sighash. A structural DER check over 400 signatures (all 400 malformed)
localised it in one command.

**Fix:** `body = _der_int(r) + _der_int(s); return b"\x30" + bytes([len(body)]) + body`

**Lesson:** before blaming the sighash, validate DER *structure* independently.

---

## E2 — BIP68 relative-locktime TYPE FLAG silently multiplies the delay by 512

**Symptom:** the refund tx was rejected `non-BIP68-final` and **never became
final**, however many blocks were mined.

**Cause:** `nSequence` was encoded as `blocks | SEQUENCE_LOCKTIME_TYPE_FLAG`
(bit 22). That bit selects **512-second units instead of blocks**, and BIP112
requires the flag to *match* between the script operand and the input's
`nSequence`. A script that pushes plain `6` therefore needs `nSequence == 6`,
flag **clear**. With the flag set, the input demanded `6 × 512 s ≈ 51 minutes`
of MTP — unsatisfiable on a regtest chain that mines instantly.

**Fix:** block-based CSV is `blocks`, with no flag.

**Lesson:** CSV has two units and its failure mode is "never final", not an
error. Quantified in the memo (A3-5).

---

## E3 — CHECKMULTISIG witness stack order, and the NULLDUMMY shape

**Symptom:** `Signature must be zero for failed CHECK(MULTI)SIG operation`.

**Cause:** two independent mistakes.

1. The dummy element was a **1-byte `b"\x00"`**. `SCRIPT_VERIFY_NULLDUMMY`
   (consensus for SegWit) requires it to be **empty**.
2. The witness order was `[payerSig, payeeSig, dummy]`. The stack that
   `OP_2 <payerPub> <payeePub> OP_2 OP_CHECKMULTISIG` consumes, bottom→top, is
   **`dummy, payerSig, payeeSig`** — `CHECKMULTISIG` pops `n`, then `n` keys,
   then `m`, then `m` sigs, then **one extra item below the signatures**.

**Fix:** `witness = [b"", payerSig, payeeSig, b"", ws]` — leading empty dummy,
trailing empty item as the falsy `OP_IF` selector.

**Lesson:** the error text names a *signature* problem for what is a *stack
layout* problem. Print the full witness, don't just read the reject reason.

---

## E4 — non-minimal number pushes

**Symptom:** `Data push larger than necessary`.

**Cause:** constants 10 and 2 were pushed as `01 0a` / `01 02`. Numbers in
`[-1, 16]` must use `OP_0` / `OP_1NEGATE` / `OP_1..OP_16`.

**Fix:** `pushnum()` in `btclib.py`.

**Lesson:** `SCRIPT_VERIFY_MINIMALDATA` is the single most common way a
hand-rolled script fails, and it fires *before* any signature check.

---

## E5 — (not a bug; a wrong claim in the first draft of the memo)

The first draft of T5 printed that the payer must "reach the network **in
time**" to use its refund, implying `OP_CSV` is a deadline.

**It is a minimum delay, not a deadline.** Once the input ages, the refund
stays spendable indefinitely — a payer returning after a year can still refund.
The rig now asserts and prints this (T5). What *does* expire is the **fee
budget** the pre-signed tx commits to (T6). The real race is the fee
environment, not a clock. This was caught because the rig's own output
contradicted the prose around it.

---

## E6 — (not a bug; a finding)

`signrawtransactionwithkey` **refuses** to co-sign a channel state:

```
Unable to sign input, invalid stack size (possibly missing key)
```

for the P2WSH 2-of-2-locked branch. Core's signer will not partially satisfy a
multisig it cannot complete. This is recorded in T8 as a load-bearing
integration cost: **`bitcoind` cannot be the payer's channel wallet.** A payer
signing channel states needs software that signs arbitrary BIP143 sighashes.
The Cashu path has no analogous requirement.
