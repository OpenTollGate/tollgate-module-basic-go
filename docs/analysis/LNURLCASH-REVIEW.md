# LNURLcash (LUD-25/LUD-26) — how it bears on the one-way-channel spike

**Status:** review notes, 2026-10-14. Source: primary. PR
<https://github.com/lnurl/luds/pull/301> (`lnurl:lnurlcash`, head of the
`lnurlcash` branch), files `25.md` (256 lines), `26.md`, `25-vectors.json`,
`26-vectors.json`. Author `dni`; co-authors `tbk (Thebora Kompanioni)`,
`thecryptodonkey`. Status `draft`, created 2026-08-06, last updated 2026-10-05,
11 comments, +547/-0 across 6 files. Companion projects: `lnurlcash/lnurl-wallet`
(`wallet.lnurlcash.com`), `lnurlcash/lnurl-mint` (`mint.lnurlcash.com`),
`lnurlcash/lnurl-vault` (`vault.lnurlcash.com`).

## 1. What LNURLcash actually is, in one paragraph

It is **not** a new payment protocol. It takes LUD-03's `k1` (a withdraw-link
secret that anyone holding it can redeem) and promotes it to a **bearer asset**:
a `k1` that a `SERVICE` has credited with value can be copied, printed,
NFC-tapped, handed over like a banknote, and **rotated, split or merged**. It
adds **no new endpoint** — a wallet that doesn't know LNURLcash sees an ordinary
withdraw link and cashes it out to a BOLT-11 invoice as usual. Minting only needs
a wallet able to attach a LUD-12 `comment`.

The load-bearing move: **every note is a BIP-341 taproot output key `Q`**, spent
by a witness Bitcoin's own script rules accept for `Q` — a hashlock for a plain
bearer secret, a signature for a key-owned note, or **any other tapscript
condition (timelocks, multi-party leaves)**. The service tracks outstanding notes
as `Q` → value and needs nothing secret to do so. Verification can be delegated
to **Bitcoin Core's script interpreter (`libbitcoinkernel`)**.

## 2. Why this matters for the spike — three direct hits

### 2.1 It already ships the "programmable bearer note" the spike was going to invent

The spike's premise is "a one-way channel as a non-custodial bearer instrument."
LUD-25's stated design goal is exactly a **programmable bearer note** whose spend
conditions are arbitrary tapscript — explicitly including **timelocks**
(`OP_CHECKLOCKTIMEVERIFY` / `OP_CHECKSEQUENCEVERIFY`) and **multi-party leaves**.
A one-way channel is a *timelock + multisig leaf*. So **LUD-25 is the general
container, and a one-way channel is one leaf script inside it.** The spike should
not propose a new instrument; it should ask whether a channel leaf can be
expressed *as* an LNURLcash note.

### 2.2 It removes the blockchain-blindness problem for the payer — but only partly, and it says so itself

The spike's hardest question was "can the payer hold no chain state?" LUD-25
answers a large piece of it with **`cs1` offline verification**:

- The service certifies a note by publishing `mintPubkey` (33-byte compressed
  secp256k1, should be its BOLT-11 node id).
- A `cs1` is a **recoverable ECDSA signature** (RFC 6979, LUD-13-style) over
  `"LNURLcash:" || amount_msat || ":" || hex(Q)`.
- A recipient **offline** derives `Q` from the `k1`, checks the spend opens `Q`
  against the canonical spend transaction for that domain, and checks the `cs1`
  against the `mintPubkey`.

That is a **no-network, no-chain verification of issuance.** This is a genuine
answer to the "chain-blind payer" half of the spike.

**But it is carefully bounded, and the spec's own warning is the crux:**

> A certificate proves **issuance, not that the note is still outstanding**: the
> previous holder still knows the spend. A new holder SHOULD **rotate (or merge)
> the note as soon as it is online**.

So offline verification is **issuance-only**, and **non-double-spend requires
going online.** The previous holder can replay the same spend. This is precisely
the double-spend window a one-way channel's on-chain enforcement closes — and
**that is the spike's real increment, precisely located.**

### 2.3 The custody confession is explicit and should be quoted in the spike

On timelocks, LUD-25 states:

> A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a
> **custodial policy, not a consensus guarantee, and MUST NOT be presented as
> trustless.**

This is the single most important sentence in the spec for our purposes. LNURLcash
timelocks are **advisory** — the service checks the claim against its own clock
rather than the chain enforcing it. So a one-way channel implemented as a plain
LNURLcash timelock leaf is **custodial in exactly the dimension the spike is
about**. A *real* one-way channel would need the timelock enforced by
**consensus** (an actual on-chain spend), which is not what LNURLcash does.

**This is the spike's sharpest possible framing:** LNURLcash gives you the
programmable bearer note, the offline issuance proof, and the rotate/split/merge
algebra — but its enforcement is custodial-by-design. The spike's question becomes:

> Can a channel leaf be added to the LNURLcash container such that the
> double-spend window and the timelock are enforced by **Bitcoin consensus**
> rather than by `SERVICE`'s clock — and does the resulting instrument beat
> today's `SERVICE`-adjudicated one for a TollGate?

### 2.4 It independently validates our SegWit/taproot reasoning

Our rig independently concluded SegWit was structurally required (txid
malleability). LUD-25 arrives at **P2TR/taproot** for the same family of reasons
and adds a constraint we should adopt: `SERVICE` MUST reject any leaf containing
`OP_SUCCESSx` outside pushed data, and MUST NOT use public keys that are neither
empty nor 32 bytes in a signature check (BIP-342 upgradable key types let any
signature pass). Both are real footguns our rig's script builder should assert.

## 3. Where LNURLcash and the spike *diverge* (do not conflate)

| Axis | LNURLcash (LUD-25) | One-way channel (spike) |
|---|---|---|
| Adjudicator | `SERVICE` (its own DB + clock) | **Bitcoin consensus** |
| Double-spend | closed online by **rotate** | closed by **2-of-2 + CSV** |
| Timelock | **custodial policy**, explicitly not trustless | consensus-enforced |
| Offline verify | **yes** — `cs1` proves issuance | proves issuance; refund is chain-checked |
| Needs chain code | **no** — delegates to `libbitcoinkernel` | **yes** — real chain code |
| Custody | service is the authority | **no custodian** (that's the point) |
| Algebra | rotate / split / merge (specified) | none specified |

## 4. The bridge — how the two combine (Worth testing)

The most interesting construction available is a **hybrid**, and it falls out of
the two specs almost mechanically:

1. Customer mints an LNURLcash note at `SERVICE` — `cs1` certifies issuance.
2. Offline, the customer verifies the note against `mintPubkey` (**no network**).
3. The customer hands the note to the TollGate, which verifies it offline too.
4. **Instead of** the TollGate rotating online (double-spend window open until
   then), the hand-over is bound by a **channel leaf**: a `cw1` whose script is
   `OP_IF <expiry> CSV DROP <payerPub> CHECKSIG OP_ELSE 2 <payerPub> <payeePub> 2
   CHECKMULTISIG OP_ENDIF`.
5. The TollGate opens the gate **on the offline proof** (removing the 30-second
   `Receive` block that T3 identified as the real target), and settles later when
   it has WAN.

So LNURLcash supplies **existence** (`cs1`) and the container; the channel
supplies **consumption** (consensus-enforced non-double-spend) and the
**deferral**. That maps almost exactly onto
`docs/architecture/bearer-instrument-port.md`'s two rules — existence from the
signing log, consumption from the authority — with the chain replacing the
authority for consumption. **This is the construction the spike should specify.**

## 5. Open questions worth putting in the report

1. **Is `cs1` sufficient for a TollGate to accept value with no WAN?** The
   spec says rotate-as-soon-as-online is *SHOULD*, and that a certificate does
   not prove the note is outstanding. A TollGate that opens a gate on `cs1`
   alone is accepting a **replayable** instrument. Does the channel leaf close
   that, and is the close consensus-enforced or service-asserted?
2. **Does `mintPubkey` = the LN node id create a trust coupling?** The spec
   *should*s this. If the mint's node id is also the certificate key, then a
   compromised LN node key forges certificates.
3. **`ck1` signature scope depends on `domain`, not value or time** — "one
   signature per SERVICE serves every redemption and offline check". Does that
   mean a `ck1` for one note is reproducible across notes? The spec says wallets
   SHOULD use all-zero `aux_rand` so `ck1` is *reproducible from `sk` and the
   domain alone*. That is a deliberate **replayability/correlation property**
   worth stress-testing: reproducible signatures are also **linkable**.
4. **Timelock `nSequence` is relative to when `SERVICE` credited the note** (by
   mint, rotate, split or merge) — not to an absolute chain height. A channel's
   CSV is anchored to the funding tx. Reconciling "service-relative" and
   "chain-absolute" time is a real design problem for the hybrid.
5. **`nLockTime` must be Unix time ≥ 500,000,000 and relative locks MUST carry
   BIP-68's time-type flag; block heights and block counts MUST be refused.**
   Note this is the *opposite* of what our rig found — our rig needed the
   type flag **clear** for block-based CSV. LUD-25 mandates **time-based only**.
   That is a direct conflict to resolve, and our rig's BIP68 bug is the reason we
   know it matters.
6. **Is LUD-25 stabilized enough to depend on?** Status `draft`, 11 comments,
   last updated 2026-10-05, author asking for review and "are you guys still
   interested in the spec?". Depending on it in a TollGate PR is a bet on an
   unfinished document.

## 6. Feedback for the LNURLcash authors (draft, for the operator to send)

Substantive points, in order of weight:

1. **The `cs1` issuance-vs-outstanding gap is the whole game for a
   physical/point-of-sale use case** (which is what a TollGate is). "New holder
   SHOULD rotate as soon as it is online" means the accepted-but-unrotated window
   is a **replay window**, and the warning treats it as an operational detail. For
   a merchant that wants to open a gate immediately, it is *the* security
   property. Would the spec consider a normative note on acceptable exposure, or a
   `cs1` variant that binds the note to a *single* consumption (a one-time
   certificate)?
2. **The timelock nondisclosure is correct and important** — but it deserves to be
   more prominent. Right now it sits in a `[!WARNING]`. A reader could easily
   implement a "trustless escrow" on top of LUD-25 timelocks without noticing the
   service is the enforcer.
3. **`ck1` reproducibility vs linkability.** Making signatures reproducible from
   `sk` + `domain` alone is good for recovery and bad for privacy. Worth an
   explicit Privacy Considerations paragraph stating the tradeoff and whether a
   `ck1` is intended to be correlatable across redemptions at one service.
4. **`mintPubkey` SHOULD be the BOLT-11 node id.** If it *should* be, say what
   breaks when it isn't, and consider whether certificate-key and node-key should
   be separable — a node migration would otherwise invalidate every certificate.
5. **BIP-68 direction conflict.** LUD-25 mandates the *time-type* flag for
   relative locks and refuses block heights/counts. Many implementations reach
   for block-based CSV first (we did, and got the flag wrong). A one-line
   rationale for time-only would help implementers not repeat our bug.
6. **Interop praise worth stating**: "adds no new endpoint; a wallet that does not
   know LNURLcash sees an ordinary withdraw link" is an excellent design
   property. Delegating to `libbitcoinkernel` rather than reimplementing script
   verification is the single best decision in the document — it means the
   verification rule is the consensus rule, and a reimplementation cannot drift
   from it.
