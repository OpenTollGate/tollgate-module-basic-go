# LuCI's TLS Port Must Be Reachable Pre-Auth — Both Halves of the Redirect

## Status: Decided (2026-09-22)

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
owns except the one the redirect targets. nodogsplash REJECTs `:443` for an
unauthenticated client, so the redirect dead-ended on a blocked port and
LuCI was unreachable before authentication: precisely the operator lockout the
sibling decision above exists to prevent. Reproduced on hardware on the
2026-09-22 pre15 build.

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
  full list: `:2121`, `:8080`, `:2050`, `:2051`, `:8090`, `:8443`, `:443`.
  Any port added to `uhttpd.portal`/`uhttpd.admin`, or targeted by a redirect
  any of them emits, has to be added here in the same change.
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
