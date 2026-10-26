# Aggregation: the `signal` run, and the measurement flaw that invalidated the earlier numbers

Date: 2026-10-19
Status: `signal subflow` endpoints tested on the device. Second subflow still not
formed. **AND a methodological error in the previous measurement is corrected
here** — the earlier "bonded" numbers were plain TCP, not MPTCP.

## THE ERROR (corrected)

The previous result (`MPTCP-AGGREGATION-RESULT.md`) ran:

```
iperf3 -c <peer> -p 5201 -t 6                  <-- NO mptcpize
```

`iperf3` on its own is a plain-TCP client. The tell was in the transfer output,
which I did not read carefully enough at the time:

```
[  5] local 192.168.2.20 port 57166 connected to 23.182.128.51
                 ^^^^^^^^^^^^ this is the LAN address, NOT an uplink address
```

A genuine MPTCP connection from this router shows an **uplink** address
(`192.168.2.15` or `10.2.1.122`). `192.168.2.20` is the router's br-lan-side
address, i.e. an ordinary TCP connection leaving via whatever the default route
was.

**Consequence:** the earlier per-configuration numbers (wired 10.5/10.7,
StarGate 4.42/3.41, "bonded" 3.75/3.07 Mbit/s) measured **plain TCP**, and the
conclusion drawn from them — "bonded slower than either single path, no
multiplier" — was a statement about the default routing table, not about MPTCP.
It must not be cited as an MPTCP aggregation result.

This is the same class of mistake the project's own notes warn about (Rule 3.5),
applied in the opposite direction: there the **peer** listener was plain TCP; here
the **client** was. The check is symmetric and must be made at both ends —
`ss -Mltn` on the peer AND `mptcpize run` on the client, with the local address in
the iperf output read as the proof.

## The `signal` run (correctly wrapped in `mptcpize`)

Endpoints set to the advertised-address shape, read back from the device:

```
192.168.2.15 id 1 signal subflow dev lan1
10.2.1.122   id 2 signal subflow dev phy0-sta0
add_addr_accepted 4 subflows 4
```

Then `mptcpize run iperf3 -c <peer> -t 6`, and the counters **did** move — for
the first time in this investigation:

```
                        before   after
MPCapableSYNTX             12  ->  14     MPTCP handshake used
MPCapableSYNACKRX          12  ->  14     peer answered MPTCP
AddAddr                     0  ->   2     the router SENT an address advertisement
AddAddrTx                   0  ->   2
EchoAdd                     0  ->   2
MPJoinSynRx                 0  ->   0     <-- still no subflow received
```

So the `signal` flag **does** produce `ADD_ADDR`, and the connection is genuinely
MPTCP. But no second subflow forms. The remaining explanation is the one already
documented: the peer must connect **back** to the advertised address, and both
uplink addresses are private (house NAT `192.168.2.15`, Starlink CGNAT
`10.2.1.122`), so that JOIN cannot succeed.

This is exactly the D10 conclusion, now demonstrated with the client correctly
wrapped: **the advertisement is sent and is useless, because the address is not
reachable from the peer.** It also confirms the tunnel or proxy-terminated design
is the only route forward — not a configuration tweak.

Honest caveat: the peer could not be inspected in this iteration (the fleet SSH
path to `23.182.128.51` was down: `Connection timed out`), so
`MPJoinNoTokenFound` on the peer and the reachability probe of the two advertised
addresses from the peer were not re-read. The router-side counters are
unambiguous on their own — `AddAddrTx` rose and `MPJoinSynRx` stayed 0 — but the
peer-side confirmation is outstanding and should be the first check next time.

## Throughput with `mptcpize` (single connection, 6 s)

```
sender   14.5 MBytes  20.3 Mbits/sec   Retr 323
receiver 11.4 MBytes  15.4 Mbits/sec
```

One run, one configuration (default path). **Not a comparison** — no single-path
baselines were taken under `mptcpize` in this iteration, so no multiplier is
claimed. Note the 323 retransmits, which is high and worth understanding before
any comparison is trusted.

## What this changes

1. **The earlier "no multiplier" result is retracted** as an MPTCP measurement.
   It measured plain TCP. The honest statement is: no valid MPTCP aggregation
   comparison has yet been performed on this hardware.
2. **`signal` is proven to advertise** (`AddAddrTx 0 -> 2`) — so the endpoint
   configuration is not the blocker, and `signal subflow` is the correct shape
   whenever the advertised address is genuinely reachable.
3. **The subflow still does not form with private addresses**, which is the
   measured basis for D10 and for the tunnel/overlay design.
4. **Next measurement must** (a) wrap the client in `mptcpize`, (b) read the local
   address in the iperf output as the MPTCP proof, (c) take single-path baselines
   the same way, and (d) read the peer's counters to see the JOIN attempt fail.
