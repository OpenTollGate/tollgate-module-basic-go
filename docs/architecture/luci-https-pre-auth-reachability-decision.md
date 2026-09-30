# LuCI's TLS Port Must Be Reachable Pre-Auth — Both Halves of the Redirect

## Status: Superseded (2026-09-26)

**Superseded by the guest-path change of 2026-09-26** (`pr/guest-path-luci-and-80`
in `tollgate-module-basic-go`). LuCI is now a **management-path-only** surface:
neither `:8080` nor `:443` is on nodogsplash's pre-auth allow list any more, both
are `del_list`-removed on every setup path, and
`packaging/files/etc/nftables.d/32-luci-not-guest-reachable.nft` drops both ports
on `br-lan` at fw4 input priority -1 for both address families. The argument below
("`:443` belongs in the list wherever `:8080` does") was correct **given that
`:8080` was reachable pre-auth**; that premise was the defect, so the contract it
describes no longer exists. Why it was wrong, and what answers a guest instead:
[Why this was reversed](#why-this-was-reversed-2026-09-26).

## Status: Decided (2026-09-22) — superseded, see above

Reaching LuCI from the captive LAN is a **two-port contract**: nodogsplash's
pre-auth allow list must cover the port that answers the client and the port
that client is then redirected to. The `:8080 → https://<router>/` redirect is
only as reachable as its target, so `:443` belongs in the list wherever
`:8080` does.

## The rule

```sh
if ! echo "$nds_users" | grep -qE '(^|[[:space:]])port 443([[:space:]]|$)'; then
    uci add_list nodogsplash.@nodogsplash[0].users_to_router='allow tcp port 443'
fi
```

Written by `setup_nodogsplash()` in
`packaging/files/etc/uci-defaults/99-tollgate-setup`, next to the `:8080`
rule, at full setup: everything the captive LAN may reach before
authentication is written in one place, in one pass, on the first boot after
an install.

## Why `:443` was missing, and what it cost

`uhttpd.main.redirect_https` is a derived value (see
[uhttpd-redirect-https-ownership-decision.md](uhttpd-redirect-https-ownership-decision.md)):
it is `1` whenever the cert/key pair is readable, which means a captive-LAN
client that hits the LuCI port on `:8080` is answered with
`307 Location: https://<router>/`. The pre-auth allow list covered `:2121`,
`:8080`, `:2050`, `:2051`, `:8090` and `:8443` — every port the setup script
owns except the one the redirect targets. (`:8090` and `:8443` have since been
dropped from the pre-auth list; see Consequences below.) nodogsplash REJECTs
`:443` for an unauthenticated client, so the redirect dead-ended on a blocked
port and LuCI was unreachable before authentication: precisely the operator
lockout the sibling decision above exists to prevent. Reproduced on hardware
on the 2026-09-22 pre15 build.

The rule was never a matter of disagreement — it was a matter of a missing
writer:

- the Go CLI already writes it for its own TLS path
  (`src/cmd/tollgate-cli/ssl.go`: `allowPort443` / `removePort443Allow` around
  the `ssl enable`/`ssl disable` commands), so a router whose TLS was enabled
  from the CLI worked;
- `:8443`, the admin board's opt-in HTTPS port, has been in the list for the
  same reason since the admin board was folded in;
- first boot was the only path that never added it, which is exactly the path
  every freshly flashed router takes.

## Why the check matches a whole field

`"allow tcp port 8443"` must never satisfy a check for `:443`, and the two
neighbouring rules are one character apart. The match is therefore delimited
on both sides rather than end-anchored (`port 443$` would be satisfied by a
list whose last entry is `allow tcp port 8443` under a space-separated
rendering — checked with BusyBox `grep -E`). uci may print a list option
either one entry per line or space separated, so the delimiters are
`[[:space:]]` and the line boundaries, not a bare `$` alone.

## Consequences

- The pre-auth contract for an unauthenticated captive-LAN client is now the
  full list: `:2121`, `:8080`, `:2050`, `:2051`, `:443`. The admin board's
  `:8090`/`:8443` are **not** on it: the board is owner-facing and is kept off
  the captive bridge by two layers — `del_list` removing their entries from
  nodogsplash's pre-auth list in `assert_nodogsplash_allow_entries`
  (`packaging/files/etc/uci-defaults/99-tollgate-setup`), and the
  unconditional packet-filter rule in
  `packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft`,
  which drops `:8090`/`:8443` on `br-lan` at fw4 input priority -1 for both
  address families and therefore also covers an *authenticated* guest, which
  the allow list cannot. A port added to `uhttpd.portal`/`uhttpd.admin`, or
  targeted by a redirect either of them emits, still has to be reflected here
  in the same change — but an admin-board port is a change to those two
  layers, never an addition to the pre-auth allow list.
  `tests/packaging/admin-board-not-guest-reachable_test.sh` is the drift
  guard: it fails if any shipped writer re-adds `:8090`/`:8443` to
  `users_to_router`, and pins the drop rule, its protocol coverage, and its
  installation by both packaging paths.
- Behaviour is unchanged for `redirect_https='0'` routers (no cert/key pair):
  the rule is an allow, not a listener, so it costs nothing when no TLS
  listener exists. It does not create one — a router with the redirect off
  still answers LuCI on `:8080`.
- The fix is deliberately in the allow list, not in the redirect: dropping
  `redirect_https=1` would hide the TLS listener from operators who have one.
- The same-version reinstall/upgrade branch of `99-tollgate-setup` re-asserts
  only the uhttpd contract, so a router that is reinstalled with an unchanged
  version marker keeps its old allow list until a full setup runs; an upgrade
  to a new version (the normal case) takes the full path and gains the rule.
  Extending the short branch to re-assert the nodogsplash allow list is the
  follow-up if that gap is ever observed in the field.
- `tests/uci-defaults-nodogsplash-443_test.sh` (offline, fake `uci`, no
  daemon, no network) pins the behaviour: the rule is added on a fresh
  install, not duplicated on re-runs, not duplicated when `tollgate-cli ssl
  enable` already wrote it, and — the trap — still added when the list ends
  in `allow tcp port 8443`, in both list renderings.
  **Superseded 2026-09-26:** that file is now
  `tests/uci-defaults-luci-ports-not-pre-auth_test.sh`, and it pins the
  opposite end state — `:8080`/`:443` are absent from the list on every setup
  path and are actively removed from a list that carries them.

## Why this was reversed (2026-09-26)

Measured on the bench MT3000 on 2026-09-25 (pre17, first-hand from a MAC the box
had never seen — the review-club vantage):

| probe | result |
|---|---|
| OS-detection redirect chain (`:2050` → `/splash.html?redir=…`) | `307` → portal ✓ |
| portal SPA `:2051` | `200` ✓ |
| admin board `:8090` | refused ✓ |
| `:80` | `307` → portal ✓ (pre-auth only) |

Everything held except the path a **tester or operator** actually walks. Two
defects:

1. **LuCI answered a guest.** `curl -m5 http://192.168.1.1:8080/` → `307
   https://192.168.1.1/`, and `https://tollgate.lan` served LuCI's login
   (self-signed warning first). The operator read exactly that as "the install is
   broken" — reasonably, because a customer-facing network must never answer a
   browser with the router's administration login. `:8080`/`:443` are the ports
   *this document* put into the pre-auth list: it was a reachability fix for an
   admin surface that should not have been reachable pre-auth at all.
2. **`:80` was dead for a trusted or authenticated client.** nodogsplash DNATs
   `:80` to `:2050` only for a pre-auth client; for a trusted (mark `0x20000`) or
   authenticated (`0x30000`) one its nat chain returns before the DNAT and nothing
   listened on `:80`, so `http://<router>/` — the URL a tester types, and the one
   a paying customer types to get back to the portal — answered nothing. The
   client fell through to `https://<router>/` → LuCI (defect 1).

**What answers a guest now.** The portal, and only the portal: `:2050`/`:2051`
(the pre-auth DNAT and the SPA) plus the backend API `:2121` — that is the whole
of the pre-auth allow list. `:80` serves a redirect stub to the SPA on `:2051`
from this package's own `uhttpd.trusted` instance
(`setup_uhttpd_trusted_entry` in `99-tollgate-setup`), so a trusted or
authenticated client gets a working plain-HTTP URL instead of an admin login. The
operator keeps LuCI on the management path — `br-private` (the private SSID) and
loopback — which the drop rule does not match. An operator who runs with the
private network disabled administers the router through the module CLI
(`tollgate status`, `tollgate wallet balance`, …).

**Layered, exactly like the `:8090` board.** The allow-list half is the
node-scoped fix, but that list is written by two scripts and repaired on install;
the packet-filter rule is the half that does not depend on the list being in the
intended state, and the only half that also covers an *authenticated* guest, whose
traffic `20-nds-enforce.nft` accepts by mark.
`tests/packaging/luci-not-guest-reachable_test.sh` is the drift guard: it fails if
any shipped writer re-adds `:8080`/`:443` to `users_to_router`, pins the drop
rule's ports and protocol coverage, and requires both packaging paths to install
it.

**Known, covered exception.** `tollgate-cli ssl enable` still adds the `:443`
allowance (`src/cmd/tollgate-cli/ssl.go`). It is now redundant — nothing on the
customer path uses TLS — and harmless while it lasts: the packet filter drops
`:443` on `br-lan` whatever the list says, and the next install `del_list`s the
entry again.
