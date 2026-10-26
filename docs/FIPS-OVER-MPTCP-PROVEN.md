# FIPS over MPTCP: a session with TWO subflows — the transport-level proof

Date: 2026-10-20
Two x86 nodes running the **patched** FIPS (`pr/tcp-mptcp-option` @ `5e150bdf`),
both with `transports.tcp.mptcp: true`. This is the measurement the whole Q1
question rested on.

## The proof

On node A (`x280`):

```
tcp   ESTAB  192.168.2.28:17501  <-  192.168.2.38:41228     (peer's enp1s0 — wired)
tcp   ESTAB  192.168.2.28:17501  <-  192.168.2.32:43359     (peer's wlp2s0 — wireless)
mptcp ESTAB  192.168.2.28:17501  <-  192.168.2.38:41228     (ONE MPTCP socket)

mptcp LISTEN 0.0.0.0:17501
```

Two TCP legs from the peer's **two different interfaces** into the **same MPTCP
socket**, and the listener itself shows as `mptcp LISTEN`. The FIPS log confirms
the peering: `Peer promoted to active peer=nodea`.

Local addresses registered as MPTCP endpoints on each node:

```
A: 192.168.2.13 (enx6c6e, wired)  192.168.2.28 (enx00e0, wired)
B: 192.168.2.38 (enp1s0, wired)   192.168.2.32 (wlp2s0, wireless)
```

## Why this matters: it closes the responder-side hole

`36f82c86` made outbound connects MPTCP but left the listener plain TCP. A plain
listener answers the MP_CAPABLE SYN with a plain SYN-ACK, the connection falls
back to single-path, and a fallback connection has **no path manager at all** —
so the initiator's MPTCP socket buys nothing. That was the exact failure that
invalidated an earlier bench measurement (plain-TCP peer listener →
`MPCapableSYNACKRX` 0, no `ADD_ADDR`).

`5e150bdf` fixed the listener (`bind_listener` builds the socket with
`Protocol::MPTCP`). The `mptcp LISTEN` line above is that fix working: **without
it this session would have fallen back and shown a single plain `tcp ESTAB`.**

## What was involved (all real, all encountered)

- **ACL.** The fleet's `peers.allow` + `peers.deny` (deny = `ALL`) rejected the
  test npubs: `WARN Rejected peer by ACL ... decision=denylist match`. Allow is
  evaluated first, so appending both test npubs to `peers.allow` on both nodes
  was sufficient and non-destructive (original files backed up to
  `/tmp/peers.allow.bak` on each).
- **Config keys worth recording** (the docs' example config does not exercise
  these): `node.control.socket_path` (NOT `control_socket` — the wrong name
  silently leaves the default `/run/fips/control.sock` and collides with a
  running fleet daemon), `node.identity.key_file`, and `dns.enabled: false` to
  avoid the `[::1]:5354` collision.
- **Isolated test nodes, not the fleet daemons.** Each ran from its own
  `data_dir`, key file, control socket and port (17501), so the live mesh was
  never disturbed.

## Honest limits — what this does NOT show

- **No throughput measured.** This proves the *topology* (two subflows, one
  connection). It does NOT show aggregation, and no speedup is claimed. Given
  earlier findings, the expectation is resilience rather than N-times bandwidth.
- **No mid-transfer failover test** on this FIPS pair.
- **Endpoints were `signal subflow` on directly-reachable LAN addresses.** The
  production shape (tunnels for NAT reversal, per Q2) was not used here.
- **TollGate is not involved at all** in this run — Q3 and Q4 are untouched.

## Reproduce

```sh
# on each node
ip mptcp endpoint flush
ip mptcp limits set subflows 4 add_addr_accepted 4
ip mptcp endpoint add <addr> dev <iface> signal subflow      # x2 per node
# config: transports.tcp.mptcp: true, distinct key_file/data_dir/socket path
# allow both npubs in /etc/fips/peers.allow (deny list contains ALL)
fips --config <test.yaml>
# then, to confirm two subflows on one connection:
ss -Mtan | grep <port>
```

Expect one `mptcp ESTAB` plus two `tcp ESTAB` rows sharing its local port.