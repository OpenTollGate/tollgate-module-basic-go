# nosigner setup — reliable signing for the wallet-migration post (P2)

**Status:** Phase-1 validated 2026-09-16/17 on the laptop with an **ephemeral**
identity. This document is the operator's recipe to sign the NIP-23 post with the
**Amber identity on a different machine** using your own `nosigner` daemon,
instead of the flaky remote bunker (which returned `already connected`).

## What works (verified)

| Path | Result |
|---|---|
| **One-shot `nosigner sign` + `nak` publish** | ✅ **PASS** — signed a kind-30023 test event, `nak verify` exit 0, published to nos.lol / nostr.mom / relay.primal.net / relay2.orangesync.tech, fetched back id `ea0f1534956a8fd65dedea820269e37aeb2e72c0cbe5be3461ddfc02dcd1c67d` (ephemeral npub `npub1g7s0qe…`) |
| `nosigner daemon` (NIP-46) | ⚠️ Server side works (decrypts nak's NIP-44 requests, signs, publishes responses) but **`nak` as client ends with `context canceled`** — an interop gap; also `connect` has a key bug (see below). **Not recommended for `nak` yet.** |

So: **use the one-shot path.** It has no relay round-trip, no NIP-46, no timing.

## Prerequisites

```bash
# signer runtime
python3 -m venv ~/.nosigner-venv
~/.nosigner-venv/bin/pip install 'nostr-sdk==0.44.2'    # ⚠ pin: 0.45.x removed HandleNotification
# client/publisher (for the one-shot path you only need nak to publish)
go install github.com/fiatjaf/nak@latest   # or grab a release binary
```

⚠️ **Pin `nostr-sdk==0.44.2`.** Newer 0.45.x renamed/removed
`nostr_sdk.HandleNotification`, which `nosigner.py` subclasses at import time
(`AttributeError: module 'nostr_sdk' has no attribute 'HandleNotification'`).

Copy `nosigner.py` (from this laptop: `~/.hermes/scripts/nosigner.py`) to the
other machine, e.g. `~/nosigner.py`.

## Import the Amber identity

`nosigner` reads its key from `$HERMES_HOME/state/bunker/keys.json`
(`HERMES_HOME` defaults to `~/.hermes`). Create it with Amber's secret:

```bash
export HERMES_HOME="$HOME/.nosigner"
mkdir -p "$HERMES_HOME/state/bunker"
umask 077
cat > "$HERMES_HOME/state/bunker/keys.json" <<EOF
{"secret_key": "<AMBER_NSEC_OR_HEX>"}
EOF
chmod 600 "$HERMES_HOME/state/bunker/keys.json"

# verify the signer's pubkey is the Amber identity (must equal 35f53948…)
python3 ~/nosigner.py status | grep -E '"public_key"|"npub"'
```

If `public_key` is not `35f53948c11654c7326f69f516ec660ffef6c1b44eb7b803451381ea698f07`
(or its npub), the key is wrong — fix before proceeding.

## Path A — one-shot sign, then publish (recommended)

`nosigner sign` takes an **unsigned** event JSON and returns the signed event.
Build the unsigned event, sign it, verify, publish:

```bash
export HERMES_HOME="$HOME/.nosigner"
REL=https://raw.githubusercontent.com/felixfelix-bot/tollgate-module-basic-go/research/wallet-migration/research/wallet-migration/04-reports/nostr-post.md

# 1. unsigned NIP-23 event (kind 30023), content = the post body
python3 - "$REL" <<'PY'
import json, sys, time, urllib.request
body = urllib.request.urlopen(sys.argv[1], timeout=30).read().decode()
ev = {
  "kind": 30023,
  "created_at": int(time.time()),
  "tags": [
    ["d", "tollgate-wallet-migration-2026-09"],
    ["title", "Choosing a Cashu wallet for an OpenWrt router: gonuts vs CDK vs nucula"],
    ["summary", "What a feasibility programme learned about replacing a forked, dead-upstream Cashu wallet on flash-constrained OpenWrt routers."],
    ["t", "cashu"], ["t", "nostr"], ["t", "openwrt"], ["t", "tollgate"], ["t", "bitcoin"],
  ],
  "content": body,
}
json.dump(ev, open("/tmp/nostr-post.unsigned.json", "w"))
print("wrote /tmp/nostr-post.unsigned.json")
PY

# 2. sign with the Amber key
python3 ~/nosigner.py sign --file /tmp/nostr-post.unsigned.json > /tmp/nostr-post.signed.json

# 3. verify the signature
nak verify < /tmp/nostr-post.signed.json && echo "signature OK"   # exit 0

# 4. publish
nak event \
  wss://relay.damus.io wss://nos.lol wss://nostr.mom \
  wss://relay1.orangesync.tech wss://relay2.orangesync.tech \
  < /tmp/nostr-post.signed.json
```

Record the returned `id` in `04-reports/nostr-post.meta.md` (`## Published`).

**Offline alternative:** if the machine cannot reach GitHub, copy
`04-reports/nostr-post.md` over and read it locally instead of the URL.

## Path B — NIP-46 daemon (only if you need a live remote signer)

```bash
python3 ~/nosigner.py authorize "$(nak key public <CLIENT_HEX>)"   # pre-authorize the client
python3 ~/nosigner.py daemon --show-url --relays wss://nos.lol     # prints the bunker:// URL
# in another shell:
nak event -k 1 -c "hello" --sec 'bunker://<pubkey>?relay=wss://nos.lol&secret=<…>' \
  --connect-as <CLIENT_HEX> wss://nos.lol
```

Known issues with this path (2026-09-17, `nostr-sdk==0.44.2`):
1. **`connect` authorizes the wrong key.** `BunkerHandlers.connect` authorizes
   `params[0]`, which per NIP-46 is the **signer's** pubkey; the real client is
   the event author. Workaround: `nosigner authorize <client-npub>` first (as
   above). Proper fix: authorize the event author in `connect`.
2. **`nak` client does not complete** (`context canceled`) even when the daemon
   promptly signs and publishes the response — an interop detail on the nak side
   (worth testing another NIP-46 client, or debugging the response event).

Until (1) and (2) are fixed, use **Path A**.

## Security

- `keys.json` holds the nsec **in plaintext** (mode `600`). Only run this on a
  trusted, ideally disk-encrypted machine. Never commit it; add `~/.nosigner/`
  to any backup exclusions.
- The daemon's pairing `secret` and any `authorized.json` entries live in the
  same state dir.
- Unlike a phone signer, this machine *is* the key holder — that is the tradeoff
  for reliability.

## Troubleshooting

- `AttributeError: … 'HandleNotification'` → you installed `nostr-sdk>=0.45`;
  install `0.44.2`.
- `status` shows the wrong npub → wrong `keys.json` (or `HERMES_HOME` unset).
- `nak event` prints `status 503` for damus → damus rate-limiting; use the other
  relays.
- Signature invalid → ensure you signed the **exact** unsigned JSON you publish
  (`--file` same file).

## Evidence (Phase 1, ephemeral test)

- Ephemeral npub `npub1g7s0qegpmyf0lwy2w0umshh0gzaty5s0pke696ucyqap66azsxqs9m5pj9`
  (hex `47a0f065…`), state isolated via `HERMES_HOME=~/r2-work/nosigner-test`.
- Test event id `ea0f1534956a8fd65dedea820269e37aeb2e72c0cbe5be3461ddfc02dcd1c67d`,
  fetched back from `wss://nostr.mom` and `wss://relay.primal.net`.
