# Client Identity for Paid Access — MAC Attribution vs. Signed Payment Credential

## Status: Proposed (2026-11-03)

The gateway must decide **who** is buying before it decides **what** to open. Today
that answer is a MAC address taken from the connection, and the plan to sell
operator-issued ecash to allowlisted client pubkeys collides with it directly.
This document sets out the collision, the options, and a recommendation.

It is a **proposal**, not a decision. It exists because two requirements — "sell
ecash to allowlisted pubkeys" and "reach the gateway over a FIPS mesh" — each look
individually reasonable and are, together, incompatible with the current
enforcement mechanism.

## The failure mode

The merchant opens access with `ndsctl auth <mac>`. NoDogSplash will only authorise
a client it has **observed on the LAN bridge** (`br-lan`). The MAC it receives is
therefore not an assertion the client makes about itself; it is an observation the
gateway made about the connection.

Three consequences follow, and only the first is comfortable:

1. **Attribution works and cannot be spoofed at the API layer.** A client cannot
   claim someone else's MAC to inherit their session, because the gateway ignores
   what the client says and reads the socket.
2. **Every relay destroys identity.** Any NAT, HTTP proxy, `adb reverse`, or VPN
   tunnel terminates the client's L2 presence. The gateway then sees the relay's
   MAC. `ndsctl` either refuses (unknown client) or — worse — authorises the relay,
   granting the tunnel operator's session to everyone behind it. This is not
   hypothetical: it is the exact failure already hit in the lab when a client
   reached the gate through a host-side proxy.
3. **It is unscalable across subnets.** A client on a different router's LAN, or
   arriving over a mesh, has no MAC on this bridge. There is nothing to observe.

Consequence 2 is the one that decides the FIPS question. FIPS is a mesh VPN: it
delivers packets, and to the gateway those packets arrive from the FIPS tunnel
endpoint. **No amount of mesh configuration makes the customer's MAC visible to
NoDogSplash**, because the MAC was consumed two hops ago.

## What the pubkey path already provides, and what it does not

`DeriveMAC(pubHex, "br-lan")` gives a client MAC that is a deterministic function
of its npub (`tollgate-mac-v1:br-lan:<pubkey>`, locally-administered bit set). This
is genuinely useful — it lets an operator pre-seed a known device's identity — but
it solves a different problem than it appears to:

- It gives a **stable name**, not an **authorisation**. Anyone may compute any
  pubkey's MAC.
- It only works when the device is genuinely on the segment. A pubkey-derived MAC
  presented through a relay is still, to NoDogSplash, the relay's MAC.

So "whitelist our pub" cannot be implemented as "pre-seed a MAC" and still survive
a FIPS hop. The name is portable; the enforcement is not.

## Options

### Option A — Keep MAC attribution, scope sales to the local segment

Nothing changes in enforcement. Sell ecash to clients on `br-lan` only; issue over
the operator socket as today.

- **Buys:** no new identity mechanism, no new failure mode, ships soonest.
- **Costs:** FIPS clients cannot buy. Multi-router deployments cannot sell across
  subnets. Every relay stays a bug.
- **Verdict:** correct for a single-router community deployment, and honest about
  its boundary.

### Option B — Signed payment credential as the authorisation unit

The client signs a Nostr event (NIP-98-style) naming the amount, unit, and mint,
with its npub. The merchant verifies the signature, checks the npub against an
allowlist, and — critically — **binds the resulting session to the MAC observed at
the socket** for enforcement purposes, while attributing the *payment* to the
pubkey.

- **Buys:** works over any transport that can carry an HTTP request, including
  FIPS. Payment identity survives relays. Allowlist becomes meaningful.
- **Costs:** two identities must be reconciled — payment pubkey and enforcement
  MAC. A client that pays from one pubkey and connects from another device needs an
  explicit binding step. Replay must be prevented (nonce + short expiry).
- **Verdict:** the only option that satisfies the stated requirement.

### Option C — Credential-only enforcement, retire `ndsctl auth`

Replace MAC-based authorisation with a credential the client presents on every
request (port 80/443 gating by pubkey, not by L2 address).

- **Buys:** one identity, end to end. No MAC/MAC-binding reconciliation.
- **Costs:** abandons NoDogSplash's captive-portal model, which is the entire
  user-facing onboarding path and the thing that makes a TollGate usable by someone
  who has not installed anything. Large, and it fights the repo's stated ownership
  boundary (firewall/captive portal are PRTA-verified surfaces).
- **Verdict:** a strategic direction, not a next step.

## Recommendation

**Option B**, with the MAC binding made explicit rather than implicit.

The reasoning is narrow: the requirement is "allowlisted pubkeys buy megabytes over
FIPS." Option A cannot deliver it. Option C delivers it but discards the captive
portal. Option B is the only one where the payment identity is transport-independent
while enforcement stays with the mechanism that actually works.

The design constraint that keeps B honest: **the credential authorises a payment,
never a session.** A verified signed event entitles the client to have its
*socket-observed* MAC authorised. It does not let the client name a MAC, because
that reintroduces the spoofing hole that MAC attribution currently closes. If the
pubkey's device is not the one connecting, the payment succeeds and the access does
not — and that outcome must be a clear, reported state, not a silent failure.

## What this decides about the unit-parametric work

This ADR exists to answer whether making the wallet unit-parametric is the right
investment. It is **orthogonal** — and that is the finding, not a dodge.

Neither option above changes whether a shipping router can *redeem* a custom-unit
token. Option B changes **who may buy**; the unit work changes **what can be
accepted**. A gateway that can authorise an FIPS client to buy megabytes, but
cannot receive an `mb` token, has built a purchase path with no settlement.

So the ordering is: **unit-parametric redemption first**, because it is a hard
prerequisite for any option, while identity is a choice between viable paths. An
allowlist that gates sales of a token the gateway refuses to accept is worse than
no allowlist — it advertises a capability that fails at the last step.

## Open questions

- **Bind or refuse?** When a verified payer's observed MAC differs from the MAC
  bound to their credential, is that an error, or a legitimate roaming case?
- **Allowlist semantics.** Grant (operator funds the mint quote, client receives
  free quota), credit line (client pays sats, receives mb), or unbacked quota. Each
  implies a different authorisation record. The ADR for the economics should
  precede the implementation of the list.
- **Replay window.** Short expiry plus a monotonic nonce is the obvious shape; the
  acceptable window depends on how much clock skew the fleet's nodes tolerate.
- **Mint trust.** A FIPS ULA mint is reachable at `http://`. The module accepts it,
  but mint traffic would be plaintext inside the mesh. Is an unencrypted mint
  acceptable on a mesh the operator controls, or does every mint endpoint require
  TLS regardless of transport?
