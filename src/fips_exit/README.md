# FIPS exit consumer foundations

This package provides pure address derivation, configuration validation and an
argv operation planner, fw4 guard renderer and read-only link/route collector,
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

### Mandatory future integration preflight (link/route slice implemented below)

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

No root configuration schema/main wiring, mutating production executor, applied routing, firewall,
DNS, tunnel lifecycle, hardware access, runtime integration, payment or paid-exit
behavior is implemented or enabled. No existing runtime calls this package.
There is no persistent state or irreversible operation; only the injected
read-only collector observes external state.

### Linux bounded read-only runner

`CollectLocalLinkRoutes(ctx, config, timeout)` opts into `RunIPProbe` without any
main/daemon wiring. Only the five observer argv shapes are accepted: JSON link
and address show, IPv4 rule show, IPv4 route show table all, and IPv6 route get
with literal unzoned IPv6 destination/from addresses. Add/del/flush, arbitrary
programs and extra arguments are refused. There is no shell or PATH lookup:
fixed system ip paths must resolve to root-owned executables and non-writable
root-owned ancestors. This assumes trusted root and a stable system installation;
it is not protection against concurrent privileged replacement of the binary.

Capture enforces a combined stdout/stderr ceiling (maximum 1 MiB) during writes,
kills the process group on overflow/cancellation, waits for the direct process,
and discards partial output. A 30-second runner ceiling applies even without a
caller deadline; inherited-pipe cleanup has a 100 ms WaitDelay. Successful JSON
comes only from stdout; nonzero status is always failure. Trusted ip does not
spawn children; process-group cleanup also covers unexpected ordinary children
(not hostile programs that deliberately escape the process group).

Local compatibility tests observed `linkmode`, address `metric`, and
`stable-privacy`; these explicitly typed metadata fields are accepted without
changing required UP/address/MTU/route/rule checks. Unknown control fields remain
refused. Real-host capture is opt-in, writes potentially sensitive local inventory
outside git, and uses a clearly synthetic config which should be refused:

```sh
# From src; create a private output directory first.
FIPS_PROBE_REPORT_DIR=/absolute/private/report-dir GOTOOLCHAIN=go1.26.10 \
  go test ./fips_exit -run TestLocalReadOnlyProbes -v
GOTOOLCHAIN=go1.26.10 go test -race ./fips_exit -cover
GOTOOLCHAIN=go1.26.10 go vet ./fips_exit
```

A refusal proves refusal only, not a working exit. DNS, effective firewall rules,
identity ownership, lifecycle/rollback and paid runtime remain unimplemented.


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
For the separate pure `BuildRecoveryPlan` API, see the
[bounded route-recovery contract](../../docs/architecture/fips-exit-route-recovery.md),
including ownership evidence, refusal conditions and verification limits.

Router guest ingress is the first slice. Bonding-server proxy egress via a
separate UID/cgroup/namespace selector remains required later, not supported or
replaced by this selector. Paid-service work follows a working transport.

## Pure fw4 guard renderer

`RenderFW4(config, portalInterface)` returns an inet-fw4 **table-body** fragment
or an error with empty output. Disabled returns empty output, even with invalid
unused fields. The caller supplies the resolved `$tg_portal_if` value from
`00-tollgate-defs.nft`; mismatch with `GuestInterface` is refused. This is only
static consistency, not a probe of the router. Rule lines use that existing
define and a dedicated `tg_fexit_tunnel_if` define rendered from the validated
config, never bridge literals. A future installer must persist the fragment
under `/etc/nftables.d/` after `00-tollgate-defs.nft`, inside `table inet fw4`,
exactly once; no runtime `nft add/insert` is generated or executed.

The forward guard at priority -2 precedes the existing NDS bridge (-1) and fw4
filter (0). It drops all selected guest native IPv6 forwarding and all selected
guest forwarding off the tunnel (including other routed local networks).
Router-local onboarding/DHCP/DNS/portal packets take INPUT, not FORWARD; this
fragment leaves guest INPUT and all OUTPUT to existing policy. A separate INPUT
guard at priority -2 drops decapsulated tunnel ingress, except IPv4 ICMP with
conntrack state related (including error/PMTU semantics), which continues to
later fw4 policy. It does not grant tunnel-originated router service access.
Tunnel-to-guest forwarding is limited to IPv4 established/related conntrack
traffic, including related ICMP. All other tunnel ingress forwarding is dropped,
including new/invalid/untracked guest-bound flows and traffic routed to non-guest
interfaces even if established/related. Both guards match the tunnel interface,
not outer FIPS ingress; outer control traffic and guest onboarding remain under
existing policy. Unrelated ingress is unchanged. Same-bridge switching is outside
this L3 guard. These are persistent-include contents only, not installed rules.

Both IPv4 TCP SYN directions clamp **only larger** advertised MSS to inner MTU
minus 40, never increasing a smaller MSS (no option insertion when absent).
Postrouting NAT at priority 99 precedes fw4 srcnat (100): `snat ip to` uses the
derived local IPv4 host address (the tunnel's /32), and requires both guest
ingress AND tunnel egress plus IPv4. Router-generated, unrelated guest/private,
native IPv6 and FIPS outer traffic are not selected for this SNAT. Existing
conntrack NAT bindings, flow offload and competing earlier hooks need separate
lifecycle/integration validation; rendering cannot evict stale state or prove
these guards see every packet.

**No paid-gate bypass or connectivity claim:** there are no rule-level accepts,
mark writes or NDS edits. Base-chain policy accept/return merely continues to
other chains; it cannot override a later terminal reject. In particular, the
existing NDS unmarked terminal reject still rejects selected guest IPv4 to the
tunnel. Paid/trusted classification, preauth exceptions, fw4 zone forwarding,
and return-path compatibility must be separately verified against effective
NDS/fw4 rules. Do not add a broad accept to try to solve that incompatibility.

**DNS remains separately required:** a guest resolver/redirect must route
non-`.fips` queries exclusively via the exit resolver, with no resolv.conf/direct
fallback; `.fips` resolution stays with FIPS. Local resolver traffic is OUTPUT,
not covered by the guest ingress guard/SNAT. This fragment deliberately does
not implement DNS redirects, resolver configuration, or RA/DHCPv6 changes.
A rendered string sets **none** of `GuardReadiness`: installation, reload/failure
behavior, effective DNS/NDS enforcement, offload handling, and kernel dataplane
checks remain prerequisites for any applier. DNS escape, the remaining observer checks,
compatibility dataplane proof (including paid/NDS authorization and return paths),
and runtime/applier integration remain unresolved. No runtime calls are wired here.

`TestRenderNFTCheckIsolated` runs `nft -c` on the actual generated fragment inside
an isolated user+network namespace when available, otherwise explicitly skips
with `NOT RUN`. It never checks against or mutates the host ruleset. Even a
successful check proves syntax/kernel acceptance, not packet-path behavior.

From `src/`, pin checks explicitly to Go 1.26.10:
`GOTOOLCHAIN=go1.26.10 GOFLAGS=-mod=readonly go test -race -count=1 -cover ./fips_exit`
and `GOTOOLCHAIN=go1.26.10 GOFLAGS=-mod=readonly go vet ./fips_exit`.
These are package-level checks, not dataplane evidence.

## Pure bootstrap DNS renderer

`RenderBootstrapDNS(enabled, listen, portalName, portalIP)` renders DNS-only
dnsmasq settings, not a running resolver. Disabled returns empty output; errors
return empty output. Listen requires a concrete unzoned unicast IP and port
1024..65535; the portal IP must also be unzoned unicast. Loopback/private/link-local
addresses are permitted; interface subnet broadcasts cannot be inferred here.
The exact ASCII hostname uses 1..63-byte LDH labels, at most 253 bytes overall,
without a trailing dot, wildcards or directive syntax. Only its `host-record` is
provided: `local=/#/` blocks upstream forwarding for all names, with `no-resolv`,
`no-hosts`, and `no-poll`. No DHCP ranges/services or upstream servers are enabled.

A future caller **must** launch with an isolated `--conf-file` and a dedicated
unprivileged user, with no inherited config directories, options or records.
This helper does not choose a UID, start a process, write files, redirect guest
DNS, alter firewall/schema/hardware, provide paid DNS, or set `GuardReadiness`.
Binding/port availability and effective packet isolation still need separate
runtime verification; rendering is not evidence of a working guest resolver.

## Read-only link/route collector

`CollectLinkRoutes(ctx, config, runner, timeout)` requests five typed argv probes:
`ip -j link show`, `ip -j address show`, `ip -j -4 rule show`,
`ip -j -4 route show table all`, and `ip -j -6 route get EXIT from LOCAL`.
Endpoints are derived from configured keys. Disabled makes zero calls.
The injected runner must execute directly without shell, resolve trusted `ip` in
the intended namespace, honor context cancellation, stop capture at the supplied
1 MiB limit and reject truncation. A total timeout in (0,30s] is mandatory.
The collector checks cancellation before/after calls and rejects oversized output;
it cannot forcibly stop a callback which violates its cancellation contract.
There is no production executor or runtime wiring. This is the only non-pure API.

Unknown/malformed JSON, incomplete inventories and failed commands (even with
partial valid stdout) return zero evidence and an error. Supported iproute2 fields
are allowlisted; new fields require review. Checks cover guest/FIPS existence and
administrative UP, FIPS MTU, derived local address usability, tunnel occupancy,
IPv4 table/priority conflicts, derived inner address/route overlap and a direct
source-specific unicast exit route via FIPS. Only standard local/main/default
IPv4 rules are accepted: custom/bonding rules and unknown table aliases refuse.
`table all` avoids interpreting missing-table command errors as evidence of safety.

`LinkRouteEvidence` is separate from `Environment` and `GuardReadiness`. It sets
**no readiness booleans**: not Verified, DNS, NAT, MSS, firewall or NDS. Kernel
routing into a TUN does not prove daemon identity ownership, exit registration,
peer discovery, authenticated reachability or functioning transport. Interface
names do not establish semantic roles. Still required: daemon identity/discovery,
peer allowlist collision checks, IPv6 RPDB/table inventory and mark/ingress-specific
lookups, effective DNS/forwarding/native-IPv6/NAT/MSS/NDS guards, offload/conntrack,
rp_filter and restart/reload/failure dataplane proof. Sequential observations are
not atomic; a future locked applier must recheck. No writes or activation occur.

Fixtures use realistic iproute2 recorded-format JSON with synthetic names and
identities, not captures from target hardware. Tests cover valid evidence, missing
interfaces, conflicts, stale/wrong routes, invalid/partial output, command failure,
cancellation, timeout and limits. They are not daemon or dataplane evidence.
