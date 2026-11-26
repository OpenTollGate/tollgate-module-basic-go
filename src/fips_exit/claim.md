# Peer-claim binding (slice A of a paid FIPS exit)

Scope: `claim.go` in package `fips_exit`. Pure data and pure functions — no I/O,
no HTTP, no payment, no nftables, no signalling, no persistence.

## The threat this closes

A caller asks a paid FIPS exit for access and names a peer key. Without this
layer the exit can only see a string that claims to be an identity, so anyone
could paste a stranger's npub and, if the exit admitted on the name alone, ride
on that stranger's reputation or grant.

`Claim.Verify` requires two independent things:

1. **Proof of possession.** A BIP340 schnorr signature over
   `SHA-256(Challenge.Canonical())` that verifies under `Claim.Buyer`. Only the
   holder of that key's secret can produce it, so submitting someone else's npub
   fails at `ErrClaimSignature`. That is the named test
   `TestClaimStolenNpubIsRejected`.
2. **Binding to the tunnel.** `Derive(Buyer)` must equal the source address the
   FIPS transport actually observed for this tunnel. A correctly signed claim
   presented from another peer's tunnel fails at `ErrClaimUnbound`
   (`TestClaimNotMyTunnelIsRejected`). An invalid, unspecified or zero observed
   source is refused as unbound; there is no fallback to the zero address.

## Why a proof cannot be replayed or re-termed

- `Challenge.Canonical()` signs **every** term: exit key, nonce, issue time,
  expiry, price and billing mode, behind a `tollgate-fips-exit/claim\0` domain
  prefix and a format-version byte, with fixed-width integers and a
  length-prefixed mode string. No two different challenges serialize to the same
  bytes, so a signature cannot be moved to cheaper terms or another exit.
- Each challenge carries a 16-byte `ClaimNonce`; `NonceCache.Use` refuses a
  second use inside the window with `ErrNonceReused`.
- The window is capped at 15 minutes and the check order is fixed (malformed →
  signature → binding → expiry → not-yet-valid), so a foreign-tunnel claim is
  never mistaken for a lapsed purchase.

**The nonce cache is in-memory only.** A process restart clears it, so it is
*not* a durable replay defence. The real bound on replay is the short challenge
TTL: a restart re-opens replay only for the remainder of an already-issued
challenge's window, which the 15-minute cap keeps small. A durable defence
belongs to a layer that persists state; this one deliberately has none.

## What is deliberately NOT here

- No HTTP handler or wire format, no Cashu/payment verification, no nftables
  rules, no SIGHUP to fips-exitd, no file I/O.
- Nothing is wired into the TollGate purchase path or the admission ledger in
  `admission.go`; a caller must connect a `GrantRequest` to a verified `Claim`
  itself, and must decide *which* address the transport saw before calling
  `Verify`.
- **A passing test here proves nothing about a live exit.** It proves the
  decision function is right for the inputs supplied. It says nothing about
  whether the caller paid, whether the observed source address was sampled
  honestly, or whether the exit's admission list was reloaded.

## Remaining work for a real paid exit

1. Issue `Challenge`s from an exit handler with `crypto/rand.Reader` and a TTL
   well under 15 minutes; deliver them out of band from the payment.
2. Verify payment against the signed `PriceSats`/`Mode` before accepting a claim,
   so a signature proves identity and payment proves the entitlement.
3. Wire the verified buyer key into the admission ledger, then rewrite the
   clients file and SIGHUP fips-exitd (or drive fips-exitd's API).
4. Supply a trustworthy `observedSource` from the FIPS transport's own peer
   view, not from anything the caller sends.
5. Hold `NonceCache` across the process lifetime and decide whether a restart
   needs a persisted nonce set; the current answer is "the TTL covers it".

## Cryptography choices

Verification uses `github.com/btcsuite/btcd/btcec/v2/schnorr`, already in the
module graph through `go-nostr`, so no new dependency and no cgo: it builds
unchanged for `linux/mips`, `mips64`, `arm`, `arm64` and `wasm`. Tests generate
keys with `nostr.GeneratePrivateKey`/`nostr.GetPublicKey` and sign with `btcec`,
because `go-nostr`'s event signing is over event ids, not arbitrary bytes.
