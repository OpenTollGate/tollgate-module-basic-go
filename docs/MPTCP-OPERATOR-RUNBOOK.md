# MPTCP Bonding — Operator Runbook

How to enable and verify a multi-WAN bond on an OpenWrt TollGate router.

**Read this first:** a bond gives **resilient multi-path, not multiplied
bandwidth**. Expect **1.2–1.3×** in the best case, and only when the far end is
also multipath-capable. Two subflows that converge at the same upstream share
that upstream's bottleneck — see [MPTCP-MEASUREMENT.md](MPTCP-MEASUREMENT.md) for
the measured evidence. Do **not** market this as doubling a connection.

## Prerequisites

| Requirement | Why |
|---|---|
| Linux kernel with MPTCP (`net.mptcp.enabled=1`) | Without it there is no multipath at all. OpenWrt 24.10.2 ships it (verified on GL-MT6000, kernel 6.12.94). |
| `ip-full` | BusyBox `ip` has **no `mptcp` subcommand**. Every command below fails with a usage error on a stock image. |
| ≥2 local addresses the peer can reach | MPTCP needs distinct local addresses to form subflows. Two addresses on the *same* L2 is not enough — see "Real paths vs. fake paths". |
| The far end also speaks MPTCP | A plain TCP far end yields a single-path connection. |

Install the mandatory package:

```sh
opkg update && opkg install ip-full
```

> `mptcpize`, `mptcpd`, and `ncat` are also available from the stock OpenWrt
> feeds if you need them. `mptcpize` is only required to make a *normal* binary
> use MPTCP; a binary built against `IPPROTO_MPTCP` does not need it.

## The four rules that bite

These are the failures found in the lab, each of which is **silent** — the bond
looks configured and carries everything on one path.

### 1. `signal` or not depends on WHICH TRANSPORT SHAPE you chose

This is the rule that is easiest to get backwards, because both directions are
defensible and only one applies to your network. It is decided by
`Config.Transport` (see the architecture decision record's D10).

| `transport` | endpoint flags | who forms the 2nd subflow | needs a peer-reachable client address? |
|---|---|---|---|
| `advertised-address` (default) | `signal subflow` | the **peer** connects back to the advertised address | **YES** |
| `proxy-terminated` | `subflow` only | the **client** opens it over an uplink it already has | **NO** |

**`advertised-address`** — the historic shape. Without `signal` the kernel never
sends `ADD_ADDR`, so the peer never learns an address to dial, and no second
subflow can ever form:

```sh
# WRONG for this shape - looks configured, never advertises
ip mptcp endpoint add 192.168.2.15 dev lan1 subflow

# RIGHT for this shape
ip mptcp endpoint add 192.168.2.15 dev lan1 signal subflow
```

**`proxy-terminated`** — the NAT-safe shape. Here `signal` is actively harmful:
it asks the peer to connect back to an address it cannot reach, so the JOIN fails
and the bond stays single-path while every counter looks plausible:

```sh
# RIGHT for this shape - nothing is advertised, the client initiates
ip mptcp endpoint add 192.168.2.15 dev lan1 subflow
```

Symptom of getting it wrong in the `advertised-address` direction:
`MPTcpExtMPJoinSynRx` stays at **0** forever while everything else looks fine.

Symptom of getting it wrong in the `proxy-terminated` direction (i.e. still
signalling behind NAT): the peer's `MPTcpExtAddAddr` stays **0** — it never
receives an advertisement, because the client's own path manager has no reachable
address to offer — while `MPJoinNoTokenFound` climbs and the subflow sits at
handshake size (~1.7 KB) indefinitely. **Measured on this bench** with both
uplinks private (house NAT + Starlink CGNAT).

> Which counters move also depends on the shape, so pick the right ones:
> with `signal` the **peer** joins → watch the *responder* counters
> (`MPJoinSynRx` / `MPJoinAckRx`). With `subflow` only the **client** joins →
> watch the *initiator* counters (`MPJoinSynTx` / `MPJoinSynAckRx`). Watching the
> wrong side reads as "no subflow" on a working bond.

### 2. Raise the subflow ceiling above the kernel default

```sh
ip mptcp limits set subflow 4 add_addr_accepted 4
```

The kernel default is **2** subflows. A multi-WAN box with three uplinks will
register the third and never use it. `add_addr_accepted` must cover the peer's
address count or the peer's `ADD_ADDR` is refused.

**Re-assert the limits after any endpoint flush.** `ip mptcp endpoint flush`
silently resets `add_addr_accepted` to **0** (measured `4 → 0`), after which the
router refuses the peer's `ADD_ADDR` and peer-initiated subflows break with no
error anywhere.

### 3. Two addresses on one L2 is not two paths

MPTCP forms a subflow only between **disjoint** paths. If both local addresses
sit on the same segment and gateway, the kernel correctly refuses a second
subflow — there is nothing to gain.

```sh
ip route get <peer-address>     # every address must NOT resolve to one interface
```

**Real paths vs. fake paths** — measured in the lab:

| Setup | Subflow forms? | Aggregates? |
|---|---|---|
| Two addresses, one L2, one gateway | No | No |
| Two disjoint L2s, **one upstream** | **Yes** | **No** — converges at the shared upstream |
| Two disjoint L2s, **two upstreams** | Yes | Yes (1.2–1.3×) |

### 4. Binding a socket to an uplink's address does NOT route it over that uplink

This is the one that makes "two uplinks" behave like one, and it is invisible
unless you look at the route lookup rather than the interface list.

```sh
# both interfaces up, both with addresses - and yet:
ip route get 23.182.128.51 from 192.168.2.15
#   23.182.128.51 via 10.2.0.1 dev phy0-sta0 src 10.2.1.122    <-- the OTHER uplink
```

The source address does **not** influence route selection. A socket bound to the
wired address still egresses whichever uplink owns the default route, carrying a
source address that does not belong to that interface — and the packets die.
**With two interfaces up there is still only one usable path**, no matter how the
endpoints are configured.

Per-uplink policy routing is therefore mandatory, and it is the same mechanism
the module installs (a rule + table per uplink):

```sh
ip rule add from 192.168.2.15 lookup 201 priority 100
ip route replace default via 192.168.2.1 dev lan1 table 201
ip rule add from 10.2.1.122 lookup 202 priority 101
ip route replace default via 10.2.0.1 dev phy0-sta0 table 202
```

Verify by reading the lookup back, not the interface list:

```sh
ip route get <peer> from 192.168.2.15   # must now say dev lan1
ip route get <peer> from 10.2.1.122     # must now say dev phy0-sta0
```


## Apply

Render the script from the bond config and run it. It is **idempotent** — safe on
every boot and every interface change, because each endpoint is preceded by a
delete of the same address.

```sh
sh /etc/tollgate/mptcp-bond.sh
```

It emits nothing at all when the bond is disabled, checks its own prerequisites
(`ip`, `net.mptcp.enabled`, the `mptcp` subcommand) and fails with a clear
message rather than half-applying.

The bind password is deliberately **not** rendered into the script — it belongs
in the transport's own configuration, not a generated file at a predictable path.

## Verify

```sh
ip mptcp limits                  # ceilings must cover your uplink count
ip mptcp endpoint show           # flags must MATCH your transport shape (rule 1)
cat /proc/sys/net/mptcp/enabled  # must be 1
```

The endpoint listing tells you which shape is actually live, so read it against
rule 1 rather than assuming:

```sh
# advertised-address shape -> every line carries `signal`
192.168.2.15 id 1 dev lan1 signal subflow
10.2.1.122   id 2 dev phy0-sta0 signal subflow

# proxy-terminated shape -> no line carries `signal`
192.168.2.15 id 1 dev lan1 subflow
10.2.1.122   id 2 dev phy0-sta0 subflow
```

Then confirm the paths are genuinely separate:

```sh
ip rule show                     # a from-address rule per uplink (rule 4)
ip route get <peer> from <uplink-addr>   # must resolve to THAT uplink's dev
```

### Reading the counters: which side you watch depends on the shape

| shape | who issues the MP_JOIN | counters that must advance |
|---|---|---|
| `advertised-address` (has `signal`) | the **peer** | *responder* side: `MPJoinSynRx`, `MPJoinAckRx` |
| `proxy-terminated` (no `signal`) | the **client** | *initiator* side: `MPJoinSynTx`, `MPJoinSynAckRx` |

Watching the wrong side reads as "no subflow" on a bond that is working. And
before trusting any of it, prove the connection is MPTCP at all rather than a
silent fallback — a plain-TCP peer listener makes MPTCP fall back, and a fallback
connection has **no path manager**, so nothing downstream can ever work:

```sh
# on the PEER: the listener must be MPTCP, not plain TCP
ss -Mltn | grep <port>        # expect: mptcp LISTEN ... *:<port>
```


Then, during a transfer, these counters must **move**:

```sh
# MPJoinSynRx - subflow JOINs received  (0 = no subflow ever formed)
# MPJoinAckRx - handshakes completed
grep MPTcpExt /proc/net/netstat | tail -1
```

The counter line is positional; the fields you want are `MPJoinSynRx` and
`MPJoinAckRx`. To read them by name, use `read_mptcp_counter.sh` (shipped
alongside this runbook) — a plain `awk` is possible but **`paste` is not in
BusyBox**, so the obvious one-liner fails on OpenWrt:

```sh
# this works:
sh /etc/tollgate/read_mptcp_counter.sh MPJoinSynRx
# this does NOT (paste is missing from BusyBox):
#   awk '/MPTcpExt:/{print}' /proc/net/netstat | tr ' ' '\n' | paste - - | grep MPJoin
```

> **`ss -Min` is not reliable on OpenWrt.** On the lab router the binary exists
> but prints nothing for MPTCP sockets. Use the `/proc/net/netstat` counters
> above — they are the authority. (`ss -Min` works on a full Linux host like
> Debian/Ubuntu, where it is the easiest per-socket view.)

**Reading the verdict:**

- `MPJoinSynRx > 0` → a real second subflow exists and completed its handshake
  (**advertised-address shape**). For the **proxy-terminated** shape read
  `MPJoinSynTx > 0` on the client instead — the peer never sees a JOIN there.
- Subflow exists but throughput is unchanged → the paths converge upstream
  (shared bottleneck), or one path is simply faster and the default scheduler is
  correctly preferring it. This is the topology, not a misconfiguration.
- `MPJoinSynRx == 0` while endpoints look right → either you omitted `signal` on
  an **advertised-address** bond (rule 1), both addresses share one L2 (rule 3),
  or the peer cannot reach your advertised address because it is NAT-ed/CGNAT-ed —
  in which case switch to the proxy-terminated shape rather than adding `signal`
  to a private address.
- Nothing moves at all → check the peer's listener is MPTCP (`ss -Mltn`). A
  plain-TCP listener causes a silent fallback, and a fallback connection has no
  path manager at all.


### Checking for disjoint paths (rule 3)

Run this **on the client, against the peer's addresses** — not on the router
against its own addresses, which returns `local` and tells you nothing:

```sh
# ON THE CLIENT, for each of the PEER's addresses:
ip route get <peer-address-1>
ip route get <peer-address-2>
```

If both resolve to the same `dev` and the same next hop, the paths are not
disjoint and no second subflow can form.

## Wireless uplinks on MT7986

Radios are shared between AP and STA. Change Wi-Fi configuration with `uci` and
`wifi reload` (or `wifi down; wifi up`). **Never** use `iw` interface surgery or
`killall wpa_supplicant` — that tears down the access points and can lock you out
of a field device.

### wpad fails with `bind(PF_UNIX): Permission denied`

On OpenWrt, `wpad` runs inside `ujail` and drops to the `network` user. The
init script creates and chowns `/var/run/hostapd` but **not**
`/var/run/wpa_supplicant`, so a root-owned control-socket directory makes the
daemon fail to start and no STA interface ever appears:

```sh
mkdir -p /var/run/wpa_supplicant
chown network:network /var/run/wpa_supplicant
chmod 770 /var/run/wpa_supplicant
wifi reload
# expect: wpa_supplicant: Successfully initialized wpa_supplicant
```

**This is not persistent across reboot** — `/var/run` is tmpfs. Re-apply it from
an init script if you rely on a wireless uplink.

## Related

- [`MPTCP-BONDING.md`](MPTCP-BONDING.md) — the module design and the FIPS
  transport patch it depends on.
- [`MPTCP-MEASUREMENT.md`](MPTCP-MEASUREMENT.md) — the lab record: what was
  proven, what was not, and the conntrack evidence for the topology limit.
- [`wireless_gateway_manager.md`](wireless_gateway_manager.md) — the existing
  module that manages upstream STA connectivity; a wireless uplink that will
  carry a subflow is provisioned there.
