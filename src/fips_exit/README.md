# FIPS exit address derivation

This package is an address-only foundation, not an exit consumer. `Derive`
accepts a decoded `[32]byte` x-only public key and returns `netip.Addr` values:

- Outer IPv6: `fd || SHA256(key)[0:15]`.
- Inner IPv4: `100.64.0.0 OR (first 24 digest bits AND 0x3fffff)`.

The authority is [fips2go v0.11.0 exitctl.py](https://github.com/fr34aky/fips2go/blob/v0.11.0/exit-node/exitctl.py),
functions `fips_address` and `inner_ipv4`. Tests use the three independent
phone-generated vectors in [test_exitctl.py](https://github.com/fr34aky/fips2go/blob/v0.11.0/exit-node/test_exitctl.py),
with public keys decoded by that tag's `npub_to_pubkey`. Expected outputs are
literal upstream fixtures, not computed with the Go implementation.

Identity parsing and validation are the caller's responsibility; this package
accepts exactly 32 bytes by type, does not parse npub/hex strings, and does not
check curve membership. This is compatible with the upstream hash operation,
which also does not check curve membership. Inner IPv4 has only 22 identity
bits: collisions must be rejected by a future allowlist/integration layer.
An address alone does not authorize access or prove possession of a key.

No configuration, routing, firewall, DNS, tunnel lifecycle, hardware access,
runtime integration, payment or paid-exit behavior is implemented or enabled.
No existing runtime calls this package. There is no persistent state or
irreversible operation; derivation is deterministic and side-effect-free.

From `src/` run `go test -race -count=1 -cover ./fips_exit`.
