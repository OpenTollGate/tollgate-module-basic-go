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
