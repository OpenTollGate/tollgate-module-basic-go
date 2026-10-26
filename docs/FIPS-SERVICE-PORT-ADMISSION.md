# FIPS service-port admission: the source-side dispatch rule (and why 83 Mbit/s needs it)

Date: 2026-10-20
Follows `FIPS-GOODPUT-LADDER-83MBIT.md`. This completes the port mystery with the
mechanism, not just the observation.

## The measured matrix (all from x280, `iperf3`/python TCP through fips0)

| destination | port | result | verdict |
|---|---|---|---|
| x240 (direct peer) | 49500 (dest-allowed) | **82.9 Mbit/s** | delivered to kernel |
| t440 (direct peer) | 80 (dest-allowed) | **80.0 Mbit/s** | delivered to kernel |
| VPS hermes-nvme (multi-hop) | 22 | SSH handshake OK, 211 ms | delivered |
| VPS hermes-nvme | 8200/3001/53 (allowlisted, nothing listening) | **RST in 0.2 s** | answered locally by the FIPS daemon |
| VPS hermes-nvme | 49500 (+ nft accept added on VPS) | timeout, ZERO packets egress fips0 | **blackholed at the SOURCE node** |
| VPS hermes-nvme | 49501/49502 | timeout, blackholed | same |

## The mechanism (three layers, all now verified)

1. **`table inet fips` (nft, inbound on each node)** — per-node allowlist, provisioned
   by fleet Ansible (role 03-fips-mesh), NOT by the FIPS daemon. Kills anything not
   allowlisted at the destination's kernel. This is why x240:43210 failed (its table
   allows 9099, not 43210) while x240:49500-in-our-table worked when the roles were
   reversed.
2. **Source-side service dispatch (FIPS daemon)** — msg_type 0x10 port-multiplexed
   service dispatch (`src/node/handlers/session*`). The daemon holds a registry of
   *services it can route to*. For ports it recognizes (8443/3001/8200/53/22 — its
   own native services) it **answers RST locally in ~0.2 s with zero packets ever
   reaching fips0** (verified: tcpdump shows nothing while the RST arrives).
3. **Unregistered ports to a destination that isn't advertising them are silently
   dropped at the source** — no RST, no ICMP, no egress. This is the blackhole that
   consumed two sessions of debugging.

**Practical rule:** to run a NEW service over FIPS between two nodes, ALL THREE must
hold: the destination's `table inet fips` allowlist must admit the port; the
destination must actually listen; and the source's FIPS must put the flow on the
mesh — which it does once a session to the destination exists AND the flow isn't
swallowed by service dispatch. The reliable way to test any node pair is to use a
port allowlisted on the destination, verified with `sudo nft list table inet fips`
on THAT node.

## What this means for the stack

- **Q1 (FIPS over bond):** the overlay carries 80+ Mbit/s — no throughput obstacle.
  But the bonding server's MPTCP port must be **allowlisted on every node that will
  reach it** and registered as a service, or flows will blackhole silently. This is
  an Ansible change (role 03-fips-mesh), one line per service port.
- **Q4 (TollGate on FIPS):** same rule — the TollGate portal/API ports must be in
  each node's allowlist. The `tollgate-suite-wrt` design's `fips-exit` instance will
  need its ports enumerated here.
- **Silent-drop behavior is a footgun worth an upstream issue**: an unregistered
  port blackholes with no ICMPv6 unreachable, so applications see pure timeout and
  operators waste hours (two sessions here). An ICMP unreachable (or a config knob
  for default-deny vs default-refuse) would make this diagnosable. Candidate
  upstream issue for `jmcorgan/fips` — NOT a code PR (operator instruction).

## Reproduce

```sh
# on DEST: pick a port in ITS allowlist: sudo nft list table inet fips
iperf3 -s -B <dest-fd00-addr> -p <allowed-port>
# on SRC:
iperf3 -6 -c <dest-fd00-addr> -B <src-fd00-addr> -t 15 -p <allowed-port>
```

Verified results: x240→x280 p49500 = 82.9 Mbit/s; x280→t440 p80 = 80.0 Mbit/s;
x280→VPS p22 SSH = working, 211 ms RTT multi-hop.