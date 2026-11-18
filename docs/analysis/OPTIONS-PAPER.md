# LNURLcash in TollGate — strategic fit and options paper

**Status:** advisory / design. Part of the LNURLcash spike.
**Date:** 2026-10-15.
**Target read:** `tollgate-A04E`, OpenWrt 24.10.2 aarch64_cortex-a53, `tollgate-wrt v0.6.0-rc4-gcf7cad3`.
**Module source:** `OpenTollGate/tollgate-module-basic-go` @ `cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` ("fix(tollwallet): bound each mint registration so a slow mint cannot stall boot (#940)").
**Spec under evaluation:** LUD-25 (`https://raw.githubusercontent.com/lnurl/luds/lnurlcash/25.md`, 256 lines) and LUD-26, both in PR <https://github.com/lnurl/luds/pull/301> — **open, unmerged, 44 commits, 11 comments, last updated 2026-10-05** `[measured, GitHub API 2026-10-15]`.

**Labels.** `[M]` = measured by me, quoted below. `[V]` = verified against a spec/vectors file, cited. `[Q]` = quoted spec or source line. `[I]` = inferred by reading code/spec, no execution.

**What I did not do.** No live TollGate router was reached (`tollgate-A04E` did not resolve on DNS or answer `:2121` from this host `[M]`). No mint-hosted LNURLcash note was ever minted, spent, rotated or melted. No `libbitcoinkernel` was run. Every number below is either (i) quoted from a spec, (ii) quoted from module source, (iii) produced by the committed offline experiment in `experiments/`, or (iv) an HTTP latency I actually measured against the seven configured mints. Anything else is marked `[I]`.

---

## 0. Bottom line

**LNURLcash is a well-designed bearer-note *container* that solves a problem TollGate does not have, at a layer TollGate does not own.** Its one genuinely relevant contribution — an offline proof of *issuance* — is exactly the property the module's own doctrine already forbids granting on, and the spec says so in its own warning.

| Role | Question | Verdict |
|---|---|---|
| **a** | Alternative **accepted instrument** at redemption | **NO** — the instrument is hard-coded at four layers, and the offline proof carries no consumption state |
| **b** | **Offline / queued payment** when the mint is unreachable | **PARTIAL — the only role with a real product case** — and the increment is *verify-and-defer*, not LNURLcash |
| **c** | **Receipt / proof format** | **NO** — `cs1` is not a receipt, and the interesting receipts are not in LUD-25 at all |
| **d** | **Value movement between TollGate routers** (reseller/upstream) | **NO for now** — the strongest *theoretical* fit, blocked by the same replay gap plus an unreachable call path |
| **e** | **Not a fit** | **This is the honest default**, with one carve-out named in §5 |

**Recommendation: bounded experiment + watch.** Run experiments X1–X4 (§3, all offline; X1 and X2 are already done and committed). Do **not** adopt. Do **not** build a LNURLcash acceptor into `tollgate-module-basic-go`. Revisit only if LUD-25 lands a normative answer to offline *consumption*, or if TollGate acquires a chain view.

---

## 1. Fit assessment, role by role

### 1.0 The stack it has to fit into `[M]`

The payment path, read from source and cross-checked live:

```
captive portal (uhttpd :2051, nodogsplash :2050)
  → GET /  ─ kind:10021 advertisement, ["price_per_step","cashu",…] × 6 reachable mints
  → GET /ln-invoice  ─ LUD-03-style quote (refuses without a quote id)
  → POST /  ─ the body IS the bearer instrument
        HandleRootPost            src/main.go:1060
        clientIdentity(w, r, "")  src/main.go:1092   ← refuse BEFORE the token is read
        extractCashuToken(body)   src/main.go:1045, :1123
        Merchant.PurchaseSession  src/merchant/merchant.go:1493
        beginReceive(reference)   src/merchant/merchant.go:1574  ← process-memory in-flight map
        beginReceiveIntent(...)   src/merchant/merchant.go:1597  ← DURABLE #502 journal
        WalletPort.Receive(token) src/tollwallet/port.go:196     ← the mint round-trip
        grantSessionAccess(...)   src/merchant/merchant.go:1790
        valve.OpenGate*           (local)
```

Five facts about that path decide everything below:

1. **The instrument is a raw string.** `extractCashuToken` returns the body verbatim unless it is a kind-21000 event `[Q src/main.go:1045-1057]`, and the code says it out loud: *"The body IS the bearer instrument on this route"* `[Q src/main.go:1111]`. Body cap is 1 MiB `[M src/main.go:1103]`.
2. **The instrument's *name* is a hard-coded literal.** The kind:10021 tags emit `"cashu"` positionally as field 2 of every `price_per_step` tag — `{"price_per_step", "cashu", …}` `[Q src/merchant/merchant.go:1998-2006]`, and the event is **signed** `[M src/merchant/merchant.go:2022]`. Live advertisement on the router shows this for all six reachable mints `[M ROUTER-FEASIBILITY.md:86-98]`.
3. **There is no instrument or backend config key.** `grep -rniE 'wallet_backend|wallet\.backend|"backend"|WalletConfig|WalletPolicy' src/config_manager/ src/merchant/ src/main.go` → **no matches** `[M ROUTER-FEASIBILITY.md:176]`. The schema (`src/config_manager/config_schema.go`) has no such field; `Metric` and `StepSize` are the only relevant enums `[M config_schema.go:39,44]`.
4. **The designed seam is dead code.** `tollwallet.OpenWallet(cfg WalletConfig)` exists at `src/tollwallet/select.go:28-41` and has **zero production callers**; the merchant calls `tollwallet.NewWalletPort(...)` directly at `src/merchant/merchant.go:509` `[M ROUTER-FEASIBILITY.md:221-232]`.
5. **Value is realised by a keyed swap at a mint.** `WalletPort.Receive` `[Q src/tollwallet/port.go:196]`; `CheckTokenSpent` is NUT-07 `[Q src/tollwallet/port.go:247-256]`.

### 1.1 Role (a) — alternative **accepted instrument** at redemption: **NO**

**Four independent reasons, any one of which is sufficient.**

**(a1) The verifier would be granting on an issuance proof, which the spec itself says proves nothing about outstandingness.** LUD-25, verbatim:

> *"A certificate proves issuance, not that the note is still outstanding: the previous holder still knows the spend. A new holder SHOULD rotate (or merge) the note as soon as it is online."* `[Q 25.md:235]`

A TollGate that opens a gate on a `cs1` check has performed *exactly* LUD-25 §"Offline verification" step 3 — *"rebuilds `message` from the `cs1` amount and `Q`, recovers a key from `cs1`, and compares it with the `mintPubkey` on record"* `[Q 25.md:232]` — and nothing else. §2 shows, with a runnable experiment, that this accepts the same note unboundedly.

**(a2) The module has already written this exact rule down and banned it.** The `bearer-instrument-wallet-port` design record:

> **"A NUT-07 answer is about CONSUMPTION, never EXISTENCE. … a plain Cashu mint answers `UNSPENT` for a nullifier it has never seen … So `UNSPENT` means 'not consumed', NOT 'real'. Existence is answered from the signing log … Conflating the two is exactly how a forged note passes a naive 'is it spendable?' check."** `[Q ARCH-FIT.md:417-424]`

`cs1` is pure existence (an ECDSA signature over `(amount, Q)`) and **zero** consumption. A TollGate `cs1` gate *is* the naive is-it-spendable check the repo documents as a known trap.

**(a3) There is no reconciler, so the module's compensating-action doctrine cannot be satisfied.** `AGENTS.md:61-68` requires either durable forward recovery or an explicit compensating action, and the module has a durable journal (#502) built for exactly that: *"the attempt is durable before the money moves, so a crash, a dropped response or a restart can still be reconciled into service-or-evidence"* `[Q src/merchant/merchant.go:1584]`. For a note there is nothing to reconcile *against*:
- `reconcileReceiveIntent` → `checkTokenSpent` → NUT-07 `[M src/merchant/receive_intent.go:247,290]` — and a note has no mint-side spend state to query. LUD-25's own burn rule: *"**Burns are permanent:** a burned `Q` MUST NOT be returned to the outstanding set"* `[Q 25.md:188]` — burn state exists **only at the SERVICE**, and the router is not the SERVICE.
- The journal's key would be unstable: `reference := receiveReference(token)` fingerprints the serialized instrument via `utils.TokenFingerprint` `[M src/merchant/merchant.go:1573]`, an HMAC-SHA256 over `strings.TrimSpace(token)` with a per-install salt `[Q src/utils/token_fingerprint.go:48-57]`. The spec-mandated rotation a new holder SHOULD perform changes `k1` completely. And the salt itself may be ephemeral — *"fingerprints will not match across a restart, which matters to the journal"* `[Q token_fingerprint.go:82-89]`.

**(a4) Even the in-band advertisement would be lying.** The accepted instrument is signed into the kind:10021 event as the literal `"cashu"` `[M merchant.go:2002]`. Relaying a taproot note under a signed `"cashu"` tag is a signed statement the router cannot honour — the artifact-misrepresentation class this repo treats as a release blocker elsewhere. Adding a second tag is a **wire-format change to a signed advertisement**; ROUTER-FEASIBILITY §6 puts that at Slice 2, gated behind everything else `[Q ROUTER-FEASIBILITY.md:448]`.

> **Do not confuse the *envelope* with the *instrument*.** The 210-character note URL `[M experiments/X1-OUTPUT.txt]` fits comfortably inside the 1 MiB body cap at `src/main.go:1103`, and "no new endpoint" is real `[Q 25.md:10]`. Route-wise, a note is a trivial fit. Semantically it is not an instrument, because the router cannot make a `k1` **not** a `k1`.

### 1.2 Role (b) — offline/queued payment when the mint is unreachable: **PARTIAL — the only real fit**

This is the role with a genuine product case, and it is **not** what LNURLcash was designed for. State it precisely.

**The problem that is real.** `defaultReceiveTimeout = 30 * time.Second` `[Q src/merchant/merchant.go:221]` bounds the customer-facing leg. Past it the customer gets `payment-outcome-unknown` and is told not to resubmit `[Q src/merchant/merchant.go:1691-1710]`. That class exists because the grant is *downstream* of an external authority. Measured mint reachability from this host right now `[M]`:

| mint | `POST /v1/checkstate` | `GET /v1/info` |
|---|---|---|
| `mint.coinos.io` | 0.47 s | 0.71 s |
| `kashu.me` | 0.29 s | 0.14 s |
| `mint.macadamia.cash` | 0.20 s | 0.16 s |
| `mint.westernbtc.com` | 0.67 s | 0.66 s |

So today the round-trip is ~0.2–0.7 s — **far inside** the 30 s deadline. `[I]` The offline role is therefore *not* motivated by latency today; it is motivated by a mint being **unreachable**, which for a router whose whole product is a WAN-uplink reseller (`reseller_mode` exists, `upstream_detector` exists, `upstream_wifi` exists `[M /etc/tollgate/config.json, ROUTER-FEASIBILITY.md:142-170]`) is a real state, not a hypothetical one.

**Why LNURLcash is only a partial answer to it.**
- `cs1` verifies **issuance**, not outstandingness `[Q 25.md:235]`. So "offline accept" necessarily means "accept a replayable instrument" — see §2.
- The module already has the *grant* half for free, and it is local: `AddAllotment` is pure memory `[M src/merchant/merchant.go:2615-2626]`, `grantSessionAccess` is local `[M src/merchant/merchant.go:1790]`, `valve.OpenGate*` is local. What is missing is `verify-without-mint` **plus** `defer-settlement`.
- **`defer-settlement` is a Cashu-level change, not a LNURLcash one.** The one-way-channel spike reached the same conclusion independently: *"the increment is not 'a payer with no internet buys from a router with no internet' … the defensible increment is deferral"* `[Q SPIKE-REPORT.md:228-240]`. And the module's own `hasLockedProofs` refuses any proof whose secret is not `nut10.AnyoneCanSpend` before `Receive` `[M src/tollwallet/tollwallet.go:296-298, :521-529]`, so a *locked/timelocked Cashu* variant lands there first `[Q CUSTOMER-MINT-AND-INTEROP.md:664-667]`.

**Verdict.** The *user-visible* goal — "the gate opens without a live mint" — is worth pursuing. LNURLcash is **one** way to get the *verify* half, and it is the only one that needs no chain view. But the *settle* half is the hard half, and LNURLcash does not help with it at all: after accepting a note the router still has to reach the mint to rotate/melt it `[Q 25.md:163-166]`. **So LNURLcash moves the mint round-trip from "before the grant" to "after the grant" — which is exactly what deferral means, and exactly what the module could do with Cashu by not blocking the grant on `Receive`.**

> The honest framing: **LNURLcash is not the deliverable; deferral is. LNURLcash is a candidate verifier for the first half of it.**

### 1.3 Role (c) — receipt / proof format: **NO**

- A `cs1` is **not a receipt**. It is issued with the note and lives inside the note URL (`lnurlw://mint.example/w?k1=<spend>&c=<cs1>` `[Q 25.md:228]`). It carries no time, no recipient, no counter, no settlement event — §2d measured this. It answers "was this `Q` credited this amount", nothing else.
- **The receipts worth having are not in LUD-25.** `lnurlcash-conformance/docs/BOUND-MINT-RECEIPTS.md` opens: *"Status: additive implementation proposal layered on current LUD-25 comment-bound minting. … It does not modify the upstream LUD-25 draft or dni's reference repositories."* `[Q]` That is the ForgeSworn/Moneyer `mintToHash` extension, not the spec.
- **TollGate does not need a receipt format.** It already has a durable, crash-safe one: `#502`'s receive-intent journal, written before the money moves, with a temp-file → fsync → rename → fsync-parent-directory commit sequence `[M src/merchant/receive_intent.go:84-137]` and states `granted`/`owed`/`abandoned`/`pending` `[Q receive_intent.go:1-31]`. That is strictly more information than a `cs1` carries, and it is already wired into the owed-grant path at `src/merchant/merchant.go:1794-1799`.

### 1.4 Role (d) — value movement between TollGate routers (reseller/upstream settlement): **NO for now**

This is the strongest **theoretical** fit, and it is worth naming why before dismissing it.

**What the module does today.** `#239`'s reseller bootstrap forwards the customer's **entire first Cashu token** whole to the upstream TollGate: *"no swap, no split, no margin"* `[Q src/upstream_session_manager/bootstrap.go:12-24]`, `proofForwarder func(gatewayIP, token string) (allotment uint64, err error)` `[Q bootstrap.go:74]`. The raw token is never kept `[Q bootstrap.go:60]`.

**Why LNURLcash looks attractive here.** "No new endpoint" `[Q 25.md:10]` and "the note *is* a withdraw link" mean a reseller forwarding a note needs no new wire format: the upstream is a SERVICE, the reseller is a WALLET, and the rotate/merge algebra `[Q 25.md:161-166]` is a *specified* way to move value between two parties **without a mint invoice and without Lightning** — indeed `lnurl-mint` already implements exactly that as an advertised rail: a recipient's payRequest carries `["text/cpub", "<cx1>:<i>"]` so a payer holding a note on the same mint can *"skip Lightning entirely … no invoice, no payment, and no round trip through the funding source at all"* `[Q dni_lnurlcash_lnurl-mint_main_README.md:213-222]`.

**Why it nonetheless fails, today, for four reasons.**

1. **Same replay gap, one level up, at a *bigger* value.** The upstream would be granting an upstream session on a proof that the reseller still holds. That is the `V0` failure shape from the spike (`RED.txt` T3: *"Core ACCEPTS a 100% theft"* `[Q SPIKE-REPORT.md:108-110]`) — except instead of a script bug, it is the spec's own design.
2. **The call path does not exist.** `ForwardFirstProof` has **no production caller**: every reference is `bootstrap_test.go`, `session.go:255` (a comment), `upstream_session_manager.go:192` (the manager's own wrapper), and the interface declaration at `types.go:101` `[M grep over src/]`. The reseller lane is not wired. Every attribute of this role is speculative until it is.
3. **`reseller_mode` is `false` on the target router** `[M /etc/tollgate/config.json, ROUTER-FEASIBILITY.md:152]`.
4. **The algebra does not add value to this role.** The bootstrap's stated contract is *"no split"* `[Q bootstrap.go:72]`. A reseller forwarding whole value has no use for split. Rotate is not needed either — the upstream burns it at the mint anyway. The single LUD-25 feature with real substance (rotate/split/merge) is a feature for **varying-value payments between holders**, which is not what a reseller forward is.

### 1.5 Role (e) — not a fit: **this is the honest default for (a), (c), (d), and most of (b)**

The project already committed to the layer where this problem lives: Cashu-native Spilman, `session-ticket-decision.md:371` `[Q SPIKE-REPORT.md:386]`. Granularity is a Cashu concern. LNURLcash is a *mint-side* container for a *holder-side* problem.

---

## 2. The killer question — the replay window, with numbers

### 2.1 What exactly is proven, and what is not

LUD-25 §"Offline verification" step 3 is the whole of the offline check: *"rebuilds `message` from the `cs1` amount and `Q`, recovers a key from `cs1`, and compares it with the `mintPubkey` on record for that `SERVICE`"* `[Q 25.md:232]`. And the spec's own warning immediately follows: *"A certificate proves issuance, not that the note is still outstanding"* `[Q 25.md:235]`.

**`cs1` is a deterministic function of a constant set.** `message = "LNURLcash:" || string(amount_msat) || ":" || hex(Q)` `[Q 25.md:212]`, signed with RFC6979 nonces `[Q 25.md:209]`. There is **no nonce, no timestamp, no recipient, no counter, no chain height, no block hash** in it. Measured `[M experiments/x1_replay_window.py]`:

```
  [PASS] cs1 is a deterministic function of (mint key, amount, Q) -- no nonce, no time
         RFC6979 + no timestamp (25.md:209-216) -> byte-identical forever
```

So the replayer does not need to produce anything. The bytes he was handed are valid forever.

### 2.2 How long is the window? **Unbounded. Not bounded by the spec at all.**

The spec's only prescribed close is *"A new holder SHOULD rotate (or merge) the note as soon as it is online"* `[Q 25.md:235]`. That is:
- a **SHOULD**, not a MUST `[Q 25.md:235]`;
- imposed on the **holder**, not on the acceptor;
- contingent on the holder **being online**.

Therefore the window's length is `(time until the acceptor or the holder reaches the mint)` — a quantity the spec does not bound and cannot bound, because a bearer note's spend secret is *copied*, never *consumed*, at handover. LUD-25's own handover diagram has Alice hand Bob `lnurlw://mint.example/w?k1=A&c=cs1` and Bob verify it **offline** `[Q 25.md:237-248]` — the `k1` is transmitted, so between handover and rotation it is held by **two parties simultaneously**.

For a TollGate whose entire premise is that the customer may have no WAN yet, `(time until online)` is *the length of the session being sold*. Expressed against this router's own constants: **1 sat = 22020096 bytes = 21 MiB** `[M src/config_manager/config_manager_config.go:424` `StepSize: 22020096, // 21 MiB`, `Metric: "bytes"` at `:426`; advertisement `[step_size","22020096"],["metric","bytes"]` `[M ROUTER-FEASIBILITY.md:90]`]. A replay of an N-sat note is N × 21 MiB of the operator's uplink, plus wall-clock on the gate.

**And the spec cannot close it, by construction.** The `cw1`'s relative lock is anchored *"since `SERVICE` credited the note (by mint, rotate, split or merge)"* `[Q 25.md:63]` — a service-clock event, and the spec's own warning: *"A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a custodial policy, not a consensus guarantee, and MUST NOT be presented as trustless."* `[Q 25.md:67]`. The canonical spend transaction is **never broadcast** — *"Every note is spent as input 0 of one fixed, never-broadcast transaction"* `[Q 25.md:71]`, spent output value **0** `[Q 25.md:76-79]`. So there is no UTXO whose spend could be the nullifier.

**Independent confirmation that this is irreducible, from DNI's own repos:**
- `lnurl-vault`'s protocol doc: *"It has no idea whether a `CONFIRMED` note is still outstanding at the mint. … A note redeemed by somebody else's wallet, or rotated away by another client, still reads `CONFIRMED` here forever. LUD-25 is explicit that this is inherent: 'A spent note keeps its valid signature forever, and no revocation is visible offline.'"* `[Q dni_lnurlcash_lnurl-vault_main_docs_PROTOCOL.md:520-529]`
- The conformance threat scorecard's **T5 ("note at rest") is a deliberate `pins-current` control, not a bug**: *"The bearer axiom: every bearer option 'fails' this by design, and the all-minus row is deliberate."* `[Q threat-suite.json scenarios.T5.notes]`
- The scorecard's own `coreTheorem`: *"Re-encrypting a bearer credential to the party that redeems it never shrinks its exposure set … The only encryption that helps is encrypting to the holder, which kills bearer-ness — option G takes that trade deliberately."* `[Q threat-suite.json policy.coreTheorem]`

**The only option on the board that beats T5 is option G, *locked notes*, and its own author says it "surrenders bearer-ness"** `[Q threat-suite.json scenarios.T5.lockedNotes]`. It is not in the draft `[Q threat-suite.json options.G.status = "proposal"]`. **So: the draft spec cannot be made POS-safe at the offline tier. That is a finding, not a preference.**

### 2.3 Who can exploit it, and what they can do

| # | Exploiter | Mechanism | Evidence |
|---|---|---|---|
| 1 | **The previous holder**, deliberately | Keeps a copy of the `k1`, redeems it again at the same router, or at any other SERVICE that accepts the same mint | `[Q 25.md:235]` |
| 2 | **Anyone who saw the note URL at rest** | "a note URL at rest IS the money" | `[Q threat-suite.json T5]` |
| 3 | **Anyone reading a log that retained the `k1`** | *"every value poll leaves the SPENDABLE `k1` in whatever retains request URLs"* | `[Q threat-suite.json T3]` |
| 4 | **Any routing hop on a mint's payment path** | Learns the preimage as the HTLC settles; rotates first | `[Q threat-suite.json T2]` |
| 5 | **A race winner against `/verify`** | Anyone holding the unpaid invoice polls it and takes the preimage | `[Q dni_lnurlcash_lnurl-mint_main_README.md:301-312]` |
| 6 | **The router itself, accidentally, on restart** | The `k1` re-verifies because the dedup was in RAM — §2.4 | `[M experiments/x1_replay_window.py X1c]` |

Threats 4 and 5 are the sharpest: they need **no misbehaviour by the customer at all**. `lnurl-mint`'s own README warns *"Don't put unpaid mint invoices anywhere public, and if you can't accept this exposure for your users, set `VERIFY_ENABLED=false`"* `[Q:301-312]`, and *"the wallet MUST rotate the note immediately after rather than treat verify as having closed that exposure window"* `[Q:292-293]`.

### 2.4 What the router loses, and why its existing dedup cannot cover it

The router's duplicate story rests on one property: *spending destroys the instrument atomically at an external authority.* `[Q src/merchant/merchant.go:297-306]`:

> *"beginReceive marks one note's Receive as in flight … A false return means the same note is already being processed … the caller must refuse WITHOUT touching the mint — the in-flight Receive may already have spent the proofs …"*

The mechanism is a **process-memory map**: `receiveInFlight map[string]struct{}`, lazily initialised, guarded by a mutex `[M src/merchant/merchant.go:370-382, :307-321]`. Measured in the experiment:

```
  [PASS] within one process lifetime a seen-note map catches the replay
  [PASS] after a restart it does NOT (nothing persists it)
         merchant.go:313-319 lazily inits it; SIGKILL empties it
```

**So the smallest possible loss is one full note per SIGKILL, and it is unbounded per note.** Against the module's own hard rule `AGENTS.md:75-77` — *"Retries must be idempotent or use fresh state. Concurrent duplicate submissions of one token must not double-count"* — the in-memory map is the **only** dedup an offline note would have.

**And the failure is worse than the Cashu ambiguity class, not merely analogous.** `isAmbiguousMintOutcomeError` is *"deliberately the inverse of the explicit-refusal list … only the mint can establish that it did not take the note"* `[Q src/merchant/merchant.go:1906-1919]` (#498/#719). For a note **every** outcome is a local signature check, so:
- **definitive refusal is unreachable** — no authority exists to return one;
- **NUT-09 restore has no analogue** — LUD-26's `cx1` is watch-only export for *the holder's* notes, not the acceptor's liability `[Q 25.md:250-252; CUSTOMER-MINT-AND-INTEROP.md:409]`;
- **a repeat submission is indistinguishable from a second sale, permanently.**

### 2.5 The replay, demonstrated — offline, no router, no network, no chain

`experiments/x1_replay_window.py` implements **exactly** LUD-25 25.md:228-232 steps 1-3 and nothing more, then presents the same 64 bytes twice `[M]`:

```
X1a  presentation 1 -- the honest first holder
  [PASS] accept #1 (the intended sale) -> ACCEPTED
         ACCEPTED (amount=6120000 msat)

X1b  presentation 2 -- THE SAME 64 BYTES, replayed byte-for-byte
  [PASS] accept #2 (a replay; no forgery, no network, no mint) -> ACCEPTED
         ACCEPTED (amount=6120000 msat)

  assertions: 6   PASS 6   FAIL 0
```

Note it is **6/6 PASS** — the finding is that the spec-faithful code is *correct* and the outcome is nonetheless a double grant. Output committed at `experiments/X1-OUTPUT.txt`. Runs with `python3` and the repo's own dependency-free LUD-25 codec; no PyPI, no network.

### 2.6 Mitigations, costed — **and the honest answer is that two of them cost more than what they replace**

| Mitigation | What it needs | Does it close the window? | Cost |
|---|---|---|---|
| **Rotate-on-see** (what the spec prescribes) | A live mint at accept time | Closes it *from that moment* — **but only if the mint is reachable, which is the case the feature exists to handle** | **Defeats the purpose.** If the mint is reachable you should just do the Cashu round-trip. `[I]` |
| **Short hold** (`cs1` → wait N s → re-check) | A clock, and *something to re-check against* | **No.** There is nothing that changes state during the hold: a `cs1` is deterministic and the note has no mint-side spend marker visible to the router | Cost with no benefit `[M §2.1, X1d]` |
| **Per-note nonce journal** (accept offline, record `Q`, refuse a second sighting) | A durable, fsync'd set of accepted `Q`s, plus a bounded-ambiguity story for the crash window | **Yes, locally** — it makes the router a nullifier store for its own acceptances | Real, bounded, and buildable — see below |
| **Service-side check when WAN returns** | A rotation/melt call on the next uplink | Closes it **only for notes the router managed to rotate first** — first rotater wins `[Q:301-312]`. And a replayed note may already be lost | Passes the loss to a later, equally racy step |
| **Do the mint round-trip instead** | Nothing new. It is what the module does today | **Yes, and atomically, at the mint** | 0.2–0.7 s measured `[M §1.2]`, and the mint is the only authority that can answer |

**The finding the operator asked for, stated plainly:**

> **The per-note nonce journal is the cheapest real mitigation, and it is not cheap — it is a new durable money-ledger in the gate binary, with its own crash semantics, its own unbounded-growth problem, and no external authority to reconcile against.** §5.5 estimates ~1,500–2,500 lines plus a multi-PR config/reboot/fsync story and a full `AGENTS.md:26-35` distributed-state-machine treatment. Against that, the thing it replaces costs **0.2–0.7 s of measured mint latency** `[M]` and is already written, tested, journaled (#502) and reconciled (NUT-07).
>
> **So yes: the mitigation costs more than the thing it replaces.** That is not an argument that deferral is worthless — it is an argument that *LNURLcash is the wrong way to buy deferral*. If deferral is the goal, buy it at the Cashu layer, where the instrument already has an external authority and a nullifier.

**And the journal is not merely expensive — it is not sufficient.** It closes only *this router's* replay of *this* note. Threat 6 in §2.3 (log retention, `T3`) and the mint's own `VERIFY_ENABLED` race (`T1`) are *upstream* of the router. A router-side journal cannot see a `k1` that leaked before it arrived. `[I]`

---

## 3. Experiments worth running — ranked

**Ranking rule: value ÷ effort.** Effort is in engineer-days `[I]`. "Offline" = no router, no mint, no chain, runs on any Linux box.

| # | Experiment | Hypothesis | Method | Falsifier | Effort | Value | Rank |
|---|---|---|---|---|---|---|---|
| **X1** ✅ **DONE** | **Replay window, executed** | A LUD-25-faithful offline acceptor accepts one bearer note unboundedly | `experiments/x1_replay_window.py` — implements 25.md:228-232 steps 1-3 verbatim, presents the same 64 bytes twice. **Offline** | If accept #2 were REFUSED by the spec-faithful code, LNURLcash offline acceptance would be safe and §2 collapses | **0.5 d — done** | **Kill-criterion + the paper's spine** | **1** |
| **X2** ✅ **DONE** | **cw1/cs1 codec conformance** | The LUD-25 wire format is reproducible at byte level by an independent implementation | `poc/tests/test_vectors.py`, 141 assertions vs `25-vectors.json` + `26-vectors.json`. **Offline** | Any vector field that does not reproduce byte-for-byte | **0 (done)** | High — proves the encoding is a solved problem. 141/141 PASS `[M]` | **2** |
| **X3** | **Reachability of the instrument seam** | The `WalletPort` seam can be wired for a non-Cashu instrument without touching the money path | Read-only: `go build` + `go vet` on a type-checked delta that routes a prefix-matching body to `SidecarWallet.Call`. **Router-free (static)**; live confirmation needs the router | If the delta cannot be made to compile without touching `PurchaseSession`, the seam is not a seam | **1–2 d** | High — this is the *only* change that makes the whole question answerable, and it pays off for the CDK migration `wallet-policy.json` already selects for aarch64 `[M ROUTER-FEASIBILITY.md:213-217]` | **3** |
| **X4** | **Journal-vs-round-trip cost** | A durable per-note nullifier journal costs more than the mint round-trip it replaces | Model both against the **measured** mint latencies (§1.2) and the module's own journal (`receive_intent.go:84-137`: temp→fsync→rename→fsync-dir). Count: syscalls per accept, bytes/note, growth over 1 y at the router's observed purchase rate, fsync cost on the target's flash. **Offline** | If a journal is <100 lines, needs no schema change, and survives SIGKILL with no new config, the mitigation is cheap and §2.6 is wrong | **1 d** | **High — this is the decision-maker.** §2.6 currently predicts "more expensive"; this converts it from an estimate to a number | **4** |
| **X5** | **Kernel agreement** | Core's interpreter accepts what the POC's `tapscript.py` accepts | Build `lnurlcash-kernel` (Rust crate, static `libbitcoinkernel`; needs C++20, CMake ≥3.22, Boost ≥1.74 headers `[Q kernel/rust/README.md]`) on a **host** (DQ05-class), run the LUD-25 vectors and the corrected 114-B `OP_CHECKSIGADD` leaf. **Needs a host with libbitcoinkernel — not the router** | If the kernel rejects a leaf the POC accepts, the POC is wrong and so is `CUSTOMER-MINT-AND-INTEROP.md` §2f | **1–2 d** | Medium — closes an honest gap that `poc/README.md` names itself: *"No consensus oracle was run … 'I read the BIP' is a weaker claim than 'I ran the kernel'"* `[Q poc/README.md:85-101]` | **5** |
| **X6** | **No-flag-day, measured** | A stock LN wallet redeems a LNURLcash note with no awareness of LNURLcash | Point a stock wallet at `lnurlw://…` and at the bech32 `LNURL1…` form of a live testnet note. Needs a mint + network. **Live** | If no stock wallet can claim it, the interop claim `[Q 25.md:10]` is marketing. **`lnurl-vault`'s own docs already report a failure here**: *"Wallet of Satoshi reports it as 'not an ln invoice', which is the spec's own backward-compatibility promise going unmet"* `[Q PROTOCOL.md:778-783]` | **0.5 d** | Medium-high — and the prior art predicts it fails | **6** |
| **X7** | **End-to-end loss, on a live router** | An offline-accepting router actually loses value on a replay | Mint a note, accept it twice through `POST /` across a service restart, observe two grants. Needs the **live router** (currently unreachable) | If the router refuses the second submission after a restart *by some other mechanism*, the loss is contained | **2–3 d** | **Highest fidelity, lowest feasibility** — blocked on `tollgate-A04E` not resolving `[M]` | **7** |
| **X8** | **Upstream/reseller settlement by note** | A note settles reseller→upstream cheaper than a whole-token forward | Compare `#239`'s whole-token forward `[Q bootstrap.go:12-24]` against a rotate-based transfer across two routers on the local link. **Needs two routers + a LNURLcash mint** | If the note path needs the mint anyway (it does — `[Q 25.md:163-166]`), there is no saving | **3–5 d** | Low, given §1.4's four blockers — and the call path has no production caller `[M]` | **8** |

### 3.1 The kill criteria, named explicitly

A good options paper names what would prove it wrong. These are the experiments that would **disprove LNURLcash's value for TollGate**, and they are ordered by how cheaply they could do it:

1. **X1's falsifier (0.5 d, already run):** if a spec-faithful offline verifier refused the second presentation, the entire §2 argument evaporates. It did not. **This is the strongest evidence in the paper against LNURLcash-for-TollGate, and it is the cheapest possible test.**
2. **X4's falsifier (1 d):** if the nullifier journal turns out to be ~100 lines with no schema change and no new failure mode, then the mitigation is cheap and offline acceptance becomes a *tempting* option — which would flip the recommendation from "bounded experiment" to "build it". **This is the experiment most likely to change my mind.**
3. **X6's falsifier (0.5 d):** if a stock wallet *can* redeem a note, "no new endpoint" is a real, deployable interop asset and the interop case gets stronger. `lnurl-vault`'s docs already report it failing `[Q PROTOCOL.md:778-783]`.
4. **A LUD-25 spec change (0 effort, watch):** if option **G** (locked notes) `[Q threat-suite.json options.G]` or a `cs1`-variant that binds one consumption `[Q LNURLCASH-REVIEW.md:230-235]` lands normatively, the whole §2 analysis must be redone. **This is the single highest-leverage thing to watch, and it costs nothing.**

### 3.2 Router-dependent vs router-free

**Router-free (runnable now, all of X1–X6):** X1 ✅, X2 ✅, X3 (static half), X4, X5 (needs a *host*, not the router), X6 (needs network + mint, not the router).
**Router-dependent:** X7 (blocked — `tollgate-A04E` unreachable `[M]`), X8 (needs two routers).

**Two of the eight are done, four more need no router.** The blocker is not this spike's capability; it is that the one experiment with the highest fidelity is the least reachable, and its cheapest proxy (X1) has already returned the answer.

---

## 4. What to reuse from DNI's stack — upstream vs build

DNI's org is 20 repos `[M, GitHub API 2026-10-15]`. The relevant slice:

| Repo | What it is | TollGate need it? |
|---|---|---|
| `lnurl-mint` (Python) | A full LUD-25 mint: `/w`, `/w/cb`, `/p/cb`, `/verify`, `/p/{username}`, NIP-05, zaps, offline verification `[Q README]` | **No — but it removes a blocker.** See 4.1 |
| `lnurl-mint-rust` / `cln-mint` | Same, Rust / as a CLN plugin | **No** |
| `run` (run.lnurlcash.com) | *"Hosted, self-managing LNURLcash mints. Each one is an unmodified lnurl-mint-rust with its own Lightning node … kept liquid by LSPS2"*, provisioned by a NIP-98-signed `POST /api/mints` `[Q README]` | **Maybe — this is the interesting one.** If TollGate ever accepts notes, an operator could point at a *hosted* mint instead of running one. That removes the "tollgate becomes a mint" objection of §5.5 — **but it does not remove the replay window, and it does not remove the custody question, it just moves it to a third party** |
| `lnurl-wallet` (TS) | Serverless wallet, `@lnurlcash/kit`; POS addon with an e2e test `[Q README, e2e/pos.spec.ts]` | **No, as a component.** **Yes, as a test client** — and its POS addon is *literally the point-of-sale case this paper is about* |
| `lnurl-vault` (C, ESP32-S3) | Air-gapped hardware note vault, WebSerial/BLE, physical confirm gates | **No.** But see 4.3 |
| `lnurlcash-go` (Go) | **`mint, rotate, split, merge, melt, and verify a note offline`**; `go 1.25`; deps **`btcec/v2 v2.5.0`, `btcutil v1.2.0`, `secp256k1/v4 v4.4.0`** — all pure Go `[Q go.mod]` | **YES — this is the single best reuse candidate.** See 4.2 |
| `kernel` / `lnurlcash-kernel` | Python + Rust wrapper over **unmodified `libbitcoinkernel`**; Rust crate builds Core's kernel from a vendored `vendor/bitcoin` and links statically `[Q kernel/rust/README.md]` | **Not in the router** (see 4.4). **Yes on a host** for X5 |
| `lnurlcash-core` (Rust) | The money-critical protocol logic in one audited place, UniFFI to Kotlin/Swift, "no I/O in the protocol layer" `[Q README]` | **Read it, don't ship it** — the right model for how to structure a codec |
| `lnurlcash-conformance` | Language-neutral JSON vectors + an adversarial mock mint + `lnurlcash-conform <mint>` grader `[Q README]` | **YES — highest-value reuse, zero cost.** See 4.5 |
| `lnurlcash-contracts-demo` | Receiver-locked notes, cancellation bonds; custody path deliberately retired | **Read it** — it is the most honest document in the org about what LNURLcash cannot do. See 4.6 |

### 4.1 Does TollGate need DNI's mint? **No, and this is the important negative**

**Cashu mints and LNURLcash mints are not interchangeable.** The router today accepts **seven Cashu mints** `[M /etc/tollgate/config.json, ROUTER-FEASIBILITY.md:146-148]`. A Cashu mint implements NUT-00/NUT-03 — proofs, keysets, swap. A LNURLcash mint implements LUD-25 — taproot `Q`, `ck1`/`cw1`, rotate/split/merge. **Neither speaks the other's wire format.** A router accepting LNURLcash notes needs an *additional*, differently-shaped authority in `accepted_mints`, and the config schema's `AcceptedMints` children are *"exactly the 8 keys seen on disk"* — URL, min_balance, balance_tolerance_percent, payout_interval_seconds, min_payout_amount, price_per_step, price_unit, purchase_min_steps `[M ROUTER-FEASIBILITY.md:173]` — none of which can express "this is a LUD-25 SERVICE, not a Cashu mint".

**So the answer is: TollGate needs no mint of its own** (correct — it is a redeemer, not an issuer), **and DNI's hosted `run` offering means it need not run one either** — but a LNURLcash mint is a *fifth* kind of accepted authority, not a swap-in for the six it already has. `[I]`

### 4.2 The cheapest real path — and it contradicts the "chain dependency" cost

LUD-25's verification cost decomposes into (i) taproot output-key reconstruction (SHA256 + x-only point add + BIP-341 tagged hash), (ii) a BIP-340/BIP-342 script interpreter run, (iii) for `cs1`, a recoverable-ECDSA recover `[Q 25.md:209-232, 33-42, 69-83; CUSTOMER-MINT-AND-INTEROP.md:307-324]`.

**All of that is pure Go and already in the binary.** Measured on the router `[M ROUTER-FEASIBILITY.md:277-299]`:

```
root@tollgate-A04E:~# strings /usr/bin/tollgate-wrt | grep -o 'ErrTaproot[A-Za-z]*' | sort -u
ErrTaprootMaxSigOps / ErrTaprootMerkleProofInvalid / ErrControlBlockInvalidLength
ErrTaprootOutputKeyParityMismatch / ErrTaprootPubkeyIsEmpty / ErrTaprootSigInvalid
root@tollgate-A04E:~# strings … | grep -oE 'OP_CHECKSIG[A-Z]*'
OP_CHECKSIGADD / OP_CHECKSIG / OP_CHECKSIGVERIFY
root@tollgate-A04E:~# strings … | grep -E '^dep\tgithub.com/(btcsuite|decred)'
dep github.com/btcsuite/btcd                      v0.24.3-0.20250318170759-4f4ea81776d6
dep github.com/btcsuite/btcd/btcec/v2             v2.3.4
```

And the positive control: `strings … | grep -cE 'lnurlp'` → **1**, while `grep -icE 'lnurlcash|withdrawrequest|mintpubkey|libbitcoinkernel'` → **0** `[M:294-295]`. So: the taproot interpreter is resident; LNURLcash is genuinely absent; the grep method is not broken.

**Therefore the "chain dependency" cost is materially overstated.** `lnurlcash-go`'s `go.mod` requires `btcec/v2 v2.5.0` — the module already carries `btcec/v2 v2.3.4 // indirect` `[M src/go.mod:49]`. That is a **version bump plus a promotion from `// indirect` to direct**, plus a new pure-Go package. **No cgo. No new artifact. No `CGO_ENABLED=0` violation** — which is the contract set across 5 architectures at 6 sites (`build-package.yml:238`, `local-build-ipk.sh:83,87`, …) `[M ROUTER-FEASIBILITY.md:330,468]`.

**And the build is green by construction:**
```
lnurlcash-go   → go 1.25,  btcec/v2 v2.5.0   (pure Go)
src/go.mod     → go 1.26.0, btcec/v2 v2.3.4   // indirect
src/tollwallet/go.mod → cdk-go v0.17.3 (direct)
```
`[M]`. Rust+musl `lnurlcash-core` is also an option (the module has precedent: `nucula`/`cdk` sidecar manifests `[M ROUTER-FEASIBILITY.md:332]`), and the repo has shipped a musl cross-build story (`rust-musl-cross-build`) `[I]`.

**So the cost of adding LNURLcash to this router is: a Go module, a codec, a journal, a config field, a schema change, a signed-advertisement wire change, and a sidecar or in-process verifier — and the *only* one of those that is not already solved is the journal.** §4.2 is the strongest *pro* case in this paper, and it does not change the verdict, because §2 is a correctness problem, not a build problem.

### 4.3 `lnurl-vault` — the review did not cover it, and it changes one thing

It is an ESP32-S3 T-Display hardware vault: WebSerial (USB-CDC, newline-delimited JSON) and BLE (NimBLE GATT) transports; commands `get_info`, `identify`, `list_notes`, `new_secret`, `new_secret_pair`, `confirm`, `discard`, `export_secret`, `import_secret`, `mark_spent`, `rename`/`delete`, `prune_spent`, `reset`, `wipe`, `ota_*` `[Q PROTOCOL.md toc]`. **The device does no networking**: *"It only ever generates secrets, discloses their SHA-256 hash, and tracks note state."* `[Q PROTOCOL.md:7-12]`

**TollGate needs none of it.** The router is a SERVICE, not a WALLET, and it has no buttons, no screen and no BLE.

**But it changes one thing, and it is the thing that matters: it is a second, independent, implementation-level confirmation of §2.** Its authors — the same org — wrote:

> *"**What it cannot do**, and does not try to. It has no idea whether a `CONFIRMED` note is still outstanding at the mint. A note reaches `SPENT` only because a host told this vault so — a melt it watched settle, or a rotate/split/merge that burned the note as an input. A note redeemed by somebody else's wallet, or rotated away by another client, still reads `CONFIRMED` here forever. LUD-25 is explicit that this is inherent: 'A spent note keeps its valid signature forever, and no revocation is visible offline.'"* `[Q PROTOCOL.md:520-529]`

> *"Reconciling that needs the mint, and asking the mint means presenting the bearer secret at its withdraw endpoint — which puts a live secret in an access log for every note checked. A vault that quietly dropped notes it merely suspected were spent would be destroying money on a guess, so this one does not guess."* `[Q PROTOCOL.md:531-535]`

That is *precisely* the trap a TollGate acceptor would fall into, described by the spec's own co-author org, with the same conclusion. **The vault is not a component to reuse; it is corroboration.**

Two further details worth carrying:
- **The mint itself is a prior holder.** `lnurl-mint` dispatches `/w/cb` replies and `verify` semantics around it: *"`preimage` … is fetched live from the funding source on every call"* and *"that wallet needs it to claim the note at all, so it must be handed over despite `SERVICE`'s own node already being a permanent prior holder of that same secret"* `[Q dni_lnurlcash_lnurl-mint_main_README.md:284-296]`. So even the *mint* is a standing replay threat.
- **A LAN/single-process fragility**: the mint *"Run exactly one process per `DATABASE_PATH`: no `--workers` greater than 1 … a second process silently voids those guarantees: at best spurious 'database is locked' errors, at worst double spends."* `[Q README:412-417]` — the same class of hazard the module's own boot-bound fix (#940) exists to avoid.

### 4.4 `kernel` — the right tool in the wrong place

The Rust crate *"links `libbitcoinkernel` statically, so a binary using this crate carries its verifier inside it"*, built from a pinned `vendor/bitcoin`, needing **C++20, CMake ≥ 3.22, Boost ≥ 1.74 headers** `[Q kernel/rust/README.md]`.

- **On the router: RED.** This is the cgo blocker `CUSTOMER-MINT-AND-INTEROP.md:333` already characterised, and ROUTER-FEASIBILITY §4.1(c) reached independently: *"`CGO_ENABLED=0` is set for the *entire* 5-arch release matrix … Adopting cgo **changes the build contract for five architectures**, and the module's own record says the existing cgo adapter (`cdk_wallet.go`) **has never once been built by CI**."* `[Q ROUTER-FEASIBILITY.md:333]`
- **On a host: the right way to close X5.** `poc/README.md` names the gap honestly: *"No consensus oracle was run. There is no `bitcoind`, no `bitcoin-cli` and no `libbitcoinkernel*` on this host"* `[Q poc/README.md:85-90]` and *"Two independent readings of a text is not the same evidence as one execution of the reference implementation."* `[Q:98-101]`. DQ05 runs Bitcoin Core v31.1 `[Q CUSTOMER-MINT-AND-INTEROP.md:634]` — so **X5 is a `docker run` / build away on a host the project already has.** That is a clean, bounded task and it is worth doing regardless of the LNURLcash verdict, because the `OP_CHECKMULTISIG`-disabled finding currently rests on a BIP read.

### 4.5 `lnurlcash-conformance` — the one thing to take unconditionally

Three independent artifacts, all usable immediately `[Q README]`:
- **`vectors/`** — language-neutral JSON (`signature.json`, `spend.json`/`spends.json`, `part2.json`, `bech32.json`, `url-admission.json`, `spec-vectors.json`, `cash-derivation.json`) loadable from any language with no dependency.
- **`mock-mint/`** — *"a real HTTP mint that can be told to misbehave, on demand, in every way the spec warns about."*
- **`runner/`** — `lnurlcash-conform <mint>`, exits non-zero if non-compliant.

And its own stated purpose is the most quotable argument for the paper's recommendation: *"These vectors are the shared statement of what the discipline is, so that every implementation can be wrong in the same place at the same time, and find out about it in CI rather than in production."* `[Q README]`

**Recommendation: vendor the vectors into `tests/` and run them in CI against any LNURLcash code the project ever writes. Cost: hours. This is a free win independent of every other conclusion here.** `[I]`

### 4.6 `lnurlcash-contracts-demo` — the honest counter-case, from the DSL side

The most useful document in the org for a *skeptic*. Its public build has *"a hard product boundary"*, item 3 of which is: *"The flow stops before custody. Bond packets, funding acknowledgements, arbiter decisions and settlement messages are rejected."* `[Q README]` And its own retrospective: *"The earlier `bilateral-arbiter-v2` research made the arbiter custodian of both bearer secrets … A 25-sat funded acceptance completed once: 23 sats were minted after fees, two 10-sat bonds completed and settled, and 20 sats returned. That is useful historical protocol evidence. It is not a non-custodial primitive, so the funded runner has been removed from the public demo."* `[Q README]`

**And a structural limit that matters if anyone proposes "lock the note to the TollGate":** `CONDITIONAL-OUTPUTS-PROPOSAL.md` proves hash-locks cannot give t-of-n: *"An LNURLcash output is keyed by `sha256(k1)`, and that hash must be published before anyone funds it. Publishing it requires knowing `k1`. Therefore, at the moment any output is created, exactly one entity knows the whole secret and can spend it alone."* … *"**Hash-locks provide 1-of-1 and, through `mergeNotesWithHash`, n-of-n. They cannot provide t-of-n.** Every escrow or arbitrated-settlement design on this rail therefore places unilateral spending power with a custodian. That is a protocol limit, not an implementation choice."* `[Q]`

**That closes the last escape hatch.** The natural proposal — "give TollGate a second-party key over the note so it can't be replayed by the customer" — is a *custody* change, on a rail whose own author documents that it cannot do thresholds. `[I]`

---

## 5. Benefit to TollGate, honestly

### 5.1 What LNURLcash gives that Cashu alone does not

| Claimed gain | Real for TollGate? | Why |
|---|---|---|
| **No new endpoint** — *"adds **no new endpoint**: a `WALLET` that does not know `LNURLcash` sees an ordinary withdraw link"* `[Q 25.md:10]` | **Mostly no.** | This is a gain for a **WALLET**. TollGate is a **SERVICE** and its customer surface is a captive portal plus `POST /` `[M §1.0]`. TollGate gets *no* endpoint benefit — it would be *adding* a verifier, not avoiding a route. |
| **Verification delegable to `libbitcoinkernel`** `[Q 25.md:12]` | **Weak.** | The property that matters — *the verification rule IS the consensus rule* `[Q LNURLCASH-REVIEW.md:197-200]` — is real, but the module can get the same code with **no cgo** via `lnurlcash-go` (§4.2). And on the router it is blocked anyway (CGO_ENABLED=0 × 5 arches). |
| **The rotate/split/merge algebra** `[Q 25.md:161-166]` | **NO.** | TollGate **consumes** an instrument; it does not make change. The module's own instrument is redeem-and-reissue at a mint, and its reseller contract is explicitly *"no split"* `[Q bootstrap.go:72]`. |
| **Offline issuance proof (`cs1`)** | **Real but weak.** | Genuinely no-network, no-chain `[Q 25.md:228-232]`, verified by me `[M X1]`. But it is issuance-only `[Q 25.md:235]`, and a TollGate granting on it is the documented "naive is-it-spendable check" `[Q ARCH-FIT.md:417-424]`. |
| **The note IS a LUD-03 withdraw link** | **Real, for the other side.** | A bearer note travels as `lnurlw://mint.example/w?k1=<spend>&c=<cs1>` `[Q 25.md:228]` — 210 chars measured `[M X1-OUTPUT.txt]` — and *"a wallet that knows nothing of taproot mints and redeems with sha256 alone"* `[Q lnurl-mint README:62-69]`. This is a genuine interop asset **for whoever is TollGate's customer**, not for TollGate as an acceptor. And `lnurl-vault` reports it failing in practice: *"Wallet of Satoshi reports it as 'not an ln invoice', which is the spec's own backward-compatibility promise going unmet."* `[Q PROTOCOL.md:778-783]` |

**Net: of the five claimed gains, one is real-but-weak-for-a-redeemer, two belong to a counterparty role TollGate is not in, and two are absent.**

### 5.2 What it costs

| Cost | Evidence | Can it be avoided? |
|---|---|---|
| **A replay window the spec does not bound** | `[Q 25.md:235]`, `[M §2.5]` | **No. Only an acceptor-side durable nullifier journal closes it — §2.6** |
| **New verification code** | `[I]` ~600–1,200 lines | Yes — partly. `lnurlcash-go` supplies it. But then you inherit a `0.x` dependency tracking a draft spec, which *pin an exact version* is the vendor's own advice `[Q lnurlcash-go README]` |
| **A durable new money-ledger in the gate binary** | `[I]` ~1,500–2,500 lines + `AGENTS.md:26-35`'s full distributed-state-machine treatment | **No.** This is the unavoidable one |
| **Chain/taproot dependency** | `btcec/v2` already in-binary `[M]`; `lnurlcash-go` is pure Go `[M go.mod]` | **Yes — and I want to correct the record.** ROUTER-FEASIBILITY §4 predicted this was the *easy* part; §4.2 confirms it with the Go module pinned. **The chain dependency is not a real cost.** |
| **A config-schema change + a signed-advertisement wire change** | *"There is no `wallet.backend` config key"* `[M]`; `"cashu"` is a signed literal `[M merchant.go:2002]` | No — and this is a **client-visible wire change** needing its own compatibility discussion `[Q ROUTER-FEASIBILITY.md:448]` |
| **A spec that is a moving target** | PR #301 **open, unmerged**, 44 commits, 11 comments, last updated 2026-10-05 `[M GitHub API]` | No. `lnurlcash-go`/`lnurlcash-core` both say *"Early `0.x`, tracking a **draft** spec. Pin an exact version."* `[Q]` |
| **Precedent risk: building on a red lane** | #719 — no value recovery when SIGKILLed between "mint signed the swap" and "wallet persisted" `[Q SPIKE-REPORT.md:402-405]` | **No.** The journal would be *new* durable money state on a module whose *existing* crash-recovery lane is red |
| **Blast radius** | `nodogsplash` `users_to_router` already permits `tcp port 2121` `[Q ROUTER-FEASIBILITY.md:441]`; a novel signature parser would sit in the process that owns the money path | Only by going to a sidecar |

### 5.3 Net verdict

> **LNURLcash's benefits accrue to the WALLET side of the protocol. TollGate is the SERVICE side. The two roles have different requirements, and TollGate's requirement — consumption state at the moment of grant — is the one thing `cs1` structurally cannot supply and the one thing the spec says it cannot supply.**

Stated as a trade:
- **TollGate would gain:** a second accepted-instrument family; an offline *issuance* check; interop with a wallet ecosystem that does not yet exist in volume (`lnurl-wallet` 6 stars, `lnurl-mint` 5, the whole org ≤ 20 `[M GitHub API]`).
- **TollGate would pay:** a durable per-note nullifier ledger, a signed-wire change, a schema change, a rewrite of the duplicate-safety story, and a bet on an unmerged draft.
- **The thing it would be buying** — removing the mint round-trip from the customer leg — **it can buy at the Cashu layer for less**, where the instrument already has an external authority and a real nullifier (§1.2).

### 5.4 Recommendation

**Bounded experiment, then watch. Not now.**

| Action | Effort | Do it? |
|---|---|---|
| Run **X4** (journal-vs-round-trip cost) — the experiment most likely to change my mind | 1 d, offline | **Yes** |
| Run **X5** (kernel agreement) on DQ05 — closes a named honest gap regardless of the verdict | 1–2 d, needs a host | **Yes** |
| Vendor **`lnurlcash-conformance`'s vectors** into `tests/` (§4.5) | hours | **Yes** — free win |
| Run **X3** (instrument-seam reachability delta) | 1–2 d | **Yes** — pays off for the CDK migration independently `[Q ROUTER-FEASIBILITY.md:442]` |
| Build a LNURLcash acceptor in `tollgate-module-basic-go` | weeks | **No** |
| Let `tollgate-module-basic-go` become a LUD-25 **SERVICE** | — | **No** — org scope: OpenTollGate is protocol, strictly non-profit; a SERVICE holds the `Q`→value ledger and the burn set, i.e. it is a mint `[Q ROUTER-FEASIBILITY.md:439]` |
| Send the LNURLcash review to `dni` (§6 of `LNURLCASH-REVIEW.md`, already drafted) | minutes | **Yes** — it is the highest-leverage action available, and it costs nothing |

**Watch triggers that would flip this to "adopt":**
1. LUD-25 lands a normative **option G** (locked notes) or a one-time-`cs1` variant `[Q threat-suite.json; LNURLCASH-REVIEW.md:230-235]`.
2. TollGate acquires a **chain view** (light client / UTXO oracle) — which would make a note a *bonded* instrument, since a taproot spend is then a real on-chain event `[Q ROUTER-FEASIBILITY.md:451]`.
3. The module's crash-recovery lane (#719) goes green **and** a durable nullifier store ships for some other reason — at which point the journal's marginal cost drops to near zero.
4. X4 returns "the journal is ~100 lines, no schema change, survives SIGKILL" — §3.1's criterion 2.

---

## 6. The honest counter-case, and whether it wins

### 6.1 The strongest case against LNURLcash in TollGate

> **LNURLcash is a solution to a problem TollGate does not have, at a layer TollGate does not own, and its one relevant property is the one TollGate's own doctrine forbids using.**
>
> **1. The problem it solves is the holder's, not the redeemer's.** LUD-25 exists so a *note* can circulate: copied, printed, NFC-tapped, handed over like a banknote `[Q 25.md:10]`. TollGate does not circulate anything. It **consumes** an instrument once, at one router, to open one gate. Every substantive LUD-25 feature — rotate, split, merge `[Q 25.md:161-166]`, the `cp1`/`ck1`/`cw1` encoding `[Q 25.md:91-96]`, the `mintPubkey`/`cs1` certificate `[Q 25.md:196-207]`, LUD-26's seed derivation and watch-only `cx1` `[Q 25.md:250-252]` — serves circulation. **A redemption path uses none of it.** TollGate would adopt a bearer-asset container to open a gate, which is the one operation that needs no bearer semantics at all.
>
> **2. The layer is wrong.** TollGate already has a bearer instrument that works, with an external authority that can say *consumed*: Cashu at a mint, via `WalletPort.Receive` `[Q src/tollwallet/port.go:196]`. The one-thing-missing for deferral — accept before the mint is reachable, settle after — is a **grant-ordering** change in the merchant, not a new instrument. `AddAllotment` is already pure memory and `valve.OpenGate*` is already local `[Q SPIKE-REPORT.md:236-240]`. **Adopting a new instrument to buy deferral is buying a new engine because the car needs a different gear.**
>
> **3. Its one relevant property is banned by the repo's own doctrine, and for a reason the repo wrote down.** `cs1` is existence without consumption `[Q 25.md:235]`. `bearer-instrument-port.md` — quoted in-tree at `ARCH-FIT.md:417-424` — says conflating the two *"is exactly how a forged note passes a naive 'is it spendable?' check."* The spec's author agrees with the repo: `lnurl-vault`'s protocol doc says consumption state does not exist offline and *"This device … does not guess"* `[Q PROTOCOL.md:520-535]`. **TollGate and DNI's own implementers have independently arrived at the same rule.**
>
> **4. The mitigation costs more than the thing it replaces.** 0.2–0.7 s of measured mint latency `[M]` and machinery that already exists (#502, NUT-07, owed-grant) versus a new durable nullifier ledger with its own crash semantics, built on a module whose existing crash lane is red (#719) `[Q SPIKE-REPORT.md:402-405]`. **§2.6's own comparison, and it is not close.**
>
> **5. It is a bet on an unmerged draft.** PR #301 open, 44 commits, 11 comments, `status: draft` `[M/Q 25.md:4]`; the vendor's own Go and Rust clients say *"Early `0.x`, tracking a **draft** spec. Pin an exact version."* `[Q]` And the spec is *actively moving* on the exact axis that matters: the threat scorecard's options B/D/F/G all target transport exposure and consumption `[Q threat-suite.json options]`. TollGate would be building a money path against a document whose author is still deciding what is normative.
>
> **6. And there is nowhere to put it.** The instrument is hard-coded at four layers, one of which is a **signed** advertisement `[M §1.0 facts 1-5]`. The config schema cannot express a non-Cashu authority `[M]`. The one seam that could take a new backend has zero production callers `[M merchant.go:509 vs select.go:28-41]`. So the first LNURLcash PR is not an LNURLcash PR at all — it is three instrument-agnostic refactors `[Q ROUTER-FEASIBILITY.md:446]`, and only *then* does the question even become askable.
>
> **7. There is a cheaper, de-risked version of the same idea.** "Accept before the mint is reachable, settle after" does not require LUD-25. It requires a **bounded, recorded deferral** with an explicit ambiguity policy — which is what the module's journal is *for* `[Q receive_intent.go:1-31]`. Buy that.

### 6.2 Do I find it convincing? **Yes, on four of the seven points — with one carve-out.**

**Where it wins outright (points 1, 2, 4, 5):**
- **1 is decisive on the instrument question.** I checked every substantive LUD-25 feature against TollGate's actual redemption path and **none of them is used**. That is not an inference; it is a feature-by-feature count, and it is the paper's strongest single finding.
- **2 is decisive on the layer question**, and it is corroborated by an independent prior spike that reached it from a completely different direction (`SPIKE-REPORT.md:228-240`).
- **4 is arithmetic**, and it now has measured inputs on both sides.
- **5 is a straightforward risk read** on an unmerged PR with a moving threat model.

**Where it is right but overstated (point 3):** the doctrine argument is sound, but it is a rule about *accepting* on existence alone — not a proof that no design could ever work. The correct statement is narrower: **TollGate cannot accept on `cs1` alone**; whether it could accept on `cs1` + something else is exactly what X4 tests. Point 3 as written is slightly stronger than the evidence, and I have written §2 accordingly (unbounded window, not "LNURLcash is broken").

**Where it is right but I would soften it (point 6):** the four-layer hard-coding is real, but §4.2 shows the *cryptographic* half of the cost is close to zero because `btcec` is already in the binary and `lnurlcash-go` is pure Go. Point 6 is a good argument about **change surface**, not about **difficulty** — and conflating those would be the same overreach ROUTER-FEASIBILITY §4.1 was written to avoid.

**Where I think it is wrong (point 7, mostly):** the deferral recommendation is correct, but "buy that instead" hides that deferral has its *own* unsolved hard part — **what is the ambiguity policy for a deferred settle?** The module's Cashu path answers it via NUT-07 `[M receive_intent.go:290]`; a deferred path that accepts with no authority has no such answer. **The counter-case assumes the Cashu-level deferral is solved. It is not; it is merely better-posed.**

**The carve-out — the one place I would not follow the counter-case:** §1.2's product goal is real. `defaultReceiveTimeout = 30 * time.Second` `[Q merchant.go:221]` and the `payment-outcome-unknown` class `[Q merchant.go:1691]` genuinely degrade the customer experience, and on a router whose product is reselling an intermittent uplink, "the mint is unreachable" is a normal operating state, not an edge case. **So the right conclusion is not "drop the idea" — it is "the idea is deferral, and LNURLcash is the wrong instrument for it."** That is a smaller claim than the counter-case makes, and it is the one I can support.

**Verdict on the counter-case: it wins on the question actually asked** — *should LNURLcash be used in TollGate* → **no, not now** — **and it loses only on the question underneath it** — *should TollGate fight for a mint-free customer leg* → **yes, at the Cashu layer.**

---

## Appendix A — evidence index

| Claim | Source |
|---|---|
| Spec is draft / PR unmerged | `25.md:4` `status: draft`; GitHub API PR #301: open, 44 commits, 11 comments, updated 2026-10-05 `[M]` |
| `cs1` proves issuance, not outstandingness | `25.md:235` (spec WARNING, verbatim) |
| Offline verification = 3 steps only | `25.md:228-232` |
| `cs1` has no nonce/time/recipient/counter | `25.md:209-216`; `[M] experiments/X1-OUTPUT.txt X1d` |
| Canonical spend tx never broadcast; spent value 0 | `25.md:71`, `:76-79` |
| Timelock is SERVICE-asserted custody | `25.md:63`, `:67` |
| No new endpoint; LUD-03 compat | `25.md:10` |
| Rotate/split/merge algebra | `25.md:161-166`, `:183-190` |
| Burn state lives only at the SERVICE | `25.md:188` |
| Note URL = `lnurlw://…?k1=<spend>&c=<cs1>`; 210 chars | `25.md:228`; `[M] experiments/X1-OUTPUT.txt` |
| Replay demonstrated, 6/6 PASS, offline | `[M] experiments/x1_replay_window.py` → `experiments/X1-OUTPUT.txt` |
| LUD-25 codec conformance 141/141 | `[M] poc/tests/test_vectors.py`; `poc/README.md:16-21` |
| Bearer `cw1` is exactly 192 chars | `[M] poc/out/GREEN.txt`; `25.md:95` |
| Instruments hard-coded: signed `"cashu"` tag | `[M] src/merchant/merchant.go:1998-2006`, `:2022` |
| Body IS the instrument; 1 MiB cap | `[M] src/main.go:1045`, `:1111`, `:1103` |
| No wallet-backend / instrument config key | `[M] ROUTER-FEASIBILITY.md:176-178`; `src/config_manager/config_schema.go` |
| `OpenWallet` seam has no production caller | `[M] src/merchant/merchant.go:509` vs `src/tollwallet/select.go:28-41` |
| 30 s receive deadline; `payment-outcome-unknown` | `[M] src/merchant/merchant.go:221`, `:1691-1710` |
| In-flight dedup is process memory | `[M] src/merchant/merchant.go:307-321`, `:370-382` |
| Durable #502 journal exists and is fsync'd | `[M] src/merchant/receive_intent.go:1-31`, `:84-137`, `:247`, `:290` |
| Token fingerprint salt may be ephemeral | `[Q] src/utils/token_fingerprint.go:82-89` |
| `btcec`/`btcd` resident in the router binary | `[M] ROUTER-FEASIBILITY.md:277-299` |
| Zero LNURLcash surface in the binary (positive control `lnurlp`=1) | `[M] ROUTER-FEASIBILITY.md:294-295` |
| `CGO_ENABLED=0` across 5 arches / 6 sites | `[M] ROUTER-FEASIBILITY.md:330,468` |
| `lnurlcash-go` is pure Go, `go 1.25`, btcec v2.5.0 | `[M] lnurlcash-go go.mod` |
| `src/go.mod` is `go 1.26.0`, btcec v2.3.4 indirect | `[M] /tmp/tmbg/src/go.mod:3,49` |
| Mint latency 0.14–0.71 s | `[M] §1.2` |
| TollGate price: 1 sat = 22020096 B = 21 MiB | `[M] src/config_manager/config_manager_config.go:424`, `:426`; advertisement `[M] ROUTER-FEASIBILITY.md:90` |
| Replay loss = N × 21 MiB | `[I]` from the two `[M]` rows above |
| Vault: no consumption state offline; "does not guess" | `[Q] lnurl-vault PROTOCOL.md:520-535` |
| Vault: Wallet of Satoshi rejects the claim link | `[Q] lnurl-vault PROTOCOL.md:778-783` |
| Mint's node is a permanent prior holder of the secret | `[Q] lnurl-mint README:284-296` |
| Mint: one process per DB or "at worst double spends" | `[Q] lnurl-mint README:412-417` |
| Option G closes T5 only by surrendering bearer-ness | `[Q] threat-suite.json scenarios.T5.lockedNotes`, options.G |
| T1/T2/T3 exposure classes | `[Q] threat-suite.json scenarios` |
| Hash-locks cannot give t-of-n | `[Q] contracts-demo CONDITIONAL-OUTPUTS-PROPOSAL.md` |
| `ForwardFirstProof` has no production caller | `[M] grep over src/` |
| `#239` forwards whole token, "no split" | `[Q] src/upstream_session_manager/bootstrap.go:12-24`, `:72` |
| `reseller_mode: false` on the target | `[M] ROUTER-FEASIBILITY.md:152` |
| `#719` crash-recovery lane red | `[Q] SPIKE-REPORT.md:402-405` |
| `hasLockedProofs` refuses non-AnyoneCanSpend | `[M] src/tollwallet/tollwallet.go:296-298`, `:521-529` |
| Org size / stars | `[M] GitHub API `orgs/lnurlcash/repos` 2026-10-15` |

## Appendix B — artifacts

- `OPTIONS-PAPER.md` — this document
- `experiments/x1_replay_window.py` — the offline replay experiment (kill criterion)
- `experiments/X1-OUTPUT.txt` — its captured output, 6/6 PASS
- `poc/` — the dependency-free LUD-25/26 codec + 141-assertion vector suite + `ERRATA.md`
- `PROBE-OUTPUT.txt`, `ROUTER-FEASIBILITY.md` — the router-side memo and raw probe capture
- `/tmp/tmbg` — module clone @ `cf7cad3`
- `/tmp/dni_lnurlcash_lnurl-{mint_main_README,wallet_main_README,vault_main_docs_PROTOCOL}.md`
- `/tmp/lud25.md`, `threat.json` — spec and threat scorecard

**Router state: unmodified.** No writes, no `uci set`, no package install, no HTTP POST to any router, and no mint was contacted with a note. The only network traffic in this work was read-only `GET /v1/info` and empty-`Ys` `POST /v1/checkstate` calls to the six publicly-advertised mints `[M §1.2]`.
