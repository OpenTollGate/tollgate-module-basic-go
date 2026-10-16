# LNURLcash (LUD-25 / LUD-26) codec POC — what is proven, and what is NOT

A dependency-free, offline Python implementation of the **LNURLcash encoding and
verification layer**, validated against the specification's own official test
vectors. Part of the spike on using LNURLcash with TollGate.

```bash
bash run-poc.sh          # everything; writes out/ENV.txt out/RED.txt out/GREEN.txt
python3 tests/test_vectors.py        # LUD-25 + LUD-26 official vectors
python3 tests/test_bip342.py         # the BIP-342 demonstration
```

`python3 -S tests/test_vectors.py` also passes (S = site-packages removed), which
is the mechanical proof that nothing here needs PyPI.

## Result

| suite | assertions | PASS | FAIL |
|---|---|---|---|
| `tests/test_vectors.py` | 141 | **141** | 0 |
| `tests/test_bip342.py` | 13 | **13** | 0 |

Every LUD-25 vector reproduces byte-for-byte: the key-path `SigMsg` (**all 11
listed fields**) and its sighash, the `ck1`, the canonical `spend_tx`, both `cs1`
certificates, and the bearer note with **both** short forms, its full `cw1`
(192 chars) and its certificate. LUD-26's derivation and registration vectors
also reproduce, which independently confirms the `sk`/`Q` LUD-25 signs with.

## Files

| file | role |
|---|---|
| `lnurlcash/bech32m.py` | BIP-350 bech32m with the 90-char limit **removed** (LUD-25 "Encoding") |
| `lnurlcash/secp.py` | secp256k1, BIP-340 `lift_x`/`tagged_hash`/Schnorr, BIP-341 `TapTweak`/`tapleaf_hash` |
| `lnurlcash/bctx.py` | tx serialization + BIP-341 `SigMsg` and BIP-342 `ext` (ext_flag=1) |
| `lnurlcash/note.py` | `cp1`/`ck1`/`cw1`/`cs1` codecs, bearer notes, short forms, canonical spend tx, verification |
| `lnurlcash/tapscript.py` | a BIP-342 tapscript interpreter (enough to *execute* a leaf) |
| `lnurlcash/bip32.py` | minimal BIP-32, for the LUD-26 cross-check |
| `tests/test_vectors.py` | the official-vector suite; `--broken` injects defects (RED) |
| `tests/test_bip342.py` | the BIP-342 finding; `--unpatched` removes the rule (RED) |
| `vectors/` | the four upstream files, unmodified (see "provenance" below) |
| `out/ENV.txt` `out/RED.txt` `out/GREEN.txt` | the captures |
| `ERRATA.md` | **read this first** — 7 things running the vectors caught |

## What is PROVEN

* **The LUD-25 vectors all pass**, in a re-implementation written from the text
  alone, at the byte level: `SigMsg` field by field, sighash, `ck1`, `spend_tx`,
  both `cs1` (including RFC6979 determinism and the recovery id moved to the
  end), the bearer `h`/`leaf`/`tapleaf_hash`/`tweak`/`Q`/`control_block`/`cw1`,
  and both short forms.
* **The canonical spend transaction is exactly as tabulated** — nVersion 2, the
  `tagged_hash("LNURLcash/mint", domain)` prevout *un-reversed*, empty scriptSig,
  vout[0] value 0 with an empty scriptPubKey, claimed nLockTime/nSequence, spent
  output `OP_1 <Q>` (see ERRATA E4 for why "un-reversed" is a finding).
* **A key-path spend verifies and is domain-bound**: the same `ck1` fails at any
  other domain, as LUD-25 requires.
* **A script-path (`cw1`) spend verifies**, and `Q` is recomputed from the
  control block and compared against the vector. A wrong preimage does not spend.
* **The bearer `cw1` is exactly 192 characters**, and both short forms rebuild
  exactly the same `cw1` and `Q` from 64 hex characters (LUD-25 "Short forms").
* **LUD-25's timelock policy is enforced as written**: a block-height `nLockTime`
  is refused, a block-count `nSequence` is refused, a future locktime is refused,
  and an unelapsed BIP-68 time lock is refused.
* **LUD-25's SERVICE-side rejections work**: `cp1` whose `Q` is not a curve-point
  x, `ck1` whose payload is not 96 bytes, `cw1` with a length prefix that does not
  consume the payload, and a leaf carrying `OP_SUCCESSx` outside pushed data —
  which consensus would accept unconditionally and LUD-25 requires SERVICE to
  reject anyway.
* **BIP-342 disables `OP_CHECKMULTISIG` in tapscript** — demonstrated by
  *executing* both leaves, not by reading the BIP: the as-specified Spilman leaf
  (**109 bytes**) is rejected with the disabled-opcode error while the
  `OP_CHECKSIGADD` form (**110 bytes**) executes and its 2-of-2 threshold is
  real (1-of-2 fails). The `--unpatched` RED run removes the rule and the invalid
  leaf then passes, which is the finding reproduced on demand. BIP-342's own
  translation is quoted at `bip-0342.mediawiki:76`, and the disable rule at
  `:38`/`:72`.
* **The prior spike's LUD-25 claims check out**: (a) ✔ CHECKMULTISIG is disabled;
  (b) ✔ time-type relative locks only, block heights/counts refused; (c) ✔ `cw1`
  is 192 chars; (d) ✔ the timelock enforcement is SERVICE-asserted policy, not
  consensus — see "what is NOT proven".

## What is NOT PROVEN — read this before quoting any of it

1. **No consensus oracle was run.** There is no `bitcoind`, no `bitcoin-cli` and
   no `libbitcoinkernel*` on this host (recorded in `out/ENV.txt`). The vectors
   *claim* every spend was accepted by `libbitcoinkernel`; we reproduce the
   vectors, we did not reproduce the kernel. **The verification is validated
   against the official vectors and against the BIP-341/342/BIP-68/BIP-112 text,
   not against consensus.** `lnurlcash/tapscript.py` is a re-implementation of
   BIP-342 — an honest one, checked against the BIP's own step list and against
   the LUD-25 script-path vector — but it is not Bitcoin Core and a PASS in it is
   not a consensus assertion. This applies to the BIP-342 demonstration in
   particular.
2. **The BIP-342 result is therefore a two-source claim, not one.** The BIP text
   (`bip-0342.mediawiki:38` "The inefficient OP_CHECKMULTISIG and
   OP_CHECKMULTISIGVERIFY opcodes are disabled"; `:72` "The disabled opcodes
   behave in the same way as OP_RETURN, by failing and terminating the script
   immediately when executed") and this POC's interpreter agree. Two independent
   readings of a text is not the same evidence as one execution of the reference
   implementation. Closing it needs a host with `libbitcoinkernel`.
3. **The `cw1` timelock claim is custodial by construction**, and LUD-25 says so
   itself (its WARNING: "a timelock honored by SERVICE is SERVICE asserting its
   own clock… MUST NOT be presented as trustless"). `check_timelock_policy()`
   implements that policy; it proves nothing about consensus timelocks. The
   `nLockTime >= 500,000,000` and BIP-68-time-type rules are LUD-25's own
   additional constraints on top of what consensus would accept.
4. **LUD-25's mint/rotate/split/merge endpoint semantics, fees, and the LUD-26
   Lightning Address / internal-transfer rails are NOT implemented or tested.**
   This POC is the encoding and verification layer only — no HTTP, no LNURL
   request handling, no note bookkeeping, no value.
5. **`cx1`-based recovery scanning is not exercised** beyond deriving `cx1` and
   verifying the registration proof.
6. **Timing is not measured and no hardening is claimed.** Pure-Python secp256k1
   is slow by design choice (auditability over speed); the suites take tens of
   seconds. Nothing here is constant-time.
7. **The in-tree `vectors/` are copies, not the originals.** They are
   byte-identical to
   `raw.githubusercontent.com/lnurl/luds/lnurlcash/{25.md,26.md,25-vectors.json,26-vectors.json}`
   as fetched during this spike; `out/ENV.txt` records a SHA-256 prefix for each.
   If upstream changes them, these copies will not move on their own.

## On the RED capture

`out/RED.txt` is deliberately-failing. Three defects are injected into the codec
(a non-NUMS `H`; `OP_EQUALVERIFY` for `OP_EQUAL` in the bearer leaf; a
single-`sha256` `cs1` digest) and BIP-342's disabled-opcode rule is removed. The
RED run records **25 FAIL lines** across all sections and the BIP-342 leaf is
wrongly accepted. If a future change makes the RED run pass, the suite has
stopped testing anything and this POC should be distrusted.

The harness needed two fixes of its own to produce a RED capture worth reading
(ERRATA E7): a crash used to abort the run and hide 24 of the 25 failures, and
one injected defect raised instead of failing.
