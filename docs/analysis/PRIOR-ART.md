# Prior art: one-way (unidirectional) payment channels

**Consultant deliverable for the TollGate cold-start spike.** Scope: the exact construction of a
one-way channel, its trust assumptions, and what the *payer* must know about the chain. Answering
(and partly disproving) the brief's question:

> Can a one-way payment channel be built so a payer with **no internet gateway** and **no
> custodian** can buy internet time from a TollGate router?

Method note: every factual claim below is cited to a URL retrieved during this review. Claims I
inferred from sources are marked **[inferred]**. Claims I could not verify are collected in §6 and
marked `[unverified]` inline. Citation numbers map to the `Sources` block at the end.

---

## 0. Bottom line

1. **The construction exists, is 13 years old, and has been built for exactly TollGate's use case.**
   Spilman's one-way channel was posted to bitcoin-development on 2013-04-19 [1]; Hearn & Spilman
   wrote it up canonically on the Bitcoin wiki [4]; and an ETH thesis supervised by Christian Decker
   shipped a working proof-of-concept of *"an access point provid[ing] Internet access to untrusted
   users in exchange for payment in bitcoins, using a micropayment channel"* in December 2013 [18].

2. **One-way channels are the one channel family with a genuinely non-interactive payment phase.**
   Each payment is a single signature sent payer → payee; the payee needs no round trip, and the
   payee's security does not depend on the payer being reachable [4][5]. This is unlike Lightning,
   whose per-update interactivity comes from the *revocation-secret* mechanism, not from the channel
   concept [6][10].

3. **"Blockchain-blind payer" is achievable for the payment phase only — not for the channel's
   lifetime.** The payer must (a) already hold an on-chain UTXO, and (b) be able to broadcast exactly
   two transactions: the funding tx and, on failure, a pre-signed refund tx. If the payer *never*
   regains any chain gateway, its unspent deposit is **stranded, not stolen** — and a stranded
   deposit is a donation, not a channel. This is the disproof clause (§4).

4. **The canonical wiki version's double-spend defence is the part that no longer works.** The
   Bitcoin wiki describes defeating the payer's double-spend via nSequence-based mempool
   replacement [4], while the *same page* warns that mechanism "was disabled in 2010, and more
   recently the code has been removed completely" [4]. Do not build on it.

5. **Prior claim about a Decker-supervised master's thesis — directionally right, factually wrong.**
   Decker **did** supervise a thesis on precisely this construction, but it is a **semester** thesis
   (SA-2013-49, Siby, Dec 2013), by **Sandra Siby**, and it *applies* the Hearn–Spilman construction
   rather than analysing one-way channels [18][20]. It is not a master's thesis, and Burchert's work
   under Decker is duplex/tree funding, not one-way [15][16]. Details and evidence in §5b.

---

## 1. The construction of a one-way channel, step by step

### 1a. The original (Spilman 2013) — nLockTime + transaction replacement

Spilman's post proposes the sequence below. Quoted step numbering is his; the verbs are his [1].

| # | Step | Who acts |
|---|---|---|
| 0 | "User and AP negotiate how much to escrow, who pays the fees, and how far in the future nLockTime will be set" | both, off-chain |
| 1 | "User creates an unsigned TX1 with 1 or more inputs from user's 'listunspent' … and a single output of 'FundAmount' with scriptPubKey of '2 PK1 OP_0 CHECKMULTISIG', and sends to the AP" | payer |
| 2 | "AP adds to TX1; their inputs (if any), their change (if any), replaces OP_0 in the scriptPubKey with a PK they control, and signs SIGHASH_ALL, and returns TX1 to User" | payee |
| 3 | "User verifies TX1 is as agreed, and signs it SIGHASH_ALL, but keeps it to himself. User, having completed TX1, knows its TxID and can now create TX2-Locked spending TX1 back to themselves. User sets nLockTime to the agreed point, signs SIGHASH_ALL, and sends TX2-Locked to AP" | payer |
| 4 | "AP verifies TX2-Locked and adds their signature, and returns TX2-Locked to User. User can now broadcast TX1 and TX2-Locked" | payee → payer |
| 5 | "At each payment milestone, user creates TX2-Final; TX1 as input, final nSequence, no lock time, with a single output going back to the user, and an amount equal to the remaining balance of the channel. User signs with SIGHASH_SINGLE and sends to the AP" | payer |
| 6 | "AP can add an output to TX2-Final sending their portion of the coins where ever they want, sign it SIGHASH_ALL, and broadcast it at any point, closing the channel" | payee |

What each party must **hold**:

- **Payer**: its own private key(s) for the funding inputs; the payee's pubkey `PK2`; the fully
  co-signed `TX1`; the fully co-signed `TX2-Locked`; and, after each milestone, the ability to
  re-sign a new `TX2-Final` [1].
- **Payee**: the *latest* `TX2-Final` (a payer signature plus amounts); the funding tx / its outpoint;
  `TX2-Locked`; and the ability to sign the final spend [1].

What each party must **know**:

- **Payer**: the outpoints and values of its own coins (its own wallet's chain view — see §4), the
  agreed `nLockTime`, and the fact that `TX1` was broadcast.
- **Payee**: the funding outpoint, the `TX2-Locked` locktime deadline, and — critically — *when the
  deadline is approaching*, because the payee must land its `TX2-Final` before `TX2-Locked` matures
  [1][4].

**Spilman's own trusted-third-party fallback.** He names the alternative explicitly: *"The
alternative to the TX2-Locked is a 3rd party in the MULTISIG who is trusted to close the channel at
the request of either party, based on the latest TX2-Final which was sent by the user. In this case
there is no TX2-Locked, only a single boardcast version of TX2-Final, and you do not need
transaction replacement at all"* [1]. That is exactly the watchtower / always-online-cosigner
assumption, and he frames it as the way to *avoid* relying on replacement.

### 1b. The canonical wiki version (Hearn & Spilman), Example 7

Written "from the client's perspective"; client = payer, server = payee (the AP in their worked
example is a café WiFi hotspot) [4]:

1. Create K1; request K2 from the server.
2. Create and sign, **but do not broadcast**, T1 paying (e.g.) 10 BTC to an output requiring both
   the server's key and one of the client's — "A good way to do this is use OP_CHECKMULTISIG."
3. Create refund tx T2 spending T1 back to the client, time-locked (e.g. a few hours), **unsigned**,
   and send it to the server. Output script "2 K1 K2 2 CHECKMULTISIG".
4. "The server signs T2 using its private key associated with K2 and returns the signature to the
   client. Note that it has not seen T1 at this point, just the hash (which is in the unsigned T2)."
5. "The client verifies the servers signature is correct and aborts if not."
6. "The client signs T1 and passes the signature to the server, which now broadcasts the
   transaction… This locks in the money."
7. The client creates T3 (non-timelocked), initially allocating everything to K1, signs it, sends it
   to the server.
8. "When the client wishes to pay the server, it adjusts its copy of T3 to allocate more value to the
   server's output and less to its own. It then re-signs the new T3 and sends the signature to the
   server. It does not have to send the whole transaction, just the signature and the amount to
   increment by is sufficient."
9. "This continues until the session ends… The AP then signs and broadcasts the last transaction it
   saw, allocating the final amount to itself."

The refund is required "to handle the case where the server disappears or halts at any point, leaving
the allocated value in limbo" [4] — and step 9's trigger is stated explicitly: "This continues until
the session ends, **or the 1-day period is getting close to expiry**" [4]. The wiki states this
protocol "has been implemented in bitcoinj" [4], and bitcoinj's own docs confirm the deployed shape:
the refund is time-locked (their default: "currently, one day"), the client gets a fully-signed refund
*before* the contract is broadcast, and "once the payment channel is established, a micropayment can
be made with just one signature operation by the client and one verify by the server" [5].

**Attribution caveat (verified).** The wiki page's own byline is single-author: "These pages were
written by Mike Hearn" [4], and the string "Spilman" **does not appear anywhere on the page** (I
searched the retrieved HTML: 0 occurrences). The community nonetheless credits both: the eltoo paper
refers to "the simple micropayment channels by Hearn and Spilman [6]" [10], and Spilman's own
mailing-list post is the primary source for the protocol [1]. Treat [4] as *Hearn's wiki writeup of
the construction Spilman posted*, which is exactly how [1] → [4] → [5] (bitcoinj) forms an unbroken
lineage.

**Debian-grade caveat from the wiki itself**: "Starting around 2013 time-locked transactions were
made non standard and no longer enter the memory pool, thus cannot be broadcast before the timelock
expires" [4]. See §2 for why this degrades the stated double-spend defence.

### 1c. The OP_CSV / relative-timelock refinement (Poon–Dryja, BIP 112)

The brief asks specifically for the `OP_CSV` construction. Precisely what the Lightning paper
contributes: it introduces *relative* maturity to the channel toolkit. The paper proposes "Sequence
Numbers can be enforcible via a relative block maturity of the parent transaction via a soft-fork",
plus "an additional opcode, OP CHECKSEQUENCEVERIFY (a.k.a. OP RELATIVECHECKLOCKTIMEVERIFY)", and
notes this "would permit further abilities, including allowing a stop-gap solution before a more
permanent solution for resolving transaction malleability" [6]. The paper's own framing of the
resulting structure:

> "A Revocable Transaction spends from a unique output where the transaction has a unique type of
> output script. This parent's output has two redemption paths where the first can be redeemed
> immediately, and the second can only be redeemed if the child has a minimum number of
> confirmations between transactions." [6]

`OP_CSV`/BIP 112 was subsequently deployed: BIP 112's header reads `Status: Deployed` [7], and it
specifies that the opcode plus BIP 68 "allows execution pathways of a script to be restricted based
on the age of the output being spent", explicitly motivated by "phased protocols such as escrow,
**payment channels**, or bidirectional pegs" [7, emphasis added].

**What this changes for a one-way channel.** It replaces the *absolute* `nLockTime` deadline of
§1a with a *relative* delay measured from confirmation of the parent. This matters in two ways
**[inferred from [6][7]]**:

- The payer no longer has to learn "what time is it on the chain" to build a correct refund; the
  script itself expresses "at least N blocks after funding".
- It removes the reliance on nSequence *replacement* (§1a step 5 relied on `SIGHASH_SINGLE` + sequence
  semantics): a relative-timelock refund is enforced by *script*, so no mempool-replacement behaviour
  is needed for the refund path.

Note the brief's framing — "`OP_CSV`/relative timelocks, which is precisely what makes
**payer-offline** one-way channels possible" — is **too strong as stated**. Payer-offline payment is
already achieved by Hearn–Spilman's pre-signed T3/`TX2-Final` with absolute `nLockTime` [4][5], and
bitcoinj shipped it in 2013 [5][18]. `OP_CSV` makes the *timeout* logic clean and
consensus-enforced rather than mempool-dependent; it is not the thing that makes the payer offline.
`[unverified]`: I found no source asserting the stronger claim.

### 1d. Duplex channels (Decker–Wattenhofer) — the correct place for the one-way primitive

The 2015 duplex paper treats the one-way channel as a *component*. Its own words: "such a
micropayment channel is intrinsically unidirectional, i.e., the amount that the receiver is assigned
in update transactions must be strictly increasing, otherwise the receiver might release an earlier
update, which pays out a higher amount" [8]. And the structural limitation: "Each channel is
unidirectional and is limited by the amount of bitcoins locked in during the setup by the sender.
Once the limit has been consumed, the channel has to be torn down and a new one created" [8].

So the field's own lineage is: one-way simple channel (Hearn–Spilman) → duplex = a *pair* of
one-way channels wrapped in an invalidation tree so the pair can be reset [8]. The duplex
construction does not replace the one-way primitive; it consumes it.

### 1e. Later constructions that relax the *payee/redeemer* side

- **eltoo** (Decker, Russell, Osuntokun) replaces penalty-based invalidation with state numbers +
  `OP_CLTV`/`OP_CSV`, and requires a `SIGHASH_NOINPUT`-style flag so transactions can be rebound [10].
  Its security statement is explicit and relevant: safety "depends on two key assumptions: • The
  victim can detect an attack in time to react to it, either by actively participating in the
  network, or by outsourcing the reaction to a third party; • The later update transaction can be
  confirmed in the specified time to doublespend the outdated update", and "if a user is offline for
  a prolonged period it may chose a higher timeout" [10]. The flag is now BIP 118
  `SIGHASH_ANYPREVOUT`, authored by Decker and Towns, and its status is **Draft** [11] — i.e. not
  available on Bitcoin today.
- **Ark** moves in the opposite direction: it lets a *receiver* "join without performing any onchain
  transaction" and receive "offchain payments without locking any funds beforehand", with "minimal
  online requirements: only the operator must remain continuously online, while users need to connect
  periodically to refresh their VTXOs" [12]. Original concept by Burak [13]. Two cautions: it introduces an always-online operator (a liveness
 dependency, though not a custody one — per its security guarantees "any [VTXO] held by an honest
 user can be unilaterally redeemed onchain", and "it is only necessary that each user stores its own
 local state to guarantee its capability to exit unilaterally"), and its fast-finality mode
 additionally needs an existentially-honest signer committee: "under rational operator and 1-of-𝑛
 honest committee" [12]. Most interesting for the brief: Ark "enables offchain transactions of
 virtual UTXOs (VTXOs), through an untrusted operator", and "users can receive offchain payments
 without locking any funds beforehand and Ark state updates can be performed only requiring the
 users involved in that update" [12]. That last property — **per-update party count below the full
 participant set** — is the same axis the brief probes. **[unverified]** whether the 2023 design [13]
 has it verbatim: the 2026 paper says the original concept "required covenants" and was modified [12].
- **Brick** attacks channel *asynchrony* directly: it is "the first incentive-compatible off-chain
  construction that operates securely with offline channel participants under full asynchrony" [14].
  Note the diagnosis it gives of the prior art: "both solutions require online participants that
  frequently monitor the blockchain to ensure safety" [14]. That sentence is about Lightning and
  duplex — and it is a diagnosis that **does not apply to the one-way channel's payment phase**.

---

## 2. The security argument

### 2a. Why the payee is safe

The payee's safety is a **monotonicity** argument, and the papers state it in those terms:

- "The receiver is incentivized to only use the latest update as it is the one paying out the maximum
  amount" [8].
- eltoo's summary of the deployed mechanism: "The simple micropayment channel supports incremental
  transfer of value in only one direction, from a sender to a recipient. It uses partially signed
  transactions that can be completed only by the recipient, which will only ever enforce the latest
  state since it is the state that maximizes its payout" [10].

Because the payee's payout is strictly increasing and the payee alone can complete the spend by
adding its own signature, the payee needs nobody's cooperation at close time [4][8][10]. Stale
updates are simply discarded; there is no "toxic information" whose leak causes loss, because the
payee never holds anything it must keep secret from the payer [10].

### 2b. Why the payer cannot double-spend — and where that defence actually lives

Three distinct defence layers are conflated in the literature. Separating them matters for TollGate.

**(i) The structural defence (still valid).** The funding output is a 2-of-2 multisig. The payer
*cannot* unilaterally spend it. The only unilateral spend the payer can construct is the
time-locked refund, which is invalid until its timeout [4][8]. Therefore the payer's only "attack"
is to *wait* — and Peter Todd's thread settles what stops that: "The seller won't hand over the
goods of course until they have a valid transaction signed by the buyer sending them the escrowed
funds" [3]. Put differently: the payee's rule is **do not deliver service before you hold a
payer-signed paying transaction**. The ETH WiFi thesis records this being verified in practice: "we
analyzed the log files and confirmed that the micropayments are started by the client only after the
server has signed the refund transaction" [18].

**(ii) The replacement defence (broken).** The wiki describes defeating the payer's double-spend
using locktime and sequence: "The lock time and sequence numbers avoid an attack in which the AP
provides connectivity, and then the user double-spends the output back to themselves using the first
version of TX2, thus preventing the cafe from claiming the bill. If the user does try this, the TX
won't be included right away, giving the access point a window of time in which it can observe the TX
broadcast, and then broadcast the last version it saw, overriding the user's attempted double-spend"
[4]. **But the same page opens with a warning that this mechanism is gone**: "In places this page
refers to the ability to use the nSequence field for transaction mempool replacement. This mechanism
was disabled in 2010, and more recently the code has been removed completely, due to concerns over
people using it to perform DoS attacks" [4]. **[inferred]** Therefore the wiki's stated
double-spend-override defence is not available on today's network, and Spilman hit it immediately:
within a day he reported the closing transaction "was not being accepted into the mempool beacuse
'ERROR: CTxMemPool::accept() : inputs already spent'" [2].

**(iii) The bribe defence (never existed).** Spilman's own acknowledgment of the residual attack:
"AP must broadcast TX2-Final before nLockTime, but has no guarantee the user hasn't offered a bribe
for miners by spending TX2-Locked with a large fee, e.g., a pissed off user spends TX2-Locked
entirely to fees just to see if they can convince miners to wait for it" [1]. The same miner-bribe
weakness is later stated as the general reason the original nSequence-replacement design failed:
"this mechanism was flawed since a rational miner will always prefer transactions with a higher
expected payout, even though they may have a lower sequence number. An attacker could incentivize
miners to confirm a specific version by adding fees either publicly or by directly bribing the
miners" [10].

**Net payee-safety condition** (this is the load-bearing sentence for TollGate): the payee is safe
**iff it holds a payer-signed transaction paying it, and it lands that transaction on-chain before
the payer's refund path becomes spendable**. The payee's residual risks are therefore (a) a timeout
race, and (b) miner bribery during that race — not a silent theft by the payer.

### 2c. Why the payer is safe

The payer's protection is the pre-signed, time-locked refund, obtained *before* the funding tx is
broadcast [4][5]. bitcoinj states the ordering rationale: "The refund transaction is prepared in
such a way that the client gets a fully signed copy before the initial multi-signature transaction
(the contract) is sent to the server. In this way, we avoid a potential crash/attack that could
cause the client to lose money — once the client receives the refund transaction, only then is the
money locked to both parties control. If the server halts at any point in the protocol, the client
can always get their money back" [5].

Two qualifications:

- **The refund protects against theft, not against stranding.** It only pays out if the *payer* sends
  it [4][5]. See §4.
- **Timeout selection is a real trade-off, not a parameter to guess.** Lightning's own risk section:
  "Participants must choose timelocks with sufficient amounts of time. If insufficient time is given,
  it is possible that timelocked transactions believed to be invalid will become valid, enabling coin
  theft by the counterparty. There is a trade-off between longer timelocks and the time-value of
  money" [6]. eltoo says the same about `OP_CSV` durations [10].

### 2d. What the payee needs *after* the session — and it is not free

The payee must resolve the timeout race, which needs either an always-available watcher or an
always-online counterparty. Lightning's paper names both mitigations, and its phrasing is
instructive: "If one does not broadcast a transaction at the correct time, the counterparty may steal
funds. This can be mitigated by having a designated third party to send funds. An output fee can be
added to create an incentive for this third party to watch the network. Further, this can also be
mitigated by implementing OP CHECKSEQUENCEVERIFY" [6]. (Note: the word "watchtower" does not appear
anywhere in the v0.5.9.2 paper — verified by full-text search; the *concept* appears as the
"designated third party".) Spilman proposed the same shape as the trusted-cosigner third party [1],
and Brick formalises the incentives [14].

---

## 3. Which protocol layer each interactivity requirement lives at

This section answers the brief's fourth prior-art item — Decker's asserted position that Lightning's
interactivity is a *payment-protocol* property, not a transport-layer one. **I could not find a
statement by Decker to that effect** (see §6). I therefore state the layer split from the primary
protocol texts, and attribute only what those texts say.

| Requirement | One-way channel | Lightning / penalty channels | Layer it lives at |
|---|---|---|---|
| Payer reachable during payment | **No** — one signature, payer → payee, no ack needed [4][5][8] | No for the *send*; yes for the *handshake* that revokes the previous state [6] | **Payment protocol** |
| Payee reachable during payment | **No** — payee only receives/verifies [4][5] | Yes — it must return its own signature and the revocation secret [6] | **Payment protocol** |
| Round trip per update | **Zero** [4][5] | At least one [6] | **Payment protocol** |
| Setup (open) interactivity | Yes — 4-message minimum: unsigned funding out, signed refund back, funding signature out, broadcast [4][5] | Yes, and defined as a mandatory "interactive transaction construction" phase at the wire level [23] | **Transport + payment protocol** |
| Close interactivity | No for the payee (unilateral); yes for the payer's refund path [4][5] | No for either, but requires a timeout race [6][10] | **Settlement/chain** |
| Watchdog requirement | **None for the payer**; payee must act before the refund matures [1][4]; none at all if a trusted cosigner is used [1] | Third party / watchtower, or online participation [6][14] | **Watchdog layer (a distinct layer)** |
| Chain interaction | 1 funding tx + 1 closing tx, +1 refund tx in the failure case [1][4] | Same order, plus HTLC resolution txs [6] | **Settlement/chain** |
| Consensus support needed | 2-of-2 CHECKMULTISIG + nLockTime; `OP_CSV`/BIP 68 for clean relative timeouts [7] | + revocation-secret semantics; + ANYPREVOUT for eltoo ([11]: Draft) | **Consensus/script** |

**The single most decision-relevant row is the first two.** In a one-way channel the *payment
protocol is non-interactive in the strict sense*: no message is required in the payee → payer
direction after setup. The payee's security is monotone payout [8][10]. That is precisely the
property the brief needs. Lightning's interactivity is not inherent to "payment channel"; per its own
deployed spec, it is the mandatory bidirectional "Interactive Transaction Construction" phase [23]
plus the per-update revocation handshake [6].

**[inferred]** A cleaner way to state the general law, supported by the sources above but not
asserted verbatim in any one of them: *interactivity scales with the symmetry of the invalidation
mechanism*. Symmetric penalties (Lightning: both sides hold toxic secrets, both must revoke) ⇒
round trips and watchdogs. Asymmetric monotone state (one-way: only the payee's balance can rise) ⇒
no round trips and no payer watchdog. Directional state (eltoo: state numbers, symmetric and
non-toxic) ⇒ no toxic secrets, but still needs the victim to react in time [10].

---

## 4. The exact minimum the payer must know about the chain

This is the crux per the brief. Answer: **"blockchain-blind payer" is achievable for the payment
phase, and is *not* achievable for the channel's lifetime.** The minimum is four items — two are
one-shot *knowledge*, two are one-shot *capabilities*.

### 4.1 What the payer must **know**

1. **Its own funding outpoint(s) and value.** Spilman: the payer builds TX1 from "1 or more inputs
   from user's 'listunspent'" [1]. The wiki's T1 requires "10 BTC" of the client's coin [4].
   This is *self-knowledge of one's own coins*, not chain monitoring — a wallet holding its own UTXO
   set satisfies it. **[inferred]** This is the irreducible requirement: you cannot create a
   non-custodial claim on chain value without already owning a chain output. Any construction that
   appears to avoid this is either (a) custodial at some issuer, or (b) a bearer instrument that
   itself needed a chain-funded issuance.
2. **The confirmation status of the funding tx.** The payee's rule is not to deliver before it holds
   a paying signature [3][10]. The payer, symmetrically, must know funding happened before treating
   the channel as live. In practice this is deliberately skipped for latency: the ETH WiFi
   proof-of-concept states it "does not wait for the initial multi-signature transaction to be
   confirmed before proceeding with payments since confirmation takes 10 minutes on average and the
   client should not have to wait that long to use the service. This leaves open a small window of
   opportunity for the client to attempt a double-spending attack" [18]. Their measured confirmation
   time: mean ≈16 min, median ≈7.8 min [18].

### 4.2 What the payer must be **able to do** (one-shot, not continuous)

3. **Broadcast the funding tx** (or have the payee do it — the wiki explicitly allows either: "either
   party can do this if they both have connectivity" [4]).
4. **Broadcast the pre-signed refund tx, on failure.** bitcoinj's guarantee is stated actively: "If
   the server halts at any point in the protocol, the client **can always get their money back**"
   [5] — by sending it. Spilman states the same thing from the payee's side as a residual risk, not a
   guarantee: the payee "must broadcast TX2-Final before nLockTime, but has no guarantee the user
   hasn't offered a bribe for miners" [1]. There is no mechanism by which the refund publishes itself.

### 4.3 What the payer does **not** need

- No chain view during the payment phase. One signature per payment, no ack, no confirmation wait
  [4][5][8].
- No watchdog. The payer holds the fully-signed refund; nothing needs monitoring [4][5]. (Contrast
  Lightning, which needs watchtowers or online participation [6][14].)
- No knowledge of the payee's redemption timing. The payee bears the timeout-race risk (§2d).

### 4.4 The disproof clause

> **A one-way channel imposes *no* chain requirement during the payment phase. It imposes *two***
> **one-shot chain requirements over the channel's lifetime, and the second is on the exit path the**
> **brief does not need to solve.**

The failure case is precise: if the payer never regains any gateway, the refund tx it holds is never
broadcast, so the payee can never redeem the final state *and* the payer can never reclaim. Both
parties' funds sit in a 2-of-2 until either somebody gets a chain gateway or — for the pre-signed
`TX2-Locked`/refund — forever. That is **stranded, not stolen** — the payer is not robbed, but the
deposit is a donation to nobody. `[unverified]`: I found no source that quantifies or names this
stranding case; it follows from the protocol's one-sided publication requirement [4][5].

Two corollaries for TollGate:

- **In the genuine cold start the router has no WAN** (per the brief's `bootstrap.go` context), so
  neither funding nor redemption can be broadcast by either party until *somebody* in the mesh has
  a chain gateway. A one-way channel therefore does not remove the chain dependency from the system;
  it **relocates it from every customer's per-payment path to one funding event and one redemption
  event**, and reduces the number of chain-capable parties required from "every payer" to "at least
  one". **That is a real and useful increment**, but it is not custodian-freedom.
- **The honest claim-to-be-tested is the brief's own reframing**, and the prior art supports it: the
  router, as payee, is the natural redeemer, and the payee is the party that needs the chain view
  and the redemption timing [1][4]. The *payer* needs the chain only twice, and never to *watch* it.

### 4.5 Assumption under which "blockchain-blind payer" holds, stated exactly

> A payer can transact in a one-way channel with **no chain access, no live mint, and no custodian**,
> *given* that (a) it already holds a confirmed on-chain UTXO, (b) it can publish exactly two
> transactions at two moments of its choosing (open, and exit-on-failure), and (c) at least one other
> party in the system can publish on-chain on the payee's behalf or the payee itself regains chain
> access before the refund's timeout matures.

Drop (a) and it is custodial. Drop (b)'s second half and the deposit strands. Drop (c) and the payee
eats the timeout race. Each is a *named* trust/liveness assumption, per the brief's instruction that
custody is a spectrum and must be stated exactly — none of the three is "trust the operator not to
steal", which is the property that actually matters.

---

## 5. Citations for each claim, and the Decker-thesis verification

### 5a. Claim → source map

| Claim | Source |
|---|---|
| Spilman's 7-step one-way protocol; the trusted-3rd-party alternative; the miner-bribe caveat | [1] |
| Spilman's own testnet failure on mempool replacement, within 24h of posting | [2] |
| "The seller won't hand over the goods … until they have a valid transaction signed by the buyer" | [3] |
| Wiki Example 7 protocol; pre-signed refund; mempool-replacement warning; nLockTime non-standardness | [4] |
| bitcoinj refund-before-broadcast ordering; one-signature micropayment; 1-day default locktime | [5] |
| RSMC / relative maturity / `OP_CHECKSEQUENCEVERIFY`; timelock-choice risk; "designated third party" | [6] |
| BIP 112 `Status: Deployed`; motivation names "payment channels" | [7] |
| "intrinsically unidirectional"; strictly-increasing payout; duplex = pairs of one-way channels | [8], [9] |
| eltoo: state numbers, `SIGHASH_NOINPUT`, two safety assumptions, victim-must-detect | [10] |
| BIP 118 (Decker, Towns) `Status: Draft`; why witness-script commitment is optional | [11] |
| Ark: receiver joins with no onchain tx; only the operator stays online; VTXO refresh; committee | [12] |
| Ark original concept by Burak (2023) | [13] |
| Brick: offline participants, "both solutions require online participants that frequently monitor" | [14] |
| RSOS 2018 venue/volume/page 5(8):180089; SSS 2017 predecessor; both duplex/tree funding | [15], [16], [17] |
| The Decker-supervised WiFi one-way-channel proof-of-concept; its measurements and its double-spend window | [18] |
| Decker-supervised bachelor thesis on routing over *bidirectional* channels | [19] |
| Enumeration of all Decker-supervised TIK theses (18 rows) | [20] |
| A one-way-channel thesis exists at AUEB, in Ethereum | [21] |
| A one-way channel implemented as the base of a bidirectional one, at UAB | [22] |
| BOLT #2 mandates an "Interactive Transaction Construction" phase | [23] |
| Todd's hub-and-spoke micropayment extension ("nearly completely untrusted hub") | [24] |
| Decker's Bitcoin works list (67 items) contains no one-way-channel paper | [25] |
| Crossref metadata for the RSOS paper | [27] |

### 5b. The Decker-thesis claim: what I actually established

**The circulating claim** (per the brief): *Decker supervised a master's thesis on Spilman-style
one-way channels.*

**Verdict: partly right in substance, wrong in specifics — and the substance is a stronger finding
than the claim.**

What I verified:

1. **There is no payment-channel paper by Decker about one-way channels.** His OpenAlex works list
   (entity `A5084430723`, 67 works) contains the duplex paper and the Burchert funding papers, and
   nothing one-way [25]. The Bitcointalk/Bitcoin-wiki lineage of one-way channels runs
   Spilman → Hearn (wiki) → bitcoinj [1][4][5]; Decker is not in it.
2. **The Burchert work is duplicate/tree funding, not donor-offline one-way channels.** Titled
   "Scalable Funding of Bitcoin Micropayment Channel Networks" at both SSS 2017 [16] and RSOS
   2018 [15]; the abstract concerns "shared accounts of groups of nodes that flexibly create
   one-to-one channels", one on-chain tx per user to join a group instead of one per channel, and a
   90% cost reduction for a 20-user/100-channel group [17]. Full-text search of the RSOS article
   returns **zero** hits for "unidirectional" and zero for "Spilman" [17]. So the brief's note — that
   Burchert's thesis became the duplex/tree paper — is **confirmed**.
3. **Decker genuinely supervised a thesis on the one-way channel — a *semester* thesis.** From the
   archived ETH TIK "Past Theses" database (snapshot 2018-03-13, covering HT07–FS17, 1042 thesis
   rows, of which 18 list Christian Decker as contact/supervisor) [20]:

   > `Paying your Internet, One Byte at a Time | … | S | Christian Decker | HS 13 | … | SA-2013-49` [20]

   The linked PDF is live at `pub.tik.ee.ethz.ch/students/2013-HS/SA-2013-49.pdf` [18]. Its title
   page: **"Paying your Internet, One Byte at a Time"**, author **Sandra Siby**, **"Supervisor:
   Christian Decker"**, dated **December 16, 2013** [18]. Its abstract: *"This thesis builds a
   proof-of-concept that allows an access point to provide Internet access to untrusted users in
   exchange for payment in bitcoins, using a micropayment channel"* [18]. It implements the
   channel with bitcoinj [18] and cites Hearn & Spilman's wiki page as its channel reference [18].

   So the claim is **directionally correct and better than it sounds**: this is not an abstract
   thesis, it is a working Raspberry-Pi WiFi AP selling Internet access for a one-way channel —
   closer to TollGate than anything else in the literature. But the degree type is **S (semester),
   not M (master's)**, the student is **Siby, not Burchert**, and the work **applies** the
   construction rather than studying one-way channels.

4. **Other one-way-channel theses exist, but not at Decker's group.**
   - Diamantogiannis (2021), *"OWPC: a simple unidirectional ethereum off chain payment channel"*,
     AUEB, DOI `10.26219/heal.aueb.4367`: "We propose an Ethereum unidirectional off chain payment
     channel protocol, called OWPC (One Way Payment Channel). … The main advantage of OWPC is that on
     chain fees remain constant no matter how much time the channel is alive or how many transactions
     take place in it" [21]. (Metadata and abstract only — the full text is behind the DOI.)
   - Lozano Jarque (2017), *"Development of a payment channel over the Bitcoin network"*, UAB: "El
     projecte doncs, consisteix en dissenyar i implementar un canal de pagament bidireccional usant la
     combinació de dos canals de pagament unidireccionals" — a bidirectional channel built from two
     one-way channels [22]. (Metadata/abstract only; the UAB repository is behind an Anubis
     proof-of-work wall.)

5. **A second Decker-supervised channel thesis exists and is *bidirectional*.** van Schie (2015),
   *"Routing Scalable Bitcoin Payments"*, BA-2015-12, "Supervisors: Christian Decker, Prof. Dr. Roger
   Wattenhofer" [19]; its abstract describes "Bidirectional payment channels" and a routing protocol
   over them [19]. This is the duplex lineage, not the one-way one.

**Sharpest defensible statement of the prior claim:** *Christian Decker supervised an ETH semester
thesis (SA-2013-49, Sandra Siby, Dec 2013) that built a working one-way-channel proof-of-concept for
selling WiFi Internet access — the closest existing prior art to the TollGate proposal. He did not
supervise a master's thesis, and never published a paper on one-way channels; his own channel work is
the duplex construction and the funding-tree follow-up.*

### 5c. On the two "Anti DoS pricing" sources

The brief lists "Spilman (2013), *Anti DoS pricing* / bitcoin-list; Hearn & Spilman, *Bitcoin
contracts*". Findings:

- The **document titled "Anti DoS pricing" could not be located.** A mail-archive full-text search of
  `bitcoin-development` for the phrase returns **"No matches were found for Anti DoS pricing"**.
  `[unverified]` — I cannot confirm such a document exists.
- The thread it belongs to does exist, is titled **"[Bitcoin-development] Anti DoS for tx replacement"**,
  and was opened by Mike Hearn [1]. Spilman's protocol post is a reply in that thread [1]. That is the
  right primary source and it is cited throughout this memo.
- The earliest *installment* of the protocol (the four-step variant referenced by Todd as "(1)") is
  `msg02028` — i.e. the same message I quote as [1]. Todd's 2014 cross-reference anchors it: *"Note
  how the mechanism I'm proposing is basically just a Jeremy Spilman-style micropayment channel(1)"*,
  where (1) points at `msg02028.html` [3]. That resolves the citation chain from the community's own
  side.

---

## 6. What I could NOT verify

1. **The original mailing-list post as posted.** `sourceforge.net/p/bitcoin/mailman/message/*`
   returns HTTP 403; `lists.linuxfoundation.org/pipermail/bitcoin-dev/*` returns 418 (Anubis);
   `gnusha.org` returns 418; and the Wayback CDX for the linuxfoundation URL has no snapshot. All
   Spilman quotes come from mail-archive.com mirrors of the same thread [1][2]. The
   `gist.github.com/jspilman/5424310` code sample his post links to *is* live and its description
   matches ("Proof of Concept for Payment Channels", created 2013-04-20) [26], which corroborates the
   thread content.
2. **A document literally titled "Anti DoS pricing".** See §5c. Not found; searching the list for the
   phrase yields zero matches. `[unverified]`.
3. **Decker's own statement that Lightning's interactivity is a payment-protocol-layer property.**
   The brief asserts this. I could not find any page, paper, or post by Christian Decker saying it.
   His Medium account contains no posts ("Christian Decker hasn't written any stories yet"), his
   personal site `cdecker.github.io` 404s, and `blog.blockstream.com/author/cdecker/` 404s. I
   therefore built §3 from the primary protocol texts instead (eltoo [10], BOLT #2 [23], the
   Lightning paper [6]) and attribute only what they say. **Do not cite Decker for the layer claim**
   without a source. `[unverified]`
4. **Royal Society Open Science full text.** `royalsocietypublishing.org/doi/10.1098/rsos.180089`
   returns 403 (Cloudflare), as does the Jina reader proxy. I used the PMC mirror [17] and Crossref
   metadata [27] instead; both agree on venue/volume/page 5(8):180089 [15][17][27].
5. **DBLP.** All `dblp.org` requests return HTTP 202 with no body (bot challenge). Bibliographic
   verification was done via OpenAlex [25] and Crossref [27] instead.
6. **The two other one-way-channel theses in full.** The AUEB (OWPC) and UAB theses were retrieved
   only as OpenAIRE metadata + abstracts [21][22] — AUEB's PDF is behind its DOI, UAB's repository is
   behind an Anubis proof-of-work wall. I did **not** read their constructions, so I cannot say
   whether they are payer-offline, whether they need the payer to watch the chain, or what their
   trust assumptions are. **These two are the highest-value unread prior art for this spike.**
7. **The original Ark whitepaper.** `arkdev.info/docs/ark-paper.pdf` now serves the *Arkade* docs
   site, and the Wayback CDX has no snapshot of the PDF at all. I cite the 2026 peer-reviewed
   formulation [12] and the 2023 concept post as referenced by it [13]. Note the 2026 paper's own
   acknowledgements: "The original version of the Ark protocol was conceptualised by Burak [15]. Our
   work builds on his idea, which required covenants" [12] — so the 2026 paper is a *modified* Ark,
   and its "no onchain footprint to receive" property may not hold of the 2023 design verbatim.
   `[unverified]`
8. **Completeness of the TIK theses enumeration.** The archived "Past Theses" page is a snapshot
   (Wayback, 2018-03-13) covering HT07–FS17, and in it the *student-name* column is populated for
   most rows but blank for SA-2013-49 (I read Siby's name from the PDF, not the DB) [18][20]. Later
   snapshots redirect back to this one, so I have **no coverage after FS17** — including the period
   in which Burchert's master's work would have been registered. I therefore **cannot** claim
   exhaustively that no one-way-channel master's thesis was ever supervised at that group; I can only
   claim it does not appear in the DB's coverage window.
9. **Whether Burchert's master's thesis exists as a document.** OpenAlex lists exactly two
   Burchert+Decker+Wattenhofer outputs (SSS 2017 [16], RSOS 2018 [15]) and nothing of type
   `dissertation` [25]; OpenAlex's dissertation filter for `raw_author_name: Conrad Burchert` returns
   only *other* Burcherts (an image-search author, a biologist) [25]. `[unverified]` whether the
   thesis text exists separately from the papers.
10. **Quantitative costs / feasibility numbers for a TollGate-shaped deployment.** This memo is
    protocol prior art only. Every performance figure here is the source's own measurement on the
    source's own hardware (e.g. the ETH WiFi thesis measured 100 runs on a Raspberry Pi + laptop, mean
    confirmation ≈16 min, median ≈7.8 min [18]); none of it is evidence about a TollGate mesh on a
    Cashu-based economy.

---

## 7. Three things the spike should take from this

1. **Cite SA-2013-49.** It is the closest prior art that exists — a 2013 proof-of-concept of exactly
   "AP sells Internet access for a one-way channel" — and its two self-reported limitations are the
   spike's live questions: the deliberately-skipped funding confirmation ("leaves open a small window
   of opportunity for the client to attempt a double-spending attack") and the 24-hour refund lock
   ("the client would receive money … only after 24 hours") [18].
2. **Do not build on nSequence replacement, and say so.** The canonical wiki construction's
   double-spend-override relies on a mechanism that the same page says was disabled and removed, and
   Spilman hit the failure on testnet within a day [2][4]. Use the pre-signed-refund +
   `OP_CSV`-relative-timeout shape [7] and the "no service before the payee holds a paying signature"
   delivery rule [3][10].
3. **State the payer's chain requirement in the brief's own terms and scope to it.** The defensible
   claim is: *the payer needs no chain during payment and no watchdog ever; it needs a confirmed UTXO
   and exactly two publication events (open, and exit-on-failure); and the exit needs at least one
   chain-capable party in the system.* That is the honest form of "the mint's oracle disappears from
   the customer's path" [§4.5]. Anything stronger — "payer needs no chain at all" — is disproved by
   the fact that the refund only pays when the payer sends it [4][5].

---

## Sources

1. https://www.mail-archive.com/bitcoin-development@lists.sourceforge.net/msg02028.html — Spilman (2013-04-19), 'Re: [Bitcoin-development] Anti DoS for tx replacement' — the 7-step one-way channel protocol
2. https://www.mail-archive.com/bitcoin-development@lists.sourceforge.net/msg02029.html — Spilman (2013-04-20), test-net failure report: closing tx rejected, 'inputs already spent'
3. https://www.mail-archive.com/bitcoin-development@lists.sourceforge.net/msg05183.html — Peter Todd (2014-04-26), 'basically just a Jeremy Spilman-style micropayment channel'
4. https://en.bitcoin.it/wiki/Contracts — Hearn, Bitcoin Wiki 'Contract' — Example 7: Rapidly-adjusted (micro)payments to a pre-determined party (byline: "These pages were written by Mike Hearn"; the protocol is Spilman's [1])
5. https://bitcoinj.github.io/working-with-micropayments — bitcoinj — Working with micropayment channels (the deployed Hearn-Spilman implementation)
6. https://lightning.network/lightning-network-paper.pdf — Poon & Dryja (2016), The Bitcoin Lightning Network, v0.5.9.2
7. https://github.com/bitcoin/bips/blob/master/bip-0112.mediawiki — BIP 112 CHECKSEQUENCEVERIFY (BtcDrak, Friedenbach, Lombrozo) — Status: Deployed
8. https://www.tik.ee.ethz.ch/file/716b955c130e6c703fac336ea17b1670/duplex-micropayment-channels.pdf — Decker & Wattenhofer (2015), Duplex Micropayment Channels, author PDF (ETH TIK)
9. https://doi.org/10.1007/978-3-319-21741-3_1 — Decker & Wattenhofer (2015), Duplex Micropayment Channels — DISC 2015, LNCS, DOI
10. https://blockstream.com/eltoo.pdf — Decker, Russell & Osuntokun, eltoo: A Simple Layer2 Protocol for Bitcoin
11. https://github.com/bitcoin/bips/blob/master/bip-0118.mediawiki — BIP 118 SIGHASH_ANYPREVOUT for Taproot Scripts (Decker, Towns) — Status: Draft
12. https://arxiv.org/abs/2605.20952 — Keer, Alexopoulos, Maffei, Argentieri, Camilleri, Avarikioti — Ark: Offchain Transaction Batching in Bitcoin
13. https://brqgoo.medium.com/introducing-ark-6f87ae45e272 — Burak (2023), 'Introducing Ark' — original Ark concept post cited by the arXiv paper
14. https://arxiv.org/abs/1905.11360 — Avarikioti, Kokoris-Kogias, Wattenhofer, Zindros — Brick: Asynchronous Payment Channels
15. https://doi.org/10.1098/rsos.180089 — Burchert, Decker & Wattenhofer (2018), Scalable funding of Bitcoin micropayment channel networks, R. Soc. Open Sci. 5(8):180089
16. https://doi.org/10.1007/978-3-319-69084-1_26 — Burchert, Decker & Wattenhofer (2017), Scalable Funding — SSS 2017, LNCS
17. https://www.ncbi.nlm.nih.gov/pmc/articles/PMC6124062 — PMC full text, Scalable funding of Bitcoin micropayment channel networks
18. http://pub.tik.ee.ethz.ch/students/2013-HS/SA-2013-49.pdf — Siby (2013), 'Paying your Internet, One Byte at a Time', ETH TIK Semester thesis SA-2013-49, supervisor: Christian Decker
19. http://pub.tik.ee.ethz.ch/students/2015-FS/BA-2015-12.pdf — van Schie (2015), 'Routing Scalable Bitcoin Payments', ETH TIK BA-2015-12, supervisors: Christian Decker, Roger Wattenhofer
20. http://web.archive.org/web/20180313152942/http://www.tik.ee.ethz.ch/db/public/tik/?db=sada&form=report_thesis&status=past — Wayback snapshot of the ETH TIK 'Past Theses' database (HT07-FS17) used to enumerate Decker-supervised theses
21. https://doi.org/10.26219/heal.aueb.4367 — Diamantogiannis (2021), OWPC: a simple unidirectional ethereum off chain payment channel
22. https://ddd.uab.cat/record/181600 — Lozano Jarque (2017), Development of a payment channel over the Bitcoin network, UAB
23. https://github.com/lightning/bolts/blob/master/02-peer-protocol.md — BOLT #2: Peer Protocol for Channel Management — 'Interactive Transaction Construction'
24. https://www.mail-archive.com/bitcoin-development@lists.sourceforge.net/msg06576.html — Peter Todd (2014-12-12), near-zero fee hub-and-spoke micropayments
25. https://api.openalex.org/works?filter=author.id:A5084430723&per_page=100 — OpenAlex works list for blockchain 'Christian Decker' entity A5084430723
26. https://gist.github.com/jspilman/5424310 — Spilman, 'Proof of Concept for Payment Channels' gist (2013-04-20)
27. https://api.crossref.org/works/10.1098/rsos.180089 — Crossref record for 10.1098/rsos.180089
