# MPTCP multi-WAN bonding — integration design

Status: **design + validated core** (this PR). The transport itself has been
proven on real hardware; this package is the TollGate-side seam.

Related: [`mt6000-mptcp-bonding`](https://github.com/felixfelix-bot/mt6000-mptcp-bonding)
holds the Ansible that builds and verifies the bond, plus the full list of
platform gotchas.

## The idea in one paragraph

TollGate's job is portal + payment + gated access. Its **uplink** is whatever
interface currently holds the default route. Multi-WAN bonding does not change
any of that: it replaces *one* uplink with a **kernel MPTCP meta-connection**
that spreads packets over several uplinks (a physical port, a wireless STA,
…), which a remote de-aggregation server reassembles.

```
guest ─► nodogsplash portal ─► payment gate ─► forwarding
                                                   │
                                      ┌────────────▼─────────────┐
                                      │ MPTCP bonded uplink      │  ← only this changes
                                      │  subflow A  +  subflow B │
                                      └──────────────────────────┘
```

Portal, Cashu payment verification, session accounting and merchant mode are
**untouched**. That is what makes it tractable.

## Why `policy: guest_only` is the default

`guest_only` steers only forwarded guest traffic into the bond. The box's own
management traffic (SSH, portal admin, payment callbacks) keeps using the
primary uplink.

This is not a nicety. During bring-up, a naive transparent-proxy rule diverted
traffic *addressed to the router itself*, and the operator lost shell access to
a device in the field (`kex_exchange_identification: Connection closed`).
A paid device must not be able to lock you out because you enabled a feature.
`policy: all` exists, but it is opt-in and it moves the default route.

## What this PR adds

```
src/mptcp_bonding/
  config.go        Config, Validate, ShouldSteer, LocalDontSteerAddrs
  config_test.go   10 tests / 32 cases, 92.1% coverage
```

It is deliberately **pure**: no syscalls, no file I/O, no netlink. All the
I/O-bound work (applying nft rules, adding `ip mptcp endpoint`s, running the
endpoint keeper) belongs in a thin applier and is not in this PR.

Keeping the decision logic pure is what makes it testable on a workstation with
no router attached, and it means the "should this packet use the bond"
question has exactly one answer, in one place, used by both the generated
firewall rule and any Go code.

### Validation is strict on purpose

A half-configured bond is worse than none, because it silently changes how a
paying customer's traffic leaves the device. `Validate` therefore rejects:

- fewer than **2 uplinks** (one uplink is a config error, not a degenerate bond
  — it would rewrite routing for no benefit while adding every failure mode);
- a server without `host:port`, or a non-numeric / out-of-range port;
- `policy: guest_only` without a parseable `steering_cidr`;
- `socks_port == tproxy_port`;
- duplicate or empty uplink interfaces;
- an unparseable `fwmark`.

`enabled: false` validates cleanly with no other fields set — an unconfigured
device must not fail.

## Deployment shape

```jsonc
{
  "mptcp_bonding": {
    "enabled": false,                 // opt-in, always
    "policy": "guest_only",
    "server": "203.0.113.10:65101",   // MPTCP-capable far end
    "method": "chacha20-ietf-poly1305",
    "uplinks": [                       // >= 2
      { "interface": "eth1" },
      { "interface": "phy1-sta0" }
    ],
    "steering_cidr": "10.115.249.0/24",
    "subflows": 8,
    "socks_port": 1081,
    "tproxy_port": 1082,
    "fwmark": "0x1",
    "fwmark_table": 100
  }
}
```

`password` is supplied at runtime through the existing config manager and must
never be committed.

## Integration hazards (measured, not theorised)

1. **Firewall coexistence.** TollGate already drives `fw4`/nftables and
   transparent redirects. The bond needs its own table with a `prerouting`
   hook at `priority mangle - 10`, its own `ip rule` entries (pref 90/100/101)
   and its own routing tables. Keep them **separate** from TollGate's and never
   flush the shared ruleset.
   *Observed failure:* a plain `redirect to :port` on the box's own traffic
   looped the proxy into itself. The working form is **TPROXY with an explicit
   mark**, plus `return` for router-local destinations.

2. **Portal redirect vs TPROXY ordering.** Both hook `prerouting`. The portal's
   auth decision must run **first**; the bond should act only on traffic the
   portal has already admitted.

3. **Payment accounting.** Bill on TollGate's **application-level** counter,
   which sits *above* the bond. Do **not** sum per-uplink byte counters — that
   double-counts retransmissions and can bill a customer twice for the same
   bytes. An indivisible `step_size` can also straddle a mid-session failover;
   prefer a byte- or time-continuous model.

4. **The far end must speak MPTCP.** A plain endpoint silently yields a
   single-path tunnel: the client still reports MPTCP, but only one subflow is
   ever active and there is no bond. Verify with `ss -Mti` and look for
   `tcp-ulp-mptcp` **and** a token.

## What is proven vs not

Proven (on a GL-MT6000, OpenWrt 25.12.5, kernel 6.12.94):

- both uplinks up simultaneously, MP_JOIN of a second subflow captured on the
  wire (`SYN join → SYN-ACK → HMAC join`);
- `subflows:2` / `local_addr_max:2` on a live socket;
- MPTCP to a remote de-aggregation server verified end-to-end, and LAN-client
  steering into the bonded path.

**Not proven:**

- **mid-transfer failover** — in every attempt no second subflow was live at
  the moment the uplink was cut, so there was nothing to fail over to. Untested,
  not disproven.
- **aggregation beyond ~1.2–1.3×.** Both subflows terminate at the same remote
  host, so they share the bottleneck and the RTT. This is **resilience and
  session continuity**, *not* "2×". Do not market it as doubling.

## Endpoint provisioning (`mptcp_endpoints.go`)

Enabling MPTCP on the node is **not** the same as having a working bond. The
kernel side must be configured, and the lab proved the failure modes are silent:
a bond can look completely configured and still carry everything on one path.

Three facts, each earned from a lab session (GL-MT6000 `tollgate-A04E`, OpenWrt
24.10.2, kernel 6.12.94, MPTCP enabled):

1. **A `subflow`-only endpoint is useless.** Without `signal` the kernel never
   sends `ADD_ADDR`, so the peer never learns the address and no second subflow
   can ever form. It looks configured and does nothing. Every endpoint must be
   `signal subflow`. Verified: the router's endpoints were `subflow` only, and
   `MPTcpExtMPJoinSynRx` stayed at 0 across every run.
2. **`subflow`-only was the *second* bug, not the first.** Even after fixing it,
   no subflow formed — because the two hosts shared a single physical path
   (`ip route get` resolved every destination to the same interface and next
   hop). MPTCP correctly refuses to create a subflow over a path it already has.
   **Two genuinely disjoint L2 segments are a prerequisite, not a detail.**
3. **The kernel default of 2 subflows is too small.** `ip mptcp limits set
   subflow N add_addr_accepted N` must be raised to cover the uplink count, or
   the extra uplinks are configured but never used.

`EndpointPlan()` renders one `ip mptcp endpoint add <addr> dev <iface> signal
subflow` per uplink that has a known address; `LimitsSetCommand()` renders the
matching ceilings, raising the subflow ceiling to the uplink count but never
lowering it below. Both return nothing when the bond is disabled, so a
non-participating device renders no commands.

`PrerequisitePackages()` names `ip-full`: OpenWrt's default BusyBox `ip` has no
`mptcp` subcommand, so every command above fails with a usage error on a stock
image.

### What the router side can and cannot do

TollGate can prepare a node: install `ip-full`, enable MPTCP, register endpoints,
raise limits. **It cannot make the socket multipath-capable** — FIPS creates its
own socket, and an ordinary `IPPROTO_TCP` socket will not form subflows however
well the host is configured. Endpoint provisioning is *necessary but not
sufficient*; the socket change belongs in FIPS. See "FIPS transport patch" below.

### Why this is not a per-node bonding daemon

A "bonding client/server" pair on each node would re-implement in userspace what
the kernel already does: MPTCP subflows are exactly "nodes connect to each other
over multiple paths". Adding a daemon would add a process, a protocol, and a
failure mode to duplicate the kernel's scheduler. The only missing piece is the
socket protocol, which is a transport-layer change.

## FIPS transport patch (carried on a fork, not PR'd upstream)

The multipath capability itself is a small, wire-format-neutral change to FIPS's
TCP transport, carried on a fork:

**`felixfelix-bot/fips` @ `pr/tcp-mptcp-option`**
<https://github.com/felixfelix-bot/fips/tree/pr/tcp-mptcp-option>

Base: `jmcorgan/fips` `master` @ `d769e50`. Commit
`36f82c86` ("feat(tcp): optional MPTCP (multipath TCP) for the TCP transport").

| File | Site | Change |
|---|---|---|
| `src/config/transport.rs` | `TcpConfig` | add `mptcp: Option<bool>` + `mptcp()` accessor (default false) |
| `src/transport/tcp/mod.rs` | `connect_to_any_addr` | take `mptcp: bool`, call `connect_stream` |
| `src/transport/tcp/mod.rs` | `connect_stream` (new) | try MPTCP, fall back to plain TCP |
| `src/transport/tcp/mod.rs` | `try_connect_mptcp` (new) | `socket2` socket with `Protocol::MPTCP`, cfg-gated to Linux |

Design points that matter:

- **Socket creation, not `configure_socket`.** MPTCP is selected when the socket
  is created; it cannot be retrofitted via `SockRef`. `configure_socket` (NODELAY,
  keepalive, buffers) is unchanged and applies identically to an MPTCP socket.
- **`socket2` already has `Protocol::MPTCP`**, cfg-gated to Linux inside the
  crate, so no hand-rolled protocol number and no cross-platform breakage: the
  non-Linux arm returns `Unsupported` and macOS/BSD builds never name the symbol.
- **Fallback is mandatory and quiet.** Old kernels, MPTCP compiled out, and
  restrictive sandboxes all fail socket creation; the transport falls back to
  plain TCP and logs at debug. Failing hard would take a node off the network for
  an optional optimisation.

Verification on the fork branch: `cargo check --all-targets` clean (zero
warnings), `cargo test --lib` **945 passed / 0 failed**, including all 72 TCP
transport tests unchanged, plus new config tests for default-off, round-trip, and
unknown-key rejection. No production PR to `jmcorgan/fips` is planned yet — the
aggregation measurement gate below must pass first.

## Measurement status

See [`MPTCP-MEASUREMENT.md`](MPTCP-MEASUREMENT.md) for the full lab record.

**Subflow formation: PROVEN.** On the GL-MT6000 (`tollgate-A04E`, OpenWrt 24.10.2)
with both ends configured `signal subflow`, a real second subflow forms and
completes its handshake — `MPJoinSynRx` and `MPJoinAckRx` go from 0 to 2 across a
single measured transfer, and `ss -Min` shows `subflows:1`, `local_addr_used:3`.
This is the counter that stayed at 0 in every earlier run.

**Aggregation: NOT achieved — topology-limited.** Measured on two disjoint L2
segments that both terminate at the *same* router:

| Config | Throughput |
|---|---|
| single path (`--bind-dev`) | 925–928 Mbit/s |
| unbound, MPTCP free to use both | 919–921 Mbit/s |

No gain (the difference is noise). Conntrack shows why: only one of the two MPTCP
connections carries data. Both subflows converge at one router with one upstream,
so the scheduler correctly keeps traffic on the better subflow instead of
splitting across a shared bottleneck. Independent *end-to-end* paths are required
for a real aggregate.

## Licensing

- This module: same licence as the repository.
- `bondingshouldbefree`'s OpenWrt stack is **GPL-2.0-only** and is
  **incompatible** with this repo's **GPL-3.0**. Invoke the packaged binary;
  never copy its source here.
- `xray-core` is MPL-2.0, `iproute2`/kernel MPTCP are upstream — both fine.

## Follow-up work

1. A thin **applier** that renders `ip`/`nft` commands from `Config` and is
   idempotent (the Ansible repo already encodes the correct command sequence).
2. Wire `ShouldSteer` into the existing `upstream_detector` events so a
   subflow is added when an interface comes up and removed when it goes down.
3. Portal-chain ordering: mark authenticated traffic before the bond table
   acts on it.
4. An e2e test that proves a guest session survives an uplink drop.
