# The release trust model

How a TollGate consumer decides which artifacts to install, and how the
tooling supports that decision. This document is the doctrine; the tools
cited are its enforcement.

## Trust is consumer-scoped

A kind-1063 announcement is a *claim*: these bytes, this version, this
architecture. Cryptographically the signature only proves *who* claims
it. Whether the claim is worth acting on is a decision the consumer
makes about **keys**, not about events — there is no global "real"
release list, only the set of publishers a consumer has decided to
trust. The default trust set in our tooling is the two release
publishers recorded in AGENTS.md (the historical GitHub Actions key and
the dedicated ngit CI key); a consumer can narrow it to one key, or to
their own.

Two corollaries, deliberately embraced:

- **Anyone can publish a "release."** An announcement from a key outside
  your trust set is not an attack on the channel — it is noise you
  already decided not to act on. The tooling's job is to make that
  classification visible, not to prevent publication.
- **We publish deliberate fakes.** A fake we publish ourselves — from a
  throwaway key, marked — is a fixture for training and testing the
  detection tooling. It is "real" in every wire sense and untrusted in
  every decision sense, which is exactly the shape an attacker's fake
  has. See "Drills" below.

## Deterministic bytes are the anchor

Because builds are reproducible (digest-pinned SDKs, pinned toolchains,
`SOURCE_DATE_EPOCH`; `scripts/repro-test.sh` proves byte-identity), the
sha256 in an event's `x` tag is an *objective* fact about the artifact,
independent of who signed the event. This enables the self-sovereign
publication path:

1. Build deterministically from a tag with the pinned chain.
2. Reproduce the published digest (or publish your own copy to any
   Blossom mirror).
3. Announce it as kind-1063, signed with **your own npub**.

The operator's personal policy is the reference example: *trust the
artifacts I signed with my own key; fall back to the official publisher
keys when I don't want to compile myself; trust nothing else by
default.* Determinism is what makes "I signed it myself" meaningful —
your signature covers bytes anyone can reproduce and compare, so it
certifies the build, not just the upload.

## The tools

| Tool | Role | Fails when |
|---|---|---|
| `scripts/verify_publication.sh` | **Gate** — a known version's expectations are announced by trusted keys and serve matching bytes from ≥2 mirrors | any expectation missing, untrusted, or hash-mismatched |
| `scripts/release-trust-scan.sh` | **Detector** — every announcement for the package, classified `trusted`/`UNTRUSTED` by the caller's key set; conflicts flagged | exit 1 only on a digest conflict in an immutable-channel (stable/beta/alpha/rc) version — never legitimate, attacker or compromised-key alike. Dev-channel digest churn (rebuilt `branch.height.sha` versions) is reported as noise, not gated |

## Drills (deliberate fakes)

Rules that keep a drill fake safe without weakening its realism:

1. **Untrusted by construction** — a fresh throwaway key, never added to
   any mirror or maintainer list. Its insecurity is then structural
   (zero write surface), not procedural.
2. **Marked three ways** — an impossible version string
   (`v0.0.0-testfake1`), the `dev` channel, and a `filename` tag that
   says `FAKE-DRILL`. Consumers filtering by stable/beta/rc channels
   never see it; anyone who does sees the marking in the event itself.
3. **Detected, then kept** — the standing drill event doubles as the
   scanner's regression fixture: `release-trust-scan.sh v0.0.0-testfake1`
   must always report exactly one UNTRUSTED row. If a scanner change
   makes the drill invisible, the scanner is broken.

The first drill event is live (published 2026-10-08 from throwaway key
`dc2dd7ce…`, x `3031044b98dde7b…`). A second, trusted-side fixture
exists implicitly: every real release the gate has verified.

## What this model does not claim

No key — trusted, official, or your own — makes a *build* trustworthy;
it makes a claim about bytes the consumer can independently reproduce.
The chain of custody for the source is the git history and its review
process; determinism extends that chain to the artifacts. A consumer
who trusts a key is choosing to outsource the reproduce-and-compare
step, nothing more.
