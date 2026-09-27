# uhttpd.main.redirect_https Ownership — One Derived Value, One Rule, Two Writers

## Status: Decided (2026-09-21). Rule hardened 2026-09-26 — see below.

`uhttpd.main.redirect_https` is a **derived value**, not a configured one: one
rule, evaluated by two writers. LuCI's `:8080` may only be redirected to the
TLS listener when that listener can actually validate the address the browser
used.

## The rule (2026-09-26)

```sh
# cert_covers_router <cert-file> delegates to the module's own x509 SAN check
# (`tollgate ssl covers`, src/cmd/tollgate-cli/ssl.go). Exit 0 = the certificate
# validates this router's hostname, its <hostname>.lan alias, or its LAN IP.
if [ -n "$cert" ] && cert_covers_router "$cert"; then
    uci set uhttpd.main.redirect_https='1'
else
    uci set uhttpd.main.redirect_https='0'
fi
```

Redirection requires a certificate that **covers this router**. Readable,
non-empty cert/key pair is the *precondition for the TLS listener* (uhttpd
crash-loops on `listen_https` without TLS config), never a reason to redirect.

One implementation of "covers", deliberately: the shell calls the CLI, so a
browser, the CLI and the setup script cannot disagree about what a usable
identity is. The predicate matches SANs with `x509.VerifyHostname` (so wildcards
behave as a browser expects), ignores the CommonName when the certificate
carries no SAN extension (modern browsers do), and refuses an expired
certificate. The shell side **fails closed**: an absent or unusable CLI is "does
not cover", which keeps the hop off rather than pointing a browser at an identity
nobody checked.

### The superseded rule (2026-09-21 → 2026-09-26)

```sh
if [ -r /etc/uhttpd.crt ] && [ -s /etc/uhttpd.crt ] &&
   [ -r /etc/uhttpd.key ] && [ -s /etc/uhttpd.key ]; then
    uci set uhttpd.main.redirect_https='1'
else
    uci set uhttpd.main.redirect_https='0'
fi
```

Readable, non-empty cert/key pair **and** (in the feed's script) a configured
`listen_https`. That rule was written to stop a redirect to a *dead* `:443`. It
could not distinguish a router's own identity from the **OpenWrt image's
placeholder certificate** (subject `CN=OpenWrt`, `SAN DNS:OpenWrt`), which is
readable, non-empty, and covers neither the router's hostname nor its LAN IP.

## Why more than one writer

`packaging/files/etc/uci-defaults/99-tollgate-setup` (this module) and the
feed's vendored `92-tollgate-admin-setup` both write `uhttpd.main`. They run in
numeric order (`92` first), but numeric order is not ownership: whichever wrote
last holds the value. A full re-assert on one side is therefore only durable if
the other side also re-asserts its own contract on the same install.

Since 2026-09-26 the module's CLI is a writer too, for the same reason: an
operator who runs `tollgate ssl apply` (or `ssl remove`) by hand must not leave
the derived value describing a certificate that is no longer there. Both
commands re-derive it through `applyRedirectHTTPS` in
`src/cmd/tollgate-cli/ssl.go`, from the same coverage predicate the setup path
calls.

## What the disagreement cost

**2026-09-21 (pre13):** the feed's script set `redirect_https='1'`; this module's
script took its same-version branch, which never re-ran `setup_uhttpd` at all. A
router whose `:8080` answered `307 → https://<router>/` had nothing listening on
`:443` (`:443` closed, `:8443` closed, `:8090` 200, `:2051` 403). The operator
was locked out of LuCI.

**2026-09-26 (pre17, GL-MT3000 bench — the operator-reported "https problems
when logging into the admin portal"):** a router with a *live* `:443` still
could not be logged into, because the certificate behind it was the image's
placeholder. Measured read-only on the bench, before the fix:

| probe | value |
| --- | --- |
| `uci get network.lan.ipaddr` | `192.168.1.1/24` |
| `uci get system.@system[0].hostname` | `tollgate-OQ3Q` |
| `uci show uhttpd \| grep -E 'redirect_https\|cert'` | `redirect_https='1'`, `cert='/etc/uhttpd.crt'` |
| `ls -l /etc/uhttpd.crt` | 561 bytes, dated with the image (Jun 29) |
| `openssl s_client -connect 192.168.1.1:443 \| openssl x509 -noout -subject -ext subjectAltName` | `CN=OpenWrt`, `DNS:OpenWrt` |
| `curl -o /dev/null -w '%{http_code} %{redirect_url}' http://192.168.1.1:8080/` | `307 https://192.168.1.1/` |
| `tollgate ssl status` | `SSL: not configured` |

The existence-only guard accepted the placeholder, so setup derived `1` and
added `listen_https` on top of a certificate that cannot validate `192.168.1.1`,
`tollgate-OQ3Q` or `tollgate-OQ3Q.lan`. Every admin login started with a hard
certificate error, and what answered was LuCI, not the TollGate board. The
login itself was never broken: `POST /ubus session.login` over `:8443` and
`:8090` both returned a real session, and the SPA's own pre-login probe
(`tollgate auth_status`) answered `{"state":"set","password_set":true}`.

## Consequences

- Neither script may hardcode this option again: both evaluate the rule above.
  **The feed's `92-tollgate-admin-setup` still carries the superseded
  existence-only guard and must be updated to the same rule** — this repository
  cannot change it. Until it is, correctness depends on install order: `99` runs
  after `92` and lands the coverage-checked value last, so the shipped
  combination is safe, but the two writers do not yet evaluate the same premise.
- `99-tollgate-setup` **provisions** the router's TLS identity instead of
  inheriting the image's placeholder: on both the full-setup and the
  verify/repair path it drives the module's own generator
  (`tollgate ssl apply -y --no-restart`, `provision_tls_identity`), then
  re-derives `cert`/`key`/`listen_https`/`redirect_https` from what is now on
  disk (`setup_uhttpd_tls_identity`) and converges a running uhttpd
  (`converge_uhttpd_runtime`). One generator, shared with the CLI — never a
  second certificate generator in shell.
- `99-tollgate-setup` re-asserts its whole uhttpd contract (`setup_uhttpd`,
  `setup_uhttpd_portal`, the TLS identity and the `:8090` configUI repair) on the
  same-version reinstall/upgrade path, committing `uhttpd` only when the config
  changed. A router that lost its certs converges back to
  `redirect_https='0'` on the next install or boot instead of redirecting to an
  identity that cannot validate it.
- **A failed or refused provisioning is never fatal, and never redirects.** No
  CLI, no LAN IP, a provider that cannot provision: the install completes, the
  router keeps its `:443` listener with whatever certificate it has, and the
  hop stays off. The reason is in `/tmp/tollgate-setup.log` and in
  `tollgate ssl status`, which now reports the coverage of whatever uhttpd
  serves.
- **The operator's removal is honoured.** `tollgate ssl remove` writes
  `/etc/tollgate/ssl/tls-identity-removed`; the setup path does not provision
  while that marker exists, so a reinstall cannot silently re-key a router whose
  owner asked for no identity (the same class as `setup_hostname` never touching
  a custom hostname, #444). `tollgate ssl apply` clears the marker.
- Documented operator URL after an identity exists: **`https://<hostname>.lan/`**
  (`setup_dns_dnsmasq` sets `dhcp.@domain`/`expandhosts` so the `<hostname>.lan`
  alias resolves to the LAN IP, which is also in the certificate's SANs). The
  self-signed certificate still triggers the usual "not trusted" browser
  prompt — that is expected. A **hostname mismatch** is not: that means the
  certificate does not cover the address being used.
- The feed's companion change adds a fail-open post-restart check that turns
  the redirect back off when no listen socket exists on `:443`. Both writers
  must be updated together whenever this rule changes.
- **Install order is NOT a safety net, and here is the measured order.**
  `packaging/Makefile`'s postinst runs the uci-defaults explicitly as
  `90-tollgate-captive-portal-symlink`, `99-tollgate-setup`, `92-tollgate-admin-setup`
  — so on the module's own install/upgrade pass **`92` is the LAST writer of
  `uhttpd.main.redirect_https`**. At boot they run numerically (`90, 92, 99`), so
  `99` is last there. A writer that kept the superseded existence-only premise
  therefore wins on the install pass: a router whose identity cannot be validated
  (provisioning refused, or the image's placeholder as the fallback listener
  identity) is derived to `0` by `99` and then put back to `1` by `92`, which is
  the operator-visible defect this rule was hardened for. The two writers must
  evaluate the same rule; they cannot rely on who runs last.

## Product decision: what answers the captive side (2026-09-26)

The operator-facing question behind this document — "https problems logging into
the admin portal" — is really two decisions, and only the second one is recorded
here:

1. **What is the captive side answered by?** Not the router's administration UI.
   A client that is not on the owner's private network is answered by the captive
   portal (`:2050` → `:2051`, and the pre-auth stub before that). LuCI
   (`uhttpd.main`, `:8080`/`:443`) and the owner-facing admin board
   (`uhttpd.admin`, `:8090`/`:8443`) are management surfaces, reached over the
   private network, never as part of the customer journey. The mechanical half —
   which ports an *unauthenticated* client may reach, and the packet-filter drop
   that also covers an *authenticated* guest, which the allow list cannot — is
   recorded in
   [`luci-https-pre-auth-reachability-decision.md`](luci-https-pre-auth-reachability-decision.md)
   and its companion nftables fragment, and for the board in
   `31-admin-board-not-guest-reachable.nft`. Consequence for this document: the
   `:8080` → `https://` hop is not a customer-path component and must never be
   part of the pre-auth allowance. It exists for whoever **does** reach
   `uhttpd.main` — the owner on the private network — so that an admin login is
   not typed over plain HTTP.
2. **When is that hop armed, and with which identity?** Only when the identity
   on the configured listener **covers the address the browser used** (the
   router's hostname, its `<hostname>.lan` alias, or its LAN IP — the addresses
   that actually reach `uhttpd.main` differ by network), and the identity is
   **provisioned by setup** with those SANs, never inherited from the image. A
   redirect armed on a premise weaker than "this browser can validate this
   certificate" is the whole defect class this rule exists to stop: it produced
   the pre13 lockout (redirect into a dead port) and, on 2026-09-26, a live TLS
   listener serving an identity that covers neither the router's hostname nor its
   LAN IP (hard certificate error on every login, measured on the bench MT3000,
   pre17).

**One rule, two writers — and a guard over the pair.** The second writer is
`92-tollgate-admin-setup`, whose source lives in
`OpenTollGate/tollgate-captive-portal-site` (the feed vendors a pinned copy). It
kept the superseded existence-only premise until portal PR
[#64](https://github.com/OpenTollGate/tollgate-captive-portal-site/pull/64),
which evaluates the same coverage rule through the same CLI predicate and adds a
cross-repo guard
(`packaging/tests/test-redirect-https-single-rule.sh`) that fails if **either**
writer carries a premise of its own. Two caveats, deliberately recorded instead
of implied: (a) the source moving is not the router receiving it — the module's
`packaging/build-inputs.json .portal.commit` and the feed's `vendor.lock.json`
must be advanced for the aligned script to ship, and until then the install-order
hazard above is live on the module's postinst pass; (b) this document's rule is
about the derived value only — a change to *which* ports the captive side may
reach is a change to decision 1, in the reachability document and the drop rules,
never a tweak to this redirect.
