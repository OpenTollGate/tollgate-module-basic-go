# Spilman protocol sidecar and durable settlement ownership

**Status:** Proposed production ADR; records the approved capabilities-only
first milestone. Settlement and shipping requirements below are future gates,
not claims of implemented or enabled functionality.

**Date:** 2026-11-02

## Context

The Go daemon must remain `CGO_ENABLED=0`. A new pure-Go implementation of
Spilman signing, blinding, completion and recovery would duplicate protocol
cryptography. The wallet AF_UNIX client in `src/tollwallet/sidecar.go` is a
transport precedent, not a Spilman settlement service or a durable journal.

The inspected Rust reference (`OpenTollGate/tollgate-rs`, source snapshot
`6ce49426229ce2a6ac1844c13c117854807891ec`) supplies useful lifecycle evidence:

- `src/spilman/rail.rs:466–511` prepares a transition and freezes it in the
  ledger, reusing an existing preparation only for matching authorization.
- `src/spilman/rail.rs:529–580` verifies the stored preparation, claims
  `prepared → uncertain` before swap, and otherwise restores the original
  outputs. Empty restore explicitly does not permit another swap.
- `src/spilman/ledger.rs:360–408` freezes the latest accepted balance/signature
  transactionally and compares preparation plus prior state when advancing.
- `src/spilman/rail.rs:377–446` authenticates receipt retrieval against the
  accepted payment and returns sender proofs, not receiver proofs.

These are source observations, not Go implementation or executed-test evidence.
The reference's ledger and networking remain reference code: they do not become
an additional owner of Go settlement state.

## Decision

### Ownership and packaging

Keep the Rust protocol executable in this repository at `sidecar/spilman` and
package it separately from the Go daemon. Go is the single durable owner of
channel registration, accepted balances, close authorization, operation journal,
proof custody and settlement accounting. Go also owns trusted-mint policy,
keyset fetching and all mint network calls. Rust performs narrowly requested
protocol validation and cryptography; it owns no independent channel ledger and
must not implicitly swap during prepare or complete.

Do not link Rust into Go through CGO, replace the existing wallet backend, or
fork a second shipping pipeline. Shipping is later work: canonical recipes and
release pins belong to `FreedomTechFeed/packages`; its wrapper mirror is synced,
not independently edited. Extend the repository's generated build contracts and
canonical feed consumers together before shipping a new binary. Do not hand-edit
`packaging/build-contract.json`. Cross-architecture Rust/musl support, footprint
and loader compatibility require measurements, not inference from Go support.

When protocol integration is introduced, declare `spilman_protocol` in
`packaging/build-inputs.json`, initially at
`c2083defc2015ad0bae2a8a086a2876bc775ff79` of
`SatsAndSports/cashu_spilman_channels`, matching the inspected reference's
`Cargo.toml:80`. The manifest will be the build authority, with dependency/lock
and feed drift fences. This ADR does not modify that manifest or add the protocol
dependency to the capabilities skeleton.

### Local trust boundary and custody

Use AF_UNIX only, no network listener. The sidecar owns socket creation and
server-side peer authorization; Go owns client-side endpoint/peer verification.
The runtime directory is service-owned `0700`, socket `0600`; initially the
expected service identity is root. Verify Linux peer credentials against the
configured owner, reject unexpected owners or unsafe paths, and never unlink a
live listener. Packaging must preserve these permissions and supervision rules.
Filesystem isolation is not protection against a compromised root process.

The first milestone accepts no secrets. Before later signing is enabled, Go
must derive the existing v2 receiver secret from its owned merchant identity and
supply only that derived secret over the authenticated local channel. Never send
the merchant secret, use argv for secrets, or log RPC bodies. Rust must report a
receiver public key matching the advertised channel receiver; mismatch fails
readiness. Go retains the derivation identity across restart and recovery.

Prepared secrets, blinding factors, funding proofs and completed proof sets are
private durable Go records, protected at rest by restricted storage permissions
and backups. Rust may hold required material transiently, not as a second store.
Only authenticated sender receipts expose sender proofs. Receiver proofs remain
private; importing them into a wallet requires a separate idempotent handoff
journal, not a blind bearer-token Receive or a nominal balance adjustment.

### First milestone: transport only

Deliver only a capabilities-only Rust skeleton and a Go client. No signed
protocol operations, protocol readiness, mint calls, payment acceptance, close
HTTP route, service enablement or shipping package is implied by this milestone.

Landed layout:

- `sidecar/spilman/` — its own Cargo package (binary + library), not a member of
  any existing workspace. The protocol dependency is deliberately absent: see
  the Cargo manifest's comment.
- `src/spilmansidecar/` — the Go client, a package **inside the existing root
  module**. A nested `go.mod` would add a module carrier to the go-battery and
  build-contract machinery for a single package with no dependencies, so the
  client stays in the root module until it genuinely needs its own.

The planned v1 wire contract is deliberately small:

- Newline-delimited JSON, maximum **65536 bytes including the newline**, on
  requests and responses; reject oversized or unterminated frames.
- `version` is integer `1`. `request_id` and `operation_id` are nonempty strings
  of at most 128 ASCII characters, restricted to `[A-Za-z0-9_-]`.
- The only supported `method` is `capabilities`.
- Responses echo the exact version and both IDs and contain
  `capabilities: ["transport.v1"]` and an `error` string. Success uses `""`;
  other methods return `"unsupported_capability"` and perform no work.
- Validate framing/schema/version before dispatch. Go rejects mismatched IDs,
  unexpected versions and malformed responses. Bound each exchange by a finite
  deadline, with no unbounded buffering or automatic retry loop.

`request_id` correlates one exchange; it is not an idempotency guarantee.
`operation_id` identifies the logical operation across exchanges. For future
settlement it must be durably bound to the immutable prepared content; the same
ID with different content is a conflict. The skeleton only validates and echoes
IDs: it does not implement durable deduplication or settlement replay.

Names such as `validate_registration`, `prepare_close`, `verify_prepared`,
`complete_swap` and `complete_restore` describe **proposed, unimplemented**
operations, not available RPC methods. Adding them requires explicit versioned
schemas and capabilities. `transport.v1` must never satisfy settlement readiness.

### Future settlement invariant: freeze, claim, restore

Before enabling settlement, validate nested registration parameters and funding
against configured mint/keysets, channel identity, receiver, sender, capacity,
unit and expiry. Derive the channel secret from the owned receiver identity;
never trust payer-provided secrets or arbitrary mint URLs.

1. Authenticate close against the latest accepted payment. Atomically freeze
   that exact authorization and exclude further accepted balance changes.
   Persist the complete immutable preparation: exact swap request, output
   secrets/blinding, keyset snapshot, channel binding, schema/protocol revision
   and content-bound operation ID. Preparation performs no mint I/O.
2. Verify the stored preparation. Durably compare-and-swap `prepared → uncertain`
   against the exact stored content **before** Go submits the swap. A failed
   freeze or claim commit means zero swap calls. Never hold a DB transaction
   across network I/O. Only the successful claimant can submit that exact swap.
3. Complete using the pinned protocol, then atomically persist both proof sets,
   verified sums and `closed`. Account from verified outputs, not nominal credit.
4. After timeout, process death, partial write, lost/malformed RPC or mint
   response, or failed completion commit, retain uncertainty. Recover with
   NUT-09 restore of the **exact persisted outputs**, never regenerated outputs
   or a repeated swap. Empty, partial or mismatched restore stays unresolved;
   even a crash after claim but before send cannot authorize a blind retry.
   NUT-07 spent-input status is diagnostic, not recovered value.
5. Closed replay returns the authenticated durable receipt without mint calls.
   Conflicting authorization or operation content fails closed.

## Consequences and gates

This preserves a pure-Go daemon and reuses protocol crypto, at the cost of a
second executable, local secret exposure and explicit lifecycle supervision.
Capabilities tests must exercise bounds, malformed frames, invalid IDs/versions,
response correlation, disconnects and unsupported-method refusal. They prove
transport behavior only.

Settlement remains disabled until pinned-core vectors, trusted registration,
secret/proof isolation, crash-boundary and concurrency tests, exact-restore
failure cases, and real-mint proof spendability pass. Shipping additionally needs
canonical feed/build-contract integration and target runtime acceptance evidence
owned by `physical-router-test-automation`, bound to source/artifact hashes.
Neither Rust source inspection nor a capabilities handshake proves Go settlement,
router access or a genuine browser purchase flow.

## Milestone 2: `validate_registration.v1` (registration-time validation only)

The capability answers one narrow question: **do these parameters describe the
channel the payee thinks they describe?** It is offline and value-incapable by
construction — no signing, no swap, no restore, no mint call, no network — and
the shipped Go binary gains no protocol linkage at all (the crate lives behind
the sidecar's `protocol` Cargo feature; `--no-default-features` still builds a
transport-only sidecar that does not advertise the capability).

What it refuses, each verified against the real binary:

- `receiver_pubkey_mismatch` — the claimed receiver pubkey is not the one
  derived from the payee's own receiver secret. The secret is the only value a
  caller cannot lie about, so it is what the check is anchored to.
- `mint_not_trusted` — the registration, or the nested params, name a mint other
  than the supplied trusted URL. Comparison is against the **trusted** URL, not
  outer-vs-nested, because two attacker-chosen claims agreeing proves nothing.
- `keyset_mismatch` — the trusted keyset's declared id is not the id its own
  keys produce, its unit or `input_fee_ppk` disagrees with the registration, or
  the nested params name a different keyset than the trusted one.
- `nested_params_mismatch` — capacity, funding amount, expiry, setup timestamp,
  maximum output amount, input fee, sender pubkey or keyset id disagree between
  the nested `ParamsJSON` and the outer record.
- `unsupported_unit`, `zero_capacity`, `registration_expired`,
  `invalid_lifetime`, `sender_equals_receiver`, `channel_id_not_derivable`.

The reply carries only values the sidecar derived — `channel_id` and
`canonical_params_json` come from the pinned crate's own serializer, never an
echo — plus `mint_funds_asserted: false`, which is required rather than
optional: absence would read as "no claim made" and is indistinguishable from a
claim in a log.

**This does not make settlement work.** It closes the reviewer's nested-params
and untrusted-mint gaps *at registration time*: a payer can no longer present
params that name a different mint, keyset, capacity or expiry than the channel
the payee will record, and a payee cannot be talked into deriving a channel id
for a key they do not control. Everything downstream — preparation, freeze,
swap, exact-restore, receipt replay — remains absent and unproven, exactly as
listed above. A successful validation is a statement about parameter agreement
and nothing about whether the mint holds funds or whether a swap will settle.
