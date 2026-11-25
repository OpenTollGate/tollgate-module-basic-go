# FIPS exit consumer foundations

This package provides pure address derivation and configuration validation,
not an operational exit consumer. `Derive`
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
bits: config rejects a collision between local and exit, while collisions with
other peers must be rejected by a future allowlist/integration layer.
An address alone does not authorize access or prove possession of a key.

## Static configuration contract

`Config{}` is disabled; `Validate()` returns nil for disabled configs and ignores
all other fields. Enabling requires explicit `Selection: GuestForwarding`, two
nonzero decoded public keys, guest/FIPS/tunnel interface names, a routing table,
a rule priority and inner IPv4 MTU. No defaults are inferred from device names.
All-traffic, proxy/local-output and unknown selectors are rejected when enabled.
A proxy-terminating bonding server needs a separate output selector; forwarded
guest ingress cannot identify its locally generated proxy traffic.

Validation requires distinct identities and derived outer/inner addresses and
pairwise distinct interface names. The deliberately conservative name subset is
`[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,14}` (15 ASCII bytes, no leading hyphen/dot).
Keys remain caller-validated x-only identities, not parsed npubs or proof of
ownership; zero keys are rejected as missing, not as a general curve check.
Routing table 0 and Linux default/main/local tables (253/254/255) are refused.
Priority must be 1..32765, after the standard local rule and before main; this
range is policy, not evidence that a priority or table is available.

Inner MTU must be 576..1240. The lower bound is this consumer's conservative
IPv4 policy. The upper bound follows the 1280-byte mesh packet minus 40-byte
outer IPv6 header in [v0.11.0 shim/src/exit.rs](https://github.com/fr34aky/fips2go/blob/v0.11.0/shim/src/exit.rs)
(`MSS_IPV4` rationale). [v0.11.0 entrypoint.sh](https://github.com/fr34aky/fips2go/blob/v0.11.0/exit-node/entrypoint.sh)
sets tunnel MTU to observed FIPS MTU minus 40. This validator does **not** observe
that MTU or claim fragmentation/oversized-UDP support.

### Mandatory future integration preflight (not implemented)

Static validation success is **not permission to apply**. Before any mutation,
the integration must obtain current environment facts and refuse conflicts:

- Verify guest/FIPS interface existence and roles, tunnel ownership/name
  availability, local identity ownership and its FIPS address.
- Check derived addresses against existing addresses/routes and peer allowlists;
  verify the dedicated table and rule priority are unused (or owned by this
  consumer), including bonding policies and actual rule ordering.
- Require inner MTU <= observed FIPS MTU - 40 and verify the path constraints.
- Establish fail-closed forwarding/DNS/native-IPv6 policy and preserve portal
  authorization and underlay routing before steering any selected packets.

Recheck facts under the future applier's ownership/serialization contract;
configuration alone cannot discover live collisions or prevent later drift.

No root configuration schema/main wiring, command runner, routing, firewall,
DNS, tunnel lifecycle, hardware access, runtime integration, payment or paid-exit
behavior is implemented or enabled. No existing runtime calls this package.
There is no persistent state or irreversible operation; all functions are pure.

From `src/` run `go test -race -count=1 -cover ./fips_exit` and
`go vet ./fips_exit`. These are package-level checks, not dataplane evidence.
