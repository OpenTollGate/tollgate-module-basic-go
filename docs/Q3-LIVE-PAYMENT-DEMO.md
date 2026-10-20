# Q3 PROVEN: TollGate pays an upstream TollGate — live ecash, three-sided receipts

Date: 2026-10-20 · Lab topology: x280 (client) → dq05 (gateway + mint), plain LAN
Binaries: `tollgated` @ `tollgate-module-basic-go` main (b77f9d79), `mintd` gonuts v0.13.0

## Verdict

**Q3 = YES (lab).** A client TollGate's `upstream_session_manager` discovered a
gateway TollGate, read its kind-10021 advertisement, created a paid session by
sending a real Cashu token (58 sats, 58 steps × 1 sat) over `POST :2121/`, and
renewed automatically on expiry. The gateway received the token, redeemed it at
the mint (proofs swapped for signatures), and granted a 59,392 ms allotment.
Both sides ran unmodified production daemon code — only configs differ.

## Receipts (verbatim log lines)

Client (x280, `/tmp/q3lab/client2.log`):
```
✅ Session created and tracking started   gateway=192.168.2.32 metric=milliseconds
💸 Sending payment to upstream            amount=58 gateway=192.168.2.32 steps=58
✅ New session created                    new_allotment=59392
⚠️  Session expired                       previous_allotment=59392
💸 Sending payment to upstream            amount=58 steps=58          ← renewal, unattended
✅ New session created                    new_allotment=59392
```

Gateway (dq05, `/tmp/tollgated.log`):
```
Received handleRootPost request           remote_addr=192.168.2.28:43732
Treating request as plain Cashu token string
PurchaseSession: calling Receive for mint=http://192.168.2.32:4242 token_amount=58 mac=00:e0:4c:36:04:3e
ERROR: a PAID purchase could not be granted: … the value is in the operator's wallet
       and the grant is owed; it will be retried …                  ← NDS absent on x86 lab,
                                                                      owed-grant retry loop
```

Mint (dq05, `/tmp/q3mint/mintd.log`) — redemption side:
```
msg="returning signatures on swap request"  request.method=POST request.url=/v1/swap code=200   ×11
msg="proof already used"                    request.method=POST request.url=/v1/swap code=400   ← double-spend correctly refused
melt quote … /v1/melt/quote/bolt11 code=200                                                    ← wallet payout rails exercised
```

The `proof already used` rejection is itself evidence the proofs were real:
a second redemption attempt of the same secret was refused by the mint.

## How it was wired (the four undocumented traps)

1. **Partial config sections zero their defaults.** Omitting `probe_retry_count`
   from `upstream_detector` made it 0 — the probe loop never ran a single
   attempt. Every key of a section you touch must be written explicitly
   (normalized config is persisted; defaults do not backfill).
2. **Gateway discovery is default-route-driven.** `getGatewayForInterface` only
   accepts default routes (or infers .1/.254). Lab: dummy iface `q3up` +
   `ip route add default via <gw> dev q3up onlink metric 5000` — real traffic
   unaffected (metric 100 wins), detector reports the lab gateway.
   `only_interfaces: [q3up]` pins discovery to it.
3. **Client daemon wallet bootstrap.** The daemon's wallet lives in
   `/etc/tollgate`; it starts empty. Fund it by playing customer against its own
   money path (`POST` a token from a `nutw` wallet). That path attributes payers
   by MAC via ARP/DHCP — on the same host, add a static neighbor entry
   (`ip neigh replace <own-ip> lladdr <mac> dev <lan>`) so attribution resolves.
4. **Renewal works out of the box** — expiry → re-pay → new allotment, no code
   changes.

## Scope honestly stated

- **Transport:** plain HTTP over LAN. NOT yet over FIPS mesh or MPTCP bond —
  that's the Q1/Q4 integration (payment-gated steering), not Q3's claim.
- **Mint:** gonuts `LIGHTNING_BACKEND=FAKEBACKEND` — invoices settle instantly,
  so the tokens are real Cashu (signed proofs, spent-proof enforced) but backed
  by no Lightning liquidity. Correct for a demo; not a production payment.
- **Grant:** the gateway's owed-grant retry ("value is in the operator's wallet")
  loops because nodogsplash is absent on the x86 lab node. Payment accounting is
  complete; the *network enforcement* half is what Q4 builds.

## Teardown (EXECUTED 2026-10-20 — verified: ports 4242/2121 free, no tollgated/mintd processes, /etc/tollgate restored (prod mint coinos first), q3up link + onlink default + static ARP removed, dq05 /tmp cleaned; /tmp/q3lab on x280 keeps binaries+logs as receipts)

```
x280:  sudo ip route del default via 192.168.2.32 dev q3up
       sudo ip neigh del 192.168.2.28 dev enx00e04c36043e
       sudo ip link del q3up
       pkill -f q3lab/tollgated
       sudo rm -rf /etc/tollgate && sudo mv /etc/tollgate.q3bak /etc/tollgate
dq05:  sudo pkill -f /tmp/tollgated; pkill -f q3mint/mintd; rm -rf /tmp/q3mint /tmp/tollgated*
```
