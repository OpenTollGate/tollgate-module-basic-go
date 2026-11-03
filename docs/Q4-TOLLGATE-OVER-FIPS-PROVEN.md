# Q4 PROVEN: TollGate pays a gateway TollGate OVER THE FIPS MESH

Date: 2026-10-20 · Topology: x280 (client) —fips0— dq05 (gateway + mint)
Binaries: `tollgated` @ `c8f43b20` (both v6 fixes) · `mintd` gonuts v0.13.0 (FakeBackend)

## Verdict

**Q4 = YES (lab).** A client TollGate's `upstream_session_manager` paid a gateway
TollGate addressed by its **FIPS-mesh IPv6 ULA**, through the `fips0` overlay,
with a real Cashu token redeemed at the mesh-hosted mint. Session granted,
renewals flowing. Two production bugs had to be fixed to make it possible — both
landed with TDD evidence.

## Receipts (verbatim, 2026-10-20 23:39)

Client (x280, `/tmp/q3lab/client-final.log`):
```
💸 Sending payment to upstream   amount=58 gateway="fdb0:bf34:d648:96ac:f65a:3fd6:358:710b"
✅ New session created           gateway="fdb0:…:710b" new_allotment=59392
   (21 payments total; auto-recovery returned tokens on refused attempts)
```

Gateway (dq05, `/tmp/tollgated.log`):
```
INFO Received handleRootPost    remote_addr="[fd36:3c0f:d6f:8029:ea50:958b:672e:bb0]:43628"
     ^ client's fips0 mesh address — the payment traversed the overlay
PurchaseSession: calling Receive for mint=http://[fdb0:…:710b]:4242 token_amount=58 mac=02:00:00:00:00:01
PurchaseSession: Receive completed, amount=58, err=<nil>
```

Mint (dq05, `/tmp/q3mint/mintd.log`): **49 successful `/v1/swap`** redemptions.

## The two blockers that had to be fixed first (both TDD, both pushed)

1. **`88057bdf` — IPv6 URL bracketing.** All five payment-chain URL sites built
   `http://%s:2121/` raw; a mesh ULA produced an invalid URL. `gatewayURL()` /
   `bracketHost()` now bracket v6 literals. Integration test: real `[::1]:2121`
   listener receives the token POST and the kind-1022 allotment parses.
2. **`c8f43b20` — IPv6 MAC attribution.** `/proc/net/arp` is IPv4-only; every
   IPv6 payer was refused `device-unresolved`. Third lookup source:
   `ip -6 neigh show` behind a testable seam.

## Lab notes (what the live run additionally required)

- **Port admission per FIPS-SERVICE-PORT-ADMISSION.md**: gateway node needed
  `tcp dport 2121 accept` + `tcp dport 4242 accept` in `/etc/fips/fips.d/`
  before the mesh carried either service (both verified 200 through fips0).
- **Mint advertised by mesh FD**: `accepted_mints.url = http://[fdb0:…]:4242`
  in the gateway config — the advertisement carries the bracketed v6 URL.
- **Client-MAC attribution on a TUN**: the kernel neighbor table mangles static
  entries on `fips0` (`lladdr 08`), so attribution was fed via the **primary**
  source instead: a dnsmasq-format lease line in `/tmp/dhcp.leases` mapping the
  client's mesh FD → MAC. (Production note: FIPS-TUN peers need either real
  DHCP/lease plumbing on the gateway or an identity source that understands
  overlay addresses — the overlay has no ARP.)
- **Discovery**: dummy iface `q3fips` + `ip -6 route add default via <mesh-FD>
  dev q3fips onlink metric 5000` + `only_interfaces: [q3fips]`.
- **Deploy trap**: `pkill -f /tmp/tollgated` matches its own ssh command string
  and kills the deploying shell before the `cp` runs — use `pkill -x tollgated`.

## Scope honestly stated

- Same lab standard as Q3: FakeBackend mint (real Cashu proofs, no Lightning
  backing), NDS enforcement absent on x86 (grant owed, retry loop) — the
  network-enforcement half remains future work with `ShouldSteer` wiring.
- Payment traffic rode the FIPS **overlay** (fips0, IPv6 ULA, port admission
  enforced) — not yet over an MPTCP **bond** (that combination is Q1's
  disjoint-uplink measurement, still pending hardware).

## Teardown (after evidence capture)

```
x280:  pkill -x tollgated-v6; sudo ip -6 route del default via fdb0:…:710b dev q3fips
       sudo ip link del q3fips; sudo ip neigh del 192.168.2.28 dev enx00e04c36043e
       sudo rm -rf /etc/tollgate && sudo mv /etc/tollgate.q3bak2 /etc/tollgate
dq05:  sudo pkill -x tollgated; pkill -x mintd; sudo rm -rf /tmp/q3mint /tmp/tollgated*
       sudo rm /etc/fips/fips.d/q3lab.nft && sudo systemctl reload fips-firewall
       sudo rm /tmp/dhcp.leases (lease line added for attribution)
```
