# cross-vectors — canonical TollGate cross-implementation test vectors

This directory is the **canonical home** of TollGate's cross-implementation
test vectors (issue
[#750](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/750):
the originally-planned canonical home, the Rust repo, was archived before
the file existed, so this repository owns it).

## What is here

| File | Role |
| --- | --- |
| `tollgate-cross-vectors.json` | The canonical vectors. Each vector carries one advertisement in **two encodings** — a signed Nostr kind-10021 event (the TIP-01/TIP-02 discovery wire) and `cbor_hex` (the semantic data model) — plus the encoding-neutral `expected` semantics both decodings must produce. One vector carries a tampered signature that MUST fail authenticity while remaining decodable. |
| `advertisement.cddl` | The CDDL for the advertisement's semantic data model (what `cbor_hex` encodes). Authored here; style-matched to the wire schema below. |
| `tollgate.cddl` | Port of the Rust implementation's normative **wire-protocol** schema (peer-to-peer channel messages), kept here per #750's amendment so the contracts stay reviewable in one place. Provenance banner at the top names the source commit. Not yet exercised by vectors — the current vector set covers the discovery/ad surfaces; channel-message vectors are a follow-up when a second channel implementation exists. |
| `generate/` | The deterministic generator. `cd cross-vectors/generate && go run . -root ../..` rewrites the canonical file AND every embedded copy with identical bytes. It is its own tiny Go module (it lives outside `src/`, so it never enters the shipped module graph or the deps-sync set). |

## The embedded-copy convention

Consumers do **not** read this directory at test time. Each implementation
embeds a byte-identical copy next to its tests, with the file's
`provenance` field pointing back here (the cashu-cross-vectors model), and
a mechanical sync check fails CI when a copy drifts from the canonical
file. To change the vectors: edit `generate/main.go`, run it, and commit
canonical file and copies together — the generator writes them all, so a
half-update cannot happen.

Current consumers:

- Go: `src/tollgate_protocol/testdata/tollgate-cross-vectors.json`
  (pinned by `crossvectors_test.go`; sync-enforced by
  `tests/contract/check-cross-vectors-sync.sh`).
- Rust: follow-up, wherever the Rust protocol code consumes from
  (OpenTollGate/tollgate-rs).

## Fixture keys

The vectors are signed by a throwaway key committed in `generate/main.go`
on purpose: fixtures must be reproducible, and a fixture credential that
looks like a live one is a bug, not a feature.
