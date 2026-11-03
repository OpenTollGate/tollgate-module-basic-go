# CORRECTION: `signal` is NOT required — the client can originate subflows

Date: 2026-10-19
Corrects a claim this project has repeated, including in
`MPTCP-WHY-NO-MULTIPLIER.md` (cause 1) and in the standing lab-notes line
*"a subflow-only endpoint is USELESS: without signal the kernel never sends
ADD_ADDR, so the peer never learns the address. Must be signal subflow."*

## What was measured

Endpoints on the two **reachable tunnel addresses**, registered `subflow` only
(no `signal`), client wrapped in `mptcpize`:

```
[SF_ESTABLISHED] token=6780f7fc locid=2 saddr4=10.99.97.2 daddr4=<peer> ifindex=39
[SF_ESTABLISHED] token=6780f7fc locid=1 saddr4=10.99.98.2 daddr4=<peer> ifindex=38

AddAddrTx   11 -> 11    (did NOT advance)
MPJoinSynRx 60 -> 63    (+3)
MPJoinAckRx 32 -> 33    (+1)
```

**Two subflows on one token, with zero address advertisement.** `AddAddrTx` stayed
flat, so nothing was advertised to the peer — the router **originated both
subflows itself**, using its own two tunnel addresses.

## Where the old claim came from, and why it was wrong

The old rule was derived from a real failure: with `signal` and *private* uplink
addresses, the router advertised an address the peer could not dial, so
`MPJoinSynRx` stayed 0 while `AddAddrTx` climbed. That observation is correct.

The inference — "therefore `signal` is mandatory" — was wrong. The failure was
**peer-unreachability**, not the absence of `signal`. Two different things were
conflated:

| topology | what must create the subflow | `signal` needed? |
|---|---|---|
| client's addresses are **reachable** by the peer (tunnel, public, routable) | the **client** can originate them at connect | **No** |
| client is behind **NAT/CGNAT**, addresses private | only the **peer** could create them, and it cannot dial back | needs an address the peer can reach — a tunnel |

So `signal` matters when you are relying on the **peer** to create paths. When the
client itself has routable addresses, plain `subflow` is sufficient and simpler.

## Corrected rule

> Register the endpoints on addresses that are **reachable by the peer**, and the
> bond forms. `signal` is only required when the peer must learn and dial the
> address itself. Reachability, not the flag, is the governing requirement.

This matters practically: `subflow`-only is the **safer default** — it advertises
nothing, so it leaks no addresses and cannot trigger the NAT failure mode. `signal`
should be opted into, not required by default.

## Effect on the design (D10)

D10 (proxy-terminated transport) is **unchanged and still correct** — the client
must not need a routable address in the general case. What changes is the
mechanism: the bond can be formed by the client originating subflows over its own
per-uplink tunnels (proven here), rather than by relying on address advertisement
that CGNAT defeats.

## Earlier documents

`MPTCP-WHY-NO-MULTIPLIER.md` cause 1 overstated this. The accurate statement is:
*without reachable addresses no subflow forms; `signal` alone does not fix that,
and with reachable addresses `signal` is unnecessary.*
