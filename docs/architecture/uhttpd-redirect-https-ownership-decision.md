# uhttpd.main.redirect_https Ownership — One Derived Value, One Rule, Every Writer

## Status: Decided (2026-09-21); rule amended (2026-09-26)

`uhttpd.main.redirect_https` is a **derived value**, not a configured one: one
rule, evaluated by every writer of `uhttpd.main`. LuCI's `:8080` may only be
redirected to the TLS listener when that listener can actually validate the
address the browser used.

## The rule

```sh
# the certificate uhttpd.main will present covers THIS router (SANs)
if [ -r "$cert" ] && [ -s "$cert" ] && [ -r "$key" ] && [ -s "$key" ] &&
   "$TOLLGATE_CLI" ssl covers "$cert"; then
    uci set uhttpd.main.redirect_https='1'
else
    uci set uhttpd.main.redirect_https='0'
fi
```

A readable, non-empty cert/key pair **and** (in the feed's script) a configured
`listen_https` **and** — since the 2026-09-26 amendment — a certificate whose
**SANs cover this router**. The coverage verdict has exactly one implementation,
`tollgate ssl covers` (`src/cmd/tollgate-cli/ssl.go`): `x509.VerifyHostname`
against the configured hostname, its `<hostname>.lan` alias (dnsmasq serves the
system hostname in the `lan` zone) and the LAN IP. A CommonName with no SAN
extension is deliberately **not** coverage, because modern browsers ignore it.

The shell wrapper fails **closed**: an absent or unusable CLI is "does not
cover", so the redirect stays off rather than pointing a browser at an identity
nobody checked.

### Superseded form (2026-09-21 … 2026-09-26)

```sh
if [ -r /etc/uhttpd.crt ] && [ -s /etc/uhttpd.crt ] &&
   [ -r /etc/uhttpd.key ] && [ -s /etc/uhttpd.key ]; then
```

It was replaced because the OpenWrt image's **own** certificate satisfies it.
That file is a placeholder (subject `CN=OpenWrt`, `SAN DNS:OpenWrt`, 561 bytes
on the bench MT3000, dated with the image) which covers neither the router's
hostname nor its LAN IP — so the rule derived `1` on a router with no identity
at all.

## Why more than one writer

`packaging/files/etc/uci-defaults/99-tollgate-setup` (this module) and the
feed's vendored `92-tollgate-admin-setup` both write `uhttpd.main`. They run in
numeric order (`92` first), but numeric order is not ownership: whichever wrote
last holds the value. A full re-assert on one side is therefore only durable if
the other side also re-asserts its own contract on the same install.

Since 2026-09-26 the module's CLI is a writer too, for the same reason: an
operator who runs `tollgate ssl apply` (or `ssl remove`) by hand must not leave
the derived value describing a certificate that is no longer there.

## What the disagreement cost

The 2026-09-21 pre13 build left a router whose `:8080` answered
`307 → https://<router>/` while nothing listened on `:443` (`:443` closed,
`:8443` closed, `:8090` 200, `:2051` 403). The feed's script had set
`redirect_https='1'`; this module's script took its same-version branch, which
never re-ran `setup_uhttpd` at all. The operator was locked out of LuCI.

## Consequences

- Neither script may hardcode this option again: both evaluate the rule above.
- `99-tollgate-setup` re-asserts its whole uhttpd contract (`setup_uhttpd`,
  `setup_uhttpd_portal`, the TLS identity and the `:8090` configUI repair) on
  the same-version reinstall/upgrade path, committing `uhttpd` only when the
  config changed.
- A router that lost its certs converges back to `redirect_https='0'` on the
  next install or boot instead of redirecting to a dead listener.
- The feed's companion change adds a fail-open post-restart check that turns
  the redirect back off when no listen socket exists on `:443`. Both writers
  must be updated together whenever this rule changes.

## Amendment 2026-09-26 — the rule requires coverage, and the setup path provisions the identity

Measured read-only on the bench GL-MT3000 (OpenWrt 25.12.5, package
`tollgate-wrt 0.6.0_alpha4_pre17-r1`, module pin `2796d96c`):

```text
tollgate ssl status                  -> SSL: not configured
/etc/uhttpd.crt                      -> CN=OpenWrt, SAN DNS:OpenWrt, 561 bytes, dated with the image
uci show uhttpd                      -> redirect_https='1', cert='/etc/uhttpd.crt'
curl http://192.168.1.1:8080/        -> 307 https://192.168.1.1/
router hostname / LAN IP             -> tollgate-OQ3Q / 192.168.1.1
```

The product's own TLS provisioning had never run, the image's placeholder was
satisfying the existence/size guard, and the `:8080` hop therefore handed a
browser a certificate that validates neither the hostname nor the LAN IP — a
hard certificate error (not the expected self-signed "not trusted" prompt),
which is a **hostname mismatch**. Two changes, one rule:

1. **The install path provisions the identity** (`provision_tls_identity` →
   `tollgate ssl apply -y --no-restart`, the module's own generator — no second
   certificate generator). It runs on the full-setup path and on the
   verify/repair path, is idempotent, and generates nothing while an identity
   that already covers the router is in place. `--no-restart` exists because
   uci-defaults runs before procd starts the services: the script delivers the
   change itself (`converge_uhttpd_runtime`) and only when uhttpd is already
   running.
2. **The guard requires coverage** — the rule above. A router whose identity
   cannot be shown to cover it keeps its `:443` listener (the image's
   certificate is retained as a fallback so TLS does not disappear) but never
   redirects to it.

What an operator sees afterwards: `tollgate ssl status` reports the identity,
its SANs and whether it covers the router; the admin URLs are
`https://<hostname>.lan/` and `https://<lan-ip>/`, both now validating against
the presented certificate, with the browser's ordinary self-signed "not
trusted" prompt — that prompt is expected. `tollgate ssl remove` restores the
previous certificate and re-derives the redirect through the same rule, so it
cannot leave the hop armed behind a placeholder.

Still outstanding, deliberately: the feed's `92-tollgate-admin-setup` evaluates
the **older, weaker** form of the rule (file existence), so an install where
`92` writes last can still arm the hop on a placeholder. That is the companion
change named under Consequences; it lives in another repository and cannot land
before this one.
