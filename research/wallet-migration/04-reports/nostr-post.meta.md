# Nostr long-form post — event metadata

The publishable body is [`nostr-post.md`](nostr-post.md). This file records the
NIP-23 (`kind: 30023`) envelope, the publish command, and the publish result.

## Event

- **kind:** `30023` (NIP-23 long-form content)
- **d:** `tollgate-wallet-migration-2026-09`
- **title:** Choosing a Cashu wallet for an OpenWrt router: gonuts vs CDK vs nucula
- **summary:** What a feasibility programme learned about replacing a forked,
  dead-upstream Cashu wallet on flash-constrained OpenWrt routers — and the honest
  trade-offs of staying vs switching.
- **tags:** `t=cashu`, `t=nostr`, `t=openwrt`, `t=tollgate`, `t=bitcoin`
- **relays:** `wss://relay.damus.io`, `wss://nos.lol`, `wss://nostr.mom`,
  `wss://relay1.orangesync.tech`, `wss://relay2.orangesync.tech`
- **signer:** operator NIP-46 bunker (not stored here)

## Publish command

```bash
# NOSTR_SECRET_KEY holds the operator's bunker:// URL (mode-0600, never committed)
nak event -k 30023 \
  -d tollgate-wallet-migration-2026-09 \
  -t "title=Choosing a Cashu wallet for an OpenWrt router: gonuts vs CDK vs nucula" \
  -t "summary=What a feasibility programme learned about replacing a forked, dead-upstream Cashu wallet on flash-constrained OpenWrt routers." \
  -t "t=cashu" -t "t=nostr" -t "t=openwrt" -t "t=tollgate" -t "t=bitcoin" \
  --sec "$NOSTR_SECRET_KEY" \
  -c @04-reports/nostr-post.md \
  wss://relay.damus.io wss://nos.lol wss://nostr.mom \
  wss://relay1.orangesync.tech wss://relay2.orangesync.tech
```

## Published

**Pending — 2026-09-16.** The NIP-46 bunker accepts nak's connect (all four signer
relays are reachable; nak logs the client-initiated connect with a random
`--connect-as` key) but the **remote signer returned no signature within 150 s**,
across three attempts (sign-only and with relays). Most likely the signer app
(nsec.app) is not online / has not approved the client for this `secret`.

Source is ready: the command above publishes the exact body once the signer
responds. Re-run it, then replace this section with the returned event `id` and the
per-relay acceptance list. The published event is addressable by `d`, so re-publishing
supersedes any partial attempt.
