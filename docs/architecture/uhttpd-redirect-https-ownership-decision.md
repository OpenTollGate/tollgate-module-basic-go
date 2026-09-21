# uhttpd.main.redirect_https Ownership — One Derived Value, One Rule, Two Writers

## Status: Decided (2026-09-21)

`uhttpd.main.redirect_https` is a **derived value**, not a configured one: one
rule, evaluated by two writers. LuCI's `:8080` may only be redirected to the
TLS listener when that listener is real.

## The rule

```sh
if [ -r /etc/uhttpd.crt ] && [ -s /etc/uhttpd.crt ] &&
   [ -r /etc/uhttpd.key ] && [ -s /etc/uhttpd.key ]; then
    uci set uhttpd.main.redirect_https='1'
else
    uci set uhttpd.main.redirect_https='0'
fi
```

Readable, non-empty cert/key pair **and** (in the feed's script) a configured
`listen_https`. Inside this module's `setup_uhttpd()` the `listen_https` list is
added under the same cert/key condition, so there the rule reduces to the cert
check — it is still written out in full so the two scripts visibly match.

## Why two writers

`packaging/files/etc/uci-defaults/99-tollgate-setup` (this module) and the
feed's vendored `92-tollgate-admin-setup` both write `uhttpd.main`. They run in
numeric order (`92` first), but numeric order is not ownership: whichever wrote
last holds the value. A full re-assert on one side is therefore only durable if
the other side also re-asserts its own contract on the same install.

## What the disagreement cost

The 2026-09-21 pre13 build left a router whose `:8080` answered
`307 → https://<router>/` while nothing listened on `:443` (`:443` closed,
`:8443` closed, `:8090` 200, `:2051` 403). The feed's script had set
`redirect_https='1'`; this module's script took its same-version branch, which
never re-ran `setup_uhttpd` at all. The operator was locked out of LuCI.

## Consequences

- Neither script may hardcode this option again: both evaluate the rule above.
- `99-tollgate-setup` re-asserts its whole uhttpd contract (`setup_uhttpd`,
  `setup_uhttpd_portal`, and the `:8090` configUI repair) on the same-version
  reinstall/upgrade path, committing `uhttpd` only when the config changed.
- A router that lost its certs converges back to `redirect_https='0'` on the
  next install or boot instead of redirecting to a dead listener.
- The feed's companion change adds a fail-open post-restart check that turns
  the redirect back off when no listen socket exists on `:443`. Both writers
  must be updated together whenever this rule changes.
