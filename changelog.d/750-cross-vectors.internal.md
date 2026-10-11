- **Go's advertisement semantics are pinned to canonical cross-vectors.**
  The repo is now the canonical home of the TollGate cross-implementation
  test vectors (#750): `cross-vectors/tollgate-cross-vectors.json` carries
  each TIP-01/TIP-02 advertisement in two encodings — a signed kind-10021
  Nostr event (the discovery wire this module actually consumes) and a
  `cbor_hex` of the semantic data model defined by the new
  `cross-vectors/advertisement.cddl` — plus the encoding-neutral expected
  semantics, and a tampered-signature vector that must fail authenticity
  while staying decodable. `src/tollgate_protocol` consumes an embedded
  byte-identical copy (cashu-cross-vectors convention) in its conformance
  test: both decodings must agree with the expected fields. The Rust
  wire-protocol CDDL is ported alongside (provenance banner naming the
  source commit), and `tests/contract/check-cross-vectors-sync.sh` fails
  when any copy drifts from the canonical file. Vectors are regenerated
  deterministically by `cross-vectors/generate`
  ([#750](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/750)).
