- **The fake-release detector exists, and the first drill fake is live.**
  `scripts/release-trust-scan.sh` scans every kind-1063 announcement for
  the package across the channel relays and classifies each publisher
  against the caller's trust set (defaults: the two release publishers)
  — the complement to `verify_publication.sh`'s expectation gate, and
  the enforcement half of the new trust model
  ([docs/release-trust-model.md](../docs/release-trust-model.md)):
  trust is consumer-scoped, deterministic bytes are the anchor that
  makes self-published, self-signed releases meaningful, and deliberate
  fakes train the detection. The first drill event (`v0.0.0-testfake1`,
  dev channel, throwaway key `dc2dd7ce…`) is live on the relays and
  doubles as the scanner's regression fixture: a filtered scan must
  always show it as exactly one UNTRUSTED row. Digest conflicts on
  immutable-channel versions — the strongest forgery signal, including
  trusted-key compromise — fail the scan; dev-channel rebuild churn is
  reported as noise
  ([#762](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/762)).
