# The `WalletPort` acceptance contract

**Status:** written 2026-09-14 from the actual interface in
[`src/tollwallet/port.go`](../../src/tollwallet/port.go), the wire-format rules in
[`src/tollwallet/WIREFORMAT.md`](../../src/tollwallet/WIREFORMAT.md), and the
failure modes surfaced by the candidate studies (see the wallet-migration
archive: <https://github.com/felixfelix-bot/tollgate-wallet-migration-research>).

**Rule of this document:** a candidate wallet backend is judged against *this
contract*, not against a wish list. Anything here is something the router
actually does or must survive; anything not here is a candidate's own feature,
not a requirement.

---

## 1. The surface (what must be implemented)

Source of truth: `src/tollwallet/port.go`. Callers: `src/merchant/*`,
`src/lightning/*`. The interface is the seam; a replacement implements it and
leaves the rest of the module untouched.

| Method | Semantics | NUTs implied |
|---|---|---|
| `DecodeToken(str) (Token,error)` | Parse `cashuA…` (V3) / `cashuB…` (V4-CBOR) | NUT-00 (tokens), NUT-00 V4 |
| `Receive(Token) (uint64,error)` | Accept a token, validate, optionally swap to a trusted mint, credit | NUT-03 (swap), NUT-11 (P2PK), NUT-12 (DLEQ) |
| `GetBalance()`, `GetBalanceByMint(url)`, `GetAllMintBalances()` | Local proof accounting | NUT-01/02 (keysets) |
| `Send(amount, mint, includeFees) (Token,error)` | Split/swap to produce a token | NUT-03, NUT-08 (fees) |
| `SendWithOverpayment(amount, mint, pct, abs) (string,error)` | Send with bounded overpayment | NUT-03, NUT-10 |
| `Drain(mint) (Token,uint64,error)` | Token for the full balance of a mint | NUT-03 |
| `MeltToLightning(mint, target, maxCost, lnurl) error` | Pay a Lightning address from funds | NUT-05, NUT-11/12/14 (HTLC/P2PK/LNURL), NUT-20 (in-flight) |
| `RequestMintQuote(amount, mint) (*MintQuote,error)` | Ask mint for a bolt11 invoice | NUT-04, NUT-20, NUT-23 |
| `GetMintQuoteState(quoteID) (MintQuoteState,error)` | Poll quote state | NUT-04 |
| `MintTokens(quoteID) (uint64,error)` | Mint proofs for a paid quote | NUT-04, NUT-02, NUT-12, NUT-13 (deterministic secrets) |
| `RequestMeltQuote(invoice, mint) (*MeltQuote,error)` | Melt quote | NUT-05 |
| `Melt(quoteID) (*MeltResult,error)` | Pay invoice, consume proofs | NUT-05, NUT-08, NUT-20 |
| `Shutdown() error` | Release DB/CGO handles; idempotent | — |

### Contract gaps the candidate studies exposed (MUST be added to the contract)

nucula lacked these and it was disqualifying; they are **requirements**, so any
evaluation must assert them explicitly:

- **NUT-07 (token state check)** — "is this proof spent?" Must agree with the
  mint, and must recover alone after disagreement.
- **NUT-09 (restore)** — from the seed + keyset, reconstruct wallet state. This
  is what makes a lost/corrupt store survivable and what a migration must
  preserve.
- **Crash-consistent storage** — kill mid-swap must not lose proofs or funds.
- **Encrypted seed at rest** — the current gonuts store and nucula both keep the
  seed in plaintext; a replacement must not regress. (Tracked as a gap, not a
  blocker to parity.)

## 2. Type + serialization contract

- **`Token` uses explicit `Close()`** for CGO lifecycle; implementations must be
  idempotent. (gonuts: no-op; cdk: `Destroy()`.)
- **`MintQuoteState`** is `int` underneath with **dual-format JSON**:
  - marshal → uppercase string (`"UNPAID"/"PAID"/"ISSUED"/"PENDING"/"UNKNOWN"`)
  - unmarshal → accepts **both** integer (legacy gonuts) and string (canonical)
  This is a hard compatibility requirement (production `quotes.json`). See
  `src/tollwallet/WIREFORMAT.md`. A replacement must not change this, and
  `unknown` must be handled case-insensitively.
- **Primitive-only leakage:** merchant/lightning code must depend only on these
  Go types — no gonuts/cdk/nucula types may cross the seam.

## 3. Non-functional contract (measured, not assumed)

Every candidate is scored on the same axes; the procedures and thresholds are in
[`wallet-measurement-protocol.md`](wallet-measurement-protocol.md):

| Axis | Requirement / measurement |
|---|---|
| **OpenWrt build** | builds for `aarch64` **and** `mipsel` musl from the OpenWrt SDK; static; `CGO_ENABLED=0` preferred |
| **Footprint** | stripped binary size; RSS idle/peak; thread count; startup time |
| **Flash / storage** | on-flash bytes written **per payment**; crash-consistency; no whole-blob rewrite per mutation |
| **Reliability** | mint unreachable / 5xx / malformed; clock skew; kill mid-swap; disk full; corrupt store; spent-proof disagreement + self-recovery |
| **Ownership cost** | fork-ownership grade A/B/C (upstream-maintained vs platform patch set vs we own it) |
| **Licence** | GPL-3.0-compatible (gating for reuse) |

## 4. Security / funds-safety parity (acceptance criteria)

The gonuts fork is **load-bearing**: it carries two fixes upstream can never
absorb. **Any replacement must reproduce both, with a failing test per case
before the old wallet is removed:**

- `296c7bf` — **HTLC signature-enforcement bypass** fix.
- `7dc430b` — **swap proof-loss** fix.

These are not notes; they are pass/fail acceptance tests.

## 5. Candidate mapping (first pass)

| Contract area | gonuts (current, forked) | CDK (`cdk-go` / sidecar) | nucula |
|---|---|---|---|
| Surface | complete (the reference) | adapter exists but unverified | partial — missing NUT-07/09 |
| musl aarch64 | ✅ (current build) | sidecar ✅ (19.7 MiB static) | not applicable as-is |
| musl mipsel | ✅ | **✗ blocked** (no `AtomicU64`) | not applicable |
| Ownership | **C (we own the fork)** | A/B (upstream CDK) | licence unresolved |
| Licence | GPL-3.0 (fork) | MIT/Apache-2.0-compatible | **none** (issue #8) |
| Security parity | holds the two fixes | must be proven | must be proven |

**Conclusion:** the contract is small and stable, the seam already exists, and
the decisive axes are **mipsel buildability**, **security parity**, and
**fork-ownership** — not feature breadth.
