# FIPS exit consumer foundations

This package provides pure address derivation, configuration validation and an
argv operation planner,
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

### Mandatory future integration preflight (observation not implemented)

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

No root configuration schema/main wiring, command runner, applied routing, firewall,
DNS, tunnel lifecycle, hardware access, runtime integration, payment or paid-exit
behavior is implemented or enabled. No existing runtime calls this package.
There is no persistent state or irreversible operation; all functions are pure.

## Pure fresh-install routing planner

`BuildPlan(config, environment, guards)` returns typed `Operation` values with
`Program` and separate `Args`, never shell text. Disabled returns zero operations
without requiring facts or guards. Enabled returns no partial plan on error.
Only a fresh installation is supported: **even previously owned tunnel/table/rule
resources are refused**. There is no replace/flush/delete or teardown plan.

The sequence is fixed:

1. Dedicated-table IPv4 unreachable default, metric 42760, protocol static,
   no expiration or tunnel-device dependency (permanent for the running kernel,
   not persisted across reboot by this package).
2. Kernel IPv4-in-IPv6 `ipip6` tunnel bound to the FIPS interface and derived
   local/exit IPv6 addresses; `encaplimit none`, `hoplimit 64`.
3. Explicit MTU, derived local IPv4 `/32`, then tunnel link up.
4. Dedicated-table tunnel default, metric 100.
5. **Last:** IPv4 rule selecting only guest incoming interface, at the explicit
   priority, looking up the dedicated table.

No main-table defaults or FIPS outer IPv6 routes/rules are changed. The assigned
address necessarily creates its kernel local-table host route. The unreachable
route alone is **not a kill switch**: earlier rules and forwarding policy can
bypass it. `ValidateOperations` rejects edits, omissions and reordering (including
steering before the unreachable default); it does not execute or inspect anything.

### Environment and guard caller contract

The future observer/applier MUST collect complete current facts in the target
network namespace under an exclusive ownership lock. `Environment.Verified` is
an attestation, never a user-facing bypass switch. Populate interface MTUs from
observation, verify roles and local identity ownership/curve validity, and bind
`LocalPublicKey`/`LocalFIPSAddress` to the running FIPS daemon and its interface.
The planner checks the key/address match and observed FIPS MTU minus 40.

Positive attestations are mandatory, and unknown/false is refused:

- `TunnelNameUnused`, `TableUnused`, `RulePriorityUnused`: complete link, route
  (all relevant families) and rule inventory; no adoption of existing ownership.
- `AddressesConflictFree`: derived inner address conflicts against all local
  addresses/routes and exit allowlist peers have been excluded.
- `PolicyOrderSafe`: actual RPDB ordering, including bonding rules, local rule
  precedence and earlier bypasses has been checked; no reliance on numeric range
  alone. Existing local portal access must remain intact.
- `OuterRouteVerified`: the derived exit IPv6 is reachable through the existing
  FIPS interface, without recursive tunnel routing or guest policy capture.

`GuardReadiness` requires **all** forwarding kill-switch, DNS isolation, native
IPv6 isolation, inner-address SNAT, MSS and portal-authorization readiness flags.
These attest to effective, separately implemented protections for this exact
configuration, not merely generated files. DNS must have no direct fallback;
IPv6 internet bypass must be denied; forwarding must stay fail-closed through
exit loss/reloads without bypassing NDS, with tunnel-only authorized return
traffic. MSS must respect inner MTU minus 40 (at most 1200). This package installs
**none** of these protections and cannot establish those attestations itself.

Before application, re-observe facts/guards and validate the exact operation
sequence under the same lock. Resolve `ip` to a trusted executable; pass argv
directly, never through a shell. Execute sequentially, verify each result, abort
on failure, and never install the final rule after a failed prerequisite. Keep
guards and the unreachable default during faults; lifecycle, persistence,
recovery, safe teardown and continuous drift handling need a later applier.
Do not replay this fresh-install plan as a convergence loop.

Router guest ingress is the first slice. Bonding-server proxy egress via a
separate UID/cgroup/namespace selector remains required later, not supported or
replaced by this selector. Paid-service work follows a working transport.

From `src/`, using the manifest-pinned Go toolchain, run
`go test -race -count=1 -cover ./fips_exit` and
`go vet ./fips_exit`. These are package-level checks, not dataplane evidence.
