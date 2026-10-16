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

## The three rules that bite

These are the failures found in the lab, each of which is **silent** — the bond
looks configured and carries everything on one path.

### 1. An endpoint must be `signal subflow`, not `subflow`

```sh
# WRONG - looks configured, never advertises, no subflow can ever form
ip mptcp endpoint add 192.168.2.15 dev lan1 subflow

# RIGHT
ip mptcp endpoint add 192.168.2.15 dev lan1 signal subflow
```

Without `signal` the kernel never sends `ADD_ADDR`, so the peer never learns the
address. Symptom: `MPTcpExtMPJoinSynRx` stays at **0** forever while everything
else looks fine.

### 2. Raise the subflow ceiling above the kernel default

```sh
ip mptcp limits set subflow 4 add_addr_accepted 4
```

The kernel default is **2** subflows. A multi-WAN box with three uplinks will
register the third and never use it. `add_addr_accepted` must cover the peer's
address count or the peer's `ADD_ADDR` is refused.

### 3. Two addresses on one L2 is not two paths

MPTCP forms a subflow only between **disjoint** paths. If both local addresses
sit on the same segment and gateway, the kernel correctly refuses a second
subflow — there is nothing to gain.

```
ip route get <peer-address>     # every address must NOT resolve to one interface
```

**Real paths vs. fake paths** — measured in the lab:

| Setup | Subflow forms? | Aggregates? |
|---|---|---|
| Two addresses, one L2, one gateway | No | No |
| Two disjoint L2s, **one upstream** | **Yes** | **No** — converges at the shared upstream |
| Two disjoint L2s, **two upstreams** | Yes | Yes (1.2–1.3×) |

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
ip mptcp endpoint show           # every uplink address, marked `signal subflow`
cat /proc/sys/net/mptcp/enabled  # must be 1
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

- `MPJoinSynRx > 0` → a real second subflow exists and completed its handshake.
- Subflow exists but throughput is unchanged → the paths converge upstream
  (shared bottleneck). This is the topology, not a misconfiguration.
- `MPJoinSynRx == 0` while endpoints look right → you almost certainly omitted
  `signal` (rule 1), or both addresses share one L2 (rule 3).

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
