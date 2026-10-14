# CODEFEAS — what a customer must be able to do, with no internet and local access

**Module read:** `OpenTollGate/tollgate-module-basic-go`
**HEAD read:** `cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` (branch `main`, 2026-10-14 12:43:17 +0200,
`fix(tollwallet): bound each mint registration so a slow mint cannot stall boot (#940)`).
Clone used: `cd /tmp && git clone --depth 1 https://github.com/OpenTollGate/tollgate-module-basic-go`,
work tree `/tmp/spike/tmbg` (the plain `/tmp` clone path fails: `/tmp/tmbg/.git/config` could not
be created on that mount; retried under a fresh subdirectory). Working tree clean.
This is the same tip recon used (cf7cad3), so every claim below is against that commit.

**Scope:** mechanism only, read from the code. Every file:line below was re-read with `sed -n 'Np'`
before it was written down; quotes are verbatim.

---

## 0. The one-line answer, before the detail

The module's customer-facing money path has **exactly one** shape, and it is *router-mediated*: the
customer hands the router a Cashu token, the router **spends that token at a mint on the customer's
behalf** (`Receive` → swap), and only then does the router grant local allotment and open the NDS
gate. There is **no** code path in which the customer's purchase completes without the *router*
reaching a mint. A one-way channel could remove the mint from the **customer's** path — and the code
says the mint was never on the customer's path in the first place. It is on the **router's** path,
in every branch, including the reseller cold-start branch (contra the recon hypothesis that #239
avoids the mint round-trip).

---

## 1. Exact execution path of a purchase, per-step, with reachability requirements

Two purchase wires exist today. Both converge on the same `PurchaseSession` core.

### 1a. Wire A — Cashu token submit: `POST /`

Route: `mux.HandleFunc("/", …)` → `RateLimitMiddleware(CorsMiddleware(requireStarted(HandleRoot)))`
(`src/main.go:1801-1804`) → `HandleRoot` (`src/main.go:1216`) → `HandleRootPost`
(`src/main.go:1060`).

| # | Step | file:line | Requires |
|---|---|---|---|
| A1 | Identity resolved **from the socket**, not from the body; `mac` param ignored | `src/main.go:1092` `macAddress, err := clientIdentity(w, r, "")` | **(c) LAN only** (ARP/`/proc/net/arp` at the router) |
| A2 | Body read, capped 1 MiB | `src/main.go:1103` | (c) |
| A3 | Body **is** the bearer token when not a kind-21000 event | `src/main.go:1123` `extractCashuToken(body)` | (c) |
| A4 | `PurchaseSession(cashuToken, macAddress)` | `src/main.go:1144` | (c) |

`Merchant.PurchaseSession` (`src/merchant/merchant.go:1493`), in order:

| # | Step | file:line | Requires |
|---|---|---|---|
| A5 | MAC validated | `merchant.go:1497` | (c) |
| A6 | **Token decoded locally** | `merchant.go:1507` `m.tollwallet.DecodeToken(cashuToken)` | (c) — local parse of `cashuA`/`cashuB` (`tollwallet.go:481`) |
| A7 | **Swap-fee pre-check — hits the mint** | `merchant.go:1522` `swapFeeSatsBounded(m.tollwallet.SwapFeeSats, paymentCashuToken)` | **(a) mint reachable — with an escape** (below) |
| A8 | **Pre-flight: NDS/`ndsctl` says the MAC is registered** | `merchant.go:1554` | (c) — local `ndsctl` |
| A9 | Duplicate-in-flight guard (local) | `merchant.go:1574` `m.beginReceive(reference)` | (c) |
| A10 | Durable receive-intent journal write | `merchant.go:1597` `m.beginReceiveIntent(…)` | (c) — local flash |
| A11 | **`Receive` — the money-moving swap at the mint** | `merchant.go:1640` `amount, err := m.tollwallet.Receive(paymentCashuToken)`, in a goroutine | **(a) + (b) — mint reachable over WAN** |
| A12 | 30 s deadline on that goroutine | `merchant.go:1656` `case <-time.After(receiveTimeout)`; const `defaultReceiveTimeout = 30 * time.Second` at `merchant.go:221` | — |
| A13 | On timeout: `payment-outcome-unknown`, no grant | `merchant.go:1691`; late recorder `merchant.go:1682-1685` | — |
| A14 | Allotment computed from the **swapped** amount | `merchant.go:1779` `m.calculateAllotment(amountAfterSwap, mintURL)` | (c) |
| A15 | Local grant: allotment + gate open | `merchant.go:1790` `m.grantSessionAccess(macAddress, allotment)` → `src/merchant/lightning.go:731` → `AddAllotment` (`merchant.go:2615`) + `openGateForSession` (`lightning.go:749`) → `valve.OpenGateUntil` / `valve.OpenGate` (`lightning.go:753,761`) | (c) |
| A16 | On Receive error: mint marked unreachable | `merchant.go:1724` `m.mintHealthTracker.MarkUnreachable(mintURL)` | (c) |
| A17 | Grant-failed ⇒ durable owed entitlement, retried | `merchant.go:1797` `m.recordOwedGrant(…)` | (c) |

**A7 is the one nuance worth being precise about.** `SwapFeeSats` reaches the mint for its keysets
(`src/tollwallet/tollwallet.go:230` `mintServesKeysets` → bounded `GET {mint}/v1/keysets`). But the
pre-check is written to *fall through* when the fee is unknown, and the comment says so explicitly:
"If the fee can't be determined (cdk adapter, mint unreachable — or the precheck overruns its budget
against a wedged mint, #525), fall through and let Receive classify the error" (`merchant.go:1519-1521`).
So A7 does **not** by itself gate the purchase.

**A11 does.** `receiveTimeout = 30s`. So the operational statement is sharper than "the mint must be
up": *a purchase from a stand-alone customer completes iff the router can complete a NUT-03 swap at
the mint within 30 seconds.* With no WAN that swap cannot even open a TCP connection, and the answer
is `payment-outcome-unknown` (`merchant.go:1691`) — value not moved, nothing granted.

`TollWallet.Receive` (`src/tollwallet/tollwallet.go:293`) is the swap; it delegates to the adapter's
`Receive(token, swapToTrusted)` (`tollwallet.go:316`; gonuts `gonuts_wallet.go:91`, cdk
`cdk_wallet.go:121`) and classifies the result: already-spent → `ErrTokenAlreadySpent` (`tollwallet.go:327`),
unanswered → `ErrOutcomeUnknown` (`tollwallet.go:333-336`). **The wallet's accepted-mint set is built
from mints that answered at boot** (`merchant.go:494-509`, `tollwallet.go:201`), so with no WAN at
boot the router has no accepted mint at all.

### 1b. Wire B — Lightning invoice: `POST /ln-invoice` then `GET /ln-invoice?quote=`

Route `src/main.go:1811-1814` → `handleLNInvoiceRoute` (`main.go:1599`) → POST:
`quoteCreateQuotaMiddleware(HandleLightningInvoice)` (`main.go:1604`) → `handleLightningInvoicePost`
(`main.go:1610`); GET: `handleLightningInvoiceGet` (`main.go:1709`).

| # | Step | file:line | Requires |
|---|---|---|---|
| B1 | **Client identity with a resolvable MAC** | `main.go:1652-1669`; refusal `errDeviceUnresolvedCode` | (c) |
| B2 | **Ask the mint for a bolt11 mint quote** | `main.go:1671` → `src/merchant/lightning.go:117` → `lightning.go:168` `m.tollwallet.RequestMintQuote(amount, mintURL)` | **(a) + (b)** |
| B3 | Quote record persisted; monitor goroutine | `lightning.go:173-185` | (c) |
| B4 | Poll: client polls `GET /ln-invoice?quote=`; answer coarsened to a **2 s** cache of mint state | `main.go:1740` → `lightning.go:353` → `lightning.go:420`; `lightningQuoteStateCacheTTL = 2 * time.Second` (`lightning.go:49`) | **(a) + (b) every ~2 s** |
| B5 | On `PAID`: **mint the tokens at the mint** | `lightning.go:669` `mintedAmount, err := m.tollwallet.MintTokens(quoteID)` | **(a) + (b)** |
| B6 | Local grant + gate open | `lightning.go:723` → `lightning.go:736`, `741` | (c) |
| B7 | Paid-but-ungranted ⇒ `access-grant-failed`, "do NOT pay again" | `main.go:1749-1756` | — |

Per-step × { (a) mint reachable, (b) internet/WAN reachable, (c) LAN only }:

- **POST / (Wire A):** every step (a5–a7, a8, a9–a10, a14–a17) is (c). Step a11 needs (a) and (b).
  There is no (c)-only success path.
- **Wire B:** steps b2, b4, b5 are hard (a)+(b); b4 in particular re-contacts the mint on nearly every
  poll. There is no (c)-only success path.

### 1c. The customer's own reachability requirements

Note what is **absent** from every row: the customer never contacts the mint. It has no mint URL to
reach other than the one the router advertises, and the code deliberately binds identity to the
socket so the customer cannot name itself (`main.go:1646-1651`, `main.go:1718-1723`). The customer
needs: a DHCP address on the LAN, an ARP entry at the router, and reachability to `:2121` (Wire A) or
route 2121 is firewall-allowed from LAN — `packaging/files/etc/uci-defaults/99-tollgate-setup` adds
`users_to_router='allow tcp port 2121'`, per `docs/architecture/nodogsplash-port-2121-decision.md:26-30`.
That is **all**. The mint-facing half is entirely the router's job.

---

## 2. The three network states, mapped

Precise definitions matter here, so:

- **WAN reachable** = the router has a working default route off-LAN. Every mint-facing step
  (A7, A11, B2, B4, B5) needs this *and* the internet (see §4 caveat on LAN mints).
- **Mint reachable** = the concrete accepted mint's HTTP endpoint answers. With the shipped config
  (`config_manager_config.go:297-370` `defaultProductionMints()`, 7 mints on `main` — pinned by
  `src/config_manager/buildinfo_test.go:48`) these are all `https://…` public mints
  (e.g. `https://mint.coinos.io` at `config_manager_config.go:300`).

### State (a) — LAN only / walled garden (customer reaches router; router has no WAN)

| Wire | Outcome at HEAD | Evidence |
|---|---|---|
| Wire A purchase | **Refused before any money moves**, in three separate places depending on boot state | see below |
| Wire B (invoice) | **Impossible** — cannot obtain a bolt11 without the mint | `lightning.go:168` |
| Session ticket / rebind (`/session/ticket`, `/session/rebind`) | **Works** — local, memory-only | `main.go:1849-1857` |
| `/whoami`, `/usage`, `/session-state` | `/whoami` always; `/usage` returns `-1/-1` with no session | `main.go:1806-1809`, `merchant.go:596-603` |
| Gate open for an **existing** session | Works until expiry — grant is local (`AddAllotment` is pure memory, `merchant.go:2615-2649`) | — |

Wire A's three refusal modes in state (a):

1. **No mint ever answered at boot** ⇒ the module constructs `MerchantDegraded`
   (`merchant.go:462-483`), whose `PurchaseSession` is a flat refusal:
   `code = "service-unavailable"`, "TollGate is initializing. No reachable mints." 
   (`merchant_degraded.go:174-181`). Degraded also stubs `RequestLightningInvoice`
   (`merchant_degraded.go:141-143`).
2. **A mint answered at boot, WAN dropped since** ⇒ full merchant, and the refusal is the
   **30-second wait** then `payment-outcome-unknown` (`merchant.go:1656`, `1691`), plus
   `MarkUnreachable` (`merchant.go:1724`). After 3 consecutive probe failures the mint leaves the
   reachable set (default threshold at `mint_health_tracker.go:28`) and the router can downgrade to
   degraded mode wholesale (`main.go:450-488`).
3. **Never a walled garden.** There is no whitelist/garden concept in the code: `grep -rni
   'walled.garden'` over the tree returns nothing. The customer's pre-auth reachability is whatever
   NoDogSplash's own interception gives, and the module's only role is the `:2121` LAN rule.

### State (b) — LAN + router has WAN

Full path works. Wire A: mint reached in `Receive` (`merchant.go:1640`), local grant
(`merchant.go:1790`), gate opened. Wire B: quote, poll, `MintTokens`, grant. This is the only state
in which a purchase completes at HEAD.

Also in state (b), and **only** in state (b): the reseller cold-start of #239 can fire — see §3 and §5.

### State (c) — nothing at all (customer has no path to the router)

Nothing in the module is reachable. The customer cannot be identified (identity comes from the socket,
`main.go:1092`), cannot be granted anything, and cannot even receive a refusal. There is no
out-of-band delivery channel on the money path: `CreateNoticeEvent`/`createSessionEvent` build
**signed nostr events but publish them to nobody** — they are returned as the HTTP response body
(`merchant.go:2291-2292`, `merchant.go:2114-2153`; and the only nostr mention in the merchant package
outside tests is a comment at `merchant.go:1953`).

### Which state the research question actually means — and is it reachable?

**"No internet gateway" is not the same as "no internet".** The research question's own framing
("the customer has NO internet gateway and NO custodian", SPIKE-BRIEF.md:9-11) is ambiguous between
state (a) and state (c), and the two answers are unrelated:

- **The only coherent reading is state (a): LAN only, router without WAN.** Because the customer is
  *talking to the router* (that is what "with local access" means) and the router is what sells the
  time, the customer must be on the router's LAN. "No internet gateway" then means "no WAN uplink",
  not "no network".

- **State (a) is reachable and routine.** It is exactly the #239 setting — a freshly-configured
  reseller "has no internet, no upstream session and no ecash balance" (`bootstrap.go:16-18`). The
  module even has the state names for it (`reseller_mode`, `config_manager_config.go:25`, default
  `false` at `:428`).

- **State (c) is out of scope by construction, not by policy.** With no path to the router there is
  no purchase to make. Any construction aimed at state (c) is a construction for delivering value to
  a device that is not on the network, which the module has no seam for at all.

**So the scenario the spike must actually satisfy is state (a): customer ↔ router on LAN, router has
no WAN. And in state (a) at HEAD, a stand-alone customer's purchase is refused outright — Wire A
because `Receive` cannot complete, Wire B because there is no bolt11 to be had.**

---

## 3. What #582 covers — and what it does not

**Issue #582** (`OPEN`, "First payment from a present-but-unregistered client is refused
(client-not-registered) — extend the renewal presence-proof to first purchases", 2026-09-25).
Fetched with `gh issue view 582 --repo OpenTollGate/tollgate-module-basic-go --json title,body`.

### What it covers

It is a **local precondition** bug, not a connectivity bug. The refused condition, quoted verbatim
(`src/merchant/merchant.go:1554-1561`):

```go
if !m.sessionIsRenewal(macAddress) && !m.clientRegisteredForGate(macAddress) {
    noticeEvent, noticeErr := m.CreateNoticeEvent("error", "client-not-registered",
        "No captive-portal session found for this device. Reconnect to the TollGate Wi-Fi and try again.", macAddress)
```

- `sessionIsRenewal` (`merchant.go:2501-2507`) is true when the MAC has an **active or expired** local
  session: `return state == SessionStateActive || state == SessionStateExpired`.
- `clientRegisteredForGate` (`merchant.go:2588-2607`) probes `ndsctl` up to `preflightProbeAttempts`
  times (`const preflightProbeAttempts = 5`, `merchant.go:208`), sleeping
  `preflightRetryDelayDefault = 400ms` (`merchant.go:212`), and — this is the load-bearing part —
  **fails open on probe error** (`merchant.go:2592-2595`: `log.Printf(… "failing open" …); return true`).
  Only a clean "not registered" verdict refuses. The production probe is `valve.CheckClientState`
  (`merchant.go:2562` → `src/valve/valve.go:1369` → `runNdsctl("json", mac)` at `valve.go:1375`), and a
  MAC NoDogSplash has never seen parses to `ClientState{Registered: false}` (`valve.go:1390-1391`).
- The refusal happens **before `Receive`** (`merchant.go:1540-1548` comments say so explicitly), which
  is the point: the customer's note is not consumed.

#582's proposal is to treat "MAC resolved from the request socket **and** a payment POST received"
as the presence-proof for *first* purchases too, letting the valve's bounded auth retry perform
just-in-time NDS registration, with the existing `grantSessionAccess` rollback covering genuine
registration failure.

### What it does not cover — and the correction to the recon record

**#582 is unrelated to the mint round-trip.** Its own reproduction is a *wired* LAN-side client
(`curl -X POST --data "$TOKEN" http://192.168.103.51:2121/`) with, in its words, "the mint is
reachable". Merging #582 changes only whether the router chooses to proceed into `Receive`; it does
not change what `Receive` requires. In state (a), #582's fix would move the refusal from the
`client-not-registered` notice to a 30-second stall followed by `payment-outcome-unknown`
(`merchant.go:1656`, `1691`) — **same outcome, different notice**, and the customer's note is now
*dangling* (the router sent a swap it never got an answer to).

**Correction to the recon note (SPIKE-BRIEF.md:38-42).** The brief lists #582 as adjacent work on the
"first payment from a present-but-unregistered client" and #498/#719 as the "ambiguous-outcome
class". Both point at *local/outcome* bookkeeping. Neither touches the structural requirement that
the router perform a mint swap. Neither is the "closest EXISTING work" to the research question; the
closest is #239 (§4), and #239 does not remove the requirement either (§5).

---

## 4. Where a one-way channel would insert, and what it would replace

### The insertion point the code actually offers

The seam is `WalletPort` (`src/tollwallet/port.go`), whose `Receive(token Token) (uint64, error)`
(`port.go:196`) is reached from exactly one place in the purchase core:
`merchant.go:1640` (Wire A) and, indirectly, from `MintTokens` (`port.go:237`) at `lightning.go:669`
(Wire B). `Receive` is documented as necessarily-destructive — "A receive-swap consumes all input
proofs" (`port.go:248`) — and the port's own research companion states the invariant outright:
"Receiving is necessarily a swap… its value only continues to exist as a replacement output in the
holder's own derivation space" (`docs/architecture/bearer-instrument-port.md:3`,
`research/bearer-port-demo/authorities.go:25-93`). A `Receive` today is *not* a local state write; it
is a network round trip.

So a one-way (Spilman-style) instrument would insert as a **new instrument + adjudicator under
`WalletPort`/the `Authority` abstraction**, replacing the `Receive` implementation for the
customer-funded leg: in place of "swap the note at the mint now", the router would accept a signed
channel state and adjudicate later. The `Authority` interface the repo already has
(`research/bearer-port-demo/authorities.go:25-93`) requires exactly three things — what it `signed`,
what it `consumed`, how it can `answer` — and the brief's claim that a one-way channel needs a
**fourth** field (the adjudicator / chain) is correct against this code: `CheckStates`
(`authorities.go:75-80`) is the *only* state question the port models, and it presupposes an authority
that can answer. A one-way channel's real state lives in a chain's UTXO set, which is neither the
mint's issuance log nor its spend set.

### What it would replace — and what it would NOT

| It would replace | It would not touch |
|---|---|
| The mint round trip in the **customer's** redemption (`merchant.go:1640`) | `clientRegisteredForGate` / `sessionIsRenewal` (`merchant.go:1554`) — local NDS precondition, invisible to any chain |
| `ErrOutcomeUnknown` exposure on that leg (`merchant.go:1656`, `1691`) | `grantSessionAccess` → `AddAllotment` → `valve.OpenGate*` (`lightning.go:731-769`) — already fully local |
| For Wire B, `RequestMintQuote`/`MintTokens` (`lightning.go:168`, `669`) if a channel-funded path replaced the invoice path | The router's need to *settle value it received*: payout/melt (`merchant.go:1477-1484` `MeltToLightning`) is mint+Lightning work on the router's side, and a claim on a chain is not sats in the operator's wallet |
| The router's dependence on the **customer's** token being swappable | The router's dependence on a **mint** to hold and discharge the value it sells on. That is a second, separate round trip |

### Two code facts that bear directly on the design

1. **`hasLockedProofs` is a hard pre-Receive gate** (`tollwallet.go:296-298`,
   `hasLockedProofs` at `tollwallet.go:521-529`): any proof whose secret is not
   `nut10.AnyoneCanSpend` is refused with `ErrLockedToken` (`tollwallet.go:27`) — "token has spending
   conditions and cannot be spent by the gateway". A P2PK-locked or timelocked instrument is
   **refused by the router today**, before any spend. Any channel instrument expressed as a locked
   Cashu proof would land here first; the port must be taught the shape, not just handed the proof.
2. **The repo has already decided, in a merged ADR, that one-way channels are the intended rail** —
   `docs/architecture/session-ticket-decision.md:371-424`, "Spillman channels as the intended payment
   rail (2026-09-24)", naming `SatsAndSports/cashu_spilman_channels` / `cdk-spilman` with
   `SatsAndSports/MONAD` as the production-shaped example. Its migration gate is explicit and is
   **not** the research question's gate: *"as soon as the wallet runs on CDK and a wallet that
   supports the channel protocol is available. That is the migration gate"* (`:373-378`). The ADR also
   already names the same four risk classes the spike would rediscover: early-alpha reference,
   receiver-must-claim-before-expiry, unverified CDK sidecar fit on the target router, and operator
   balance at a third-party mint (`:418-422`).

---

## 5. Honest verdict: does the module's architecture permit the scenario at all?

### Verdict

**The module does not permit the question as posed, and it does not need the construction to answer
the adjacent question it actually has.**

Three findings, in order of weight.

**Closest existing work, and it is #239, not #582.** The brief says #239 "partially answers" cold start
because "a reseller with no internet, no session and no ecash forwards its first customer proof
**whole** to the upstream TollGate — no swap, no split" (SPIKE-BRIEF.md:18-23). That summary
originates in the module's own comment block (`bootstrap.go:16-24`) and is **incomplete in the way
that matters**: *the reseller* does no swap; an upstream TollGate still swaps the proof at a mint. Read the code:

- `ForwardFirstProof` does not hand the proof to an authority that exists on the LAN. It hands it to
  an **upstream TollGate over the LAN**, whose own merchant then performs the same `Receive`-shaped
  work — `postTokenToUpstream` (`session.go:404-478`) is documented as "the same HTTP payment wire the
  warm path uses" (`bootstrap.go:69-70`) and POSTs to `http://<gatewayIP>:2121/` (`session.go:405`).
  The upstream answers with a kind-1022 session event (`session.go:443-452`). So no swap happened at
  the *reseller*, and one still has to happen at the *upstream*, at the mint.
- `ForwardFirstProof` rejects a zero allotment as an error (`bootstrap.go:281-288`: `"upstream granted
  a zero allotment"`), i.e. the upstream's grant is a precondition. A grant means the upstream
  validated at the mint.
- **Most decisively: `ForwardFirstProof` has no production caller.** `grep -rn 'ForwardFirstProof'`
  over the tree returns the definition (`bootstrap.go:205`), the manager wrapper
  (`upstream_session_manager.go:192-210`), an interface declaration that **nothing implements or
  consumes** (`types.go:95-105` — `ProofForwarderTarget` appears only in its own definition file), and
  test files. It is never called from `main.go` or from `PurchaseSession`. `BootstrapCandidate` /
  `EnsureBootstrapForColdStart` are called only from `session.go:149` and `session.go:265` — the
  upstream session's own tracker path. And **`bootstrap.go` implements no mint interaction at all**:
  the file's only imports are `crypto/sha256`, `encoding/hex`, `fmt`, `sync`, `time`, `logrus`
  (`bootstrap.go:3-11`). The words "mint" appear in it only in comments.
  The release notes agree: it is "in the pinned baseline as lab-tested features awaiting router
  acceptance — treat both as experimental" (`RELEASE-NOTES.md:89-92`), and #239 phase 2b (the merchant
  side that would call it) is still unwritten, which the code itself admits at `bootstrap.go:226-228`.
- **The merchant cannot reach the bootstrap machinery anyway.** `MerchantInterface`
  (`merchant.go:336-359`) has no method that forwards a proof upstream, `merchant.go:1493-1886` never
  mentions `upstream_session_manager`, and the two packages are wired in opposite directions:
  `initUpstreamDetector()` constructs the USM **with** the merchant provider (`main.go:716`), while the
  merchant has no USM handle.

So #239's cold start is **not** an existence proof that a customer can buy without a mint in the loop.
It is an existence proof that a *reseller* can be funded by an *upstream* that has mint access — a
different claim. `ForwardFirstProof` requires, at the moment of its first use: (i) reseller mode
on (`bootstrap.go:97-103`); (ii) no established upstream session (`bootstrap.go:111-115`); (iii) zero
wallet balance at every mint the upstream accepts (`bootstrap.go:121-150`); (iv) **a reachable
upstream TollGate at `gatewayIP:2121` with access to a mint** (`session.go:405`, `:432-452`). That is
state (b) for the *reseller's WAN* — it is exactly the state #239 says the reseller does not have yet.
The bootstrap is a mesh-formation primitive, not an offline-purchase primitive.

**(2) The research question's premise collapses under the code, and that collapse is the finding.**
The question asks for a payer with "no internet gateway and no custodian". The code shows the payer
never needed either *from the module's point of view*: the payer's requirement is LAN + a resolvable
MAC (`main.go:1092`), and the mint-facing work is the router's. What the module actually needs is for
**the router** to complete a NUT-03 swap within 30 s per purchase (`merchant.go:221`, `1640`,
`1656`). The recon framing in SPIKE-BRIEF.md:52-56 — "the router is the redeemer, and the router has
(or will have, once paid) WAN" — is a **contractual** claim, not an architectural one: nothing in this
tree makes a router's WAN conditional on the router having been paid. Bandwidth comes from the
upstream link (`initUpstreamManager`, `main.go:732-761`) gated by the upstream's own `:2121` payment
wire, not from anything the reseller's customer did. And the module has **no chain code whatsoever**:
`grep -rni 'btcd|bitcoind|electrum|utxo|txid|OP_CSV|nsequence|descriptor'` over `src/` returns only
false positives on log text and a doc comment. A one-way channel's adjudicator has no home here today.

**(3) The honest restatement, which is defensible from the code.** The increment a one-way channel
could deliver is **not** "payer with no internet buys from a router with no internet". It is:

> the router accepts a customer's value **before** it can reach a mint, and settles it later — removing
> the 30-second `Receive` block (and the `payment-outcome-unknown` class at `merchant.go:1691`) from
> the customer-facing leg.

For that to work in state (a), the router must be able to (i) verify the instrument, (ii) open the
gate locally — which it already can, `AddAllotment` is pure memory (`merchant.go:2615-2649`) and
`valve.OpenGate*` is local (`lightning.go:753`, `761`) — and (iii) defer settlement. (i) and (iii) are
the whole new capability. Note that the *grant* half is already free, so the spike's deliverable is a
verify-and-defer seam plus an adjudicator, not a payment path. That is a real, testable, and
non-duplicative target — and it is a different question from the one the brief's headline asks.

**Also worth recording: the module's own ADR already chose one-way channels, for the opposite
motivation.** `session-ticket-decision.md:373-391` adopts Spillman channels to *reduce per-payment
overhead* ("small, frequent, granular payments stop costing an invoice and a mint round-trip each"),
not to serve an offline payer. The brief must not present the spike as introducing channels to this
project; it is at most supplying the offline-payer feasibility argument on top of a decision already
made.

---

## 6. What I could not determine from the code, and why

1. **Whether a LAN-hosted mint makes state (a) sufficient.** Nothing in the module constrains a mint
   URL's scheme or host: `mintServesKeysets` is a plain `GET {mintURL}/v1/keysets`
   (`tollwallet.go:230-245`) with no private-IP or https check, and `accepted_mints[].url` is a free
   string (`config_manager_config.go:19`, schema `config_schema.go:69-77`). So **architecturally** a
   mint on a second LAN host would satisfy steps A7/A11/B2/B4/B5 without WAN. Whether that is a legal
   configuration in this project I cannot tell from code: all 7 shipped defaults are public
   `https://` hosts, and I found no document either permitting or forbidding a LAN mint. If a LAN mint
   were permitted, the research question reduces to "run a mint on the LAN", and the one-way channel
   is unnecessary — which would be the strongest disproof available. **This needs a decision from the
   operators, not more code reading.**
2. **Whether #239 phase 2b (the merchant-side call to `ForwardFirstProof`) exists on a branch.** I
   read only `main` at cf7cad3. `gh issue view 239` lists Phase 2 as 3-4 tasks and the code admits
   phase 2b is unwritten (`bootstrap.go:226-228`), but I did not enumerate branches or PRs, so a
   WIP implementation may exist elsewhere. Nothing in `main` calls it.
3. **What "the customer" is, as software.** The module has no client implementation (the captive
   portal SPA lives in a separate repo — `docs/architecture/captive-portal-bundle-location-decision.md` §Background, and my clone carries only a digest-pinned placeholder/symlink, not source). So I
   cannot state from code what a real customer client could do with a channel instrument: whether it
   could hold channel state, sign updates offline, or claim at expiry. The ADR's own accepted risks
   (`session-ticket-decision.md:418-422`) put recovery in "the wallet layer, not this module" — i.e.
   outside this repo.
4. **Whether the 30 s `receiveTimeout` is honoured end-to-end on the router.** `defaultReceiveTimeout`
   is applied around the goroutine (`merchant.go:1646-1656`), but the underlying HTTP client's own
   deadline is inside `gonuts-tollgate`/`cdk-go` (`src/go.mod:42`, `:66`), outside this module. I can
   state the module's intent, not the observed worst case.
5. **The real customer-visible latency of a degraded↔full flip.** The paths exist
   (`main.go:450-488`, `merchant.go:462-483`, aggressive retry `main.go:486`), but the timings depend
   on probe constants and an operator-visible window I did not measure. This matters for the state-(a)
   UX claim ("refused until recovery") and I have not verified the recovery latency on hardware.
6. **Issues #498 / #719 / #804 — what they do and do not already answer.** Fetched all three in full:
   - **#498** (OPEN, P1, `release:post-0.6.0`) — the ambiguous-outcome class. Its file:line references
     (`merchant.go:506-538`, `:519-529`) are **stale/misdirected**: those lines are the wallet
     construction path in `newFullMerchant` (`merchant.go:506-509`), not the `Receive` timeout. The
     timeout goroutine is at `merchant.go:1631-1696`. The *problem* it describes was substantially
     built after the issue was filed — the durable receive-intent journal exists
     (`merchant.go:1590-1629`, `receive_intent.go`) and a late-outcome recorder exists
     (`merchant.go:1682-1685`, `recordLateReceiveOutcome` at `merchant.go:237`). So #498 already
     answers **part** of the ambiguous-outcome question, at the merchant layer.
   - **#719** (OPEN, S1/S2 funds-adjacent) — the crash window: `main` does **not** recover value when
     the process is SIGKILLed between "mint signed the swap" and "wallet persisted", with #502
     machinery visibly engaging and the lane still failing. **This matters to the spike as a
     non-duplication constraint, and as bad news for the one-way design**: channel state is *more*
     durable state than the router has ever held, and the ADR already flags "the receiver must claim
     before expiry or the sender refunds the whole channel, so a crash or a forgotten close is lost
     revenue" (`session-ticket-decision.md:418-420`). Building a channel on top of a tree whose
     crash-recovery lane is red would multiply an existing defect.
   - **#804** (OPEN, migrated from `Amperstrand/tollgate-module-basic-go#74`) — spent-token detection.
     Its specific claim is that `tollwallet.go:146` is `strings.Contains(err.Error(), "Token already
     spent")` and that this "matches no real mint" (gonuts returns `proof already used`, nutshell
     returns `proofs already spent`, both code 11001). **Both halves of that claim are stale at
     cf7cad3.** Line 146 is now `canonicalHost`'s doc comment; the matcher is `isAlreadySpentError` at
     `tollwallet.go:512-519` and it matches `"already been spent"` and `"already spent"` — which does
     cover nutshell's `proofs already spent`. And the cdk adapter has a proper code-11001 branch
     (`cdk_wallet.go:442`) plus `"already used"`/`"already signed"` string fallbacks
     (`cdk_wallet.go:450`). There is a **second, separate** string-match family the issue does not
     name: `isDefinitiveMintRefusal` (`merchant.go:1941-1963`) classifies `"already spent"` /
     `"already signed"` (`merchant.go:1954`) as a definitive refusal, and also does not match
     `proof already used`. So the residual hole is real but narrow, and there are two matcher sites to
     fix, not one — the classification of a gonuts spent-replay as *non*-definitive is the more
     consequential of the two, because it is what routes a spent-replay into the reconcile-vs-abandon
     decision rather than treating it as settled. Issue #804 itself flags that its claim may not
     reproduce: its migration note says "Applicability to current
     upstream `main` (4614ac2, post-rc1) was not re-verified during migration — confirming whether the
     finding still reproduces upstream is this issue's first task." **It does not reproduce as
     described.**
   - **Net:** none of the three answers the research question. #498 and #719 are the *state* half of
     "what a router does with value whose fate is unknown"; #804 is a string-match bug that is largely
     fixed. All three are precedents for the memo's §5 conclusion, not partial answers.
7. **Whether the reseller's own NDS/upstream plumbing can even be exercised in state (a).** The
   upstream detector needs a *discovered upstream gateway* on a LAN interface
   (`upstream_detector/network_monitor.go`, `getGatewayFromGlobalRoutes` / `getGatewayByInference`),
   and `postTokenToUpstream` posts to `:2121` on it. I did not verify whether discovery can complete
   for a two-router LAN with no WAN on either side — i.e. whether the #239 topology is reachable in
   the very state #239 exists for. That is a concrete, cheap, and load-bearing next test, and it is
   precisely the test the recon record should have run before calling #239 "partially answered".
