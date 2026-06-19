<!--
Target: Comment on https://github.com/OpenTollGate/tollgate-module-basic-go/issues/95
Title idea: "Architecture finding: HTTPS work is LuCI-admin-only by design; captive portal is HTTP — camera cannot work there"
-->

## Architecture finding (no fix proposed — needs maintainer decision)

I read the SSL/HTTPS implementation against this issue. The headline finding
is that the recent HTTPS work (#123, #142, plus commits `e8e252e` "serve HTTPS
when TLS certificate files exist" and `612860a` "SNI-based multi-cert TLS
support") **does not address this issue**. By design, the captive portal
splash page is still served over plain HTTP. The HTTPS work targets the LuCI
admin interface only.

### Evidence: the SSL setup explicitly excludes the captive portal

`src/cmd/tollgate-cli/ssl.go` is the user-facing SSL setup tool
(`tollgate ssl apply` / `ssl remove` / `ssl status`). Its own user-facing
output:

```
The captive portal will continue using HTTP interception.
Portal URL: http://<domain>/ (NoDogSplash, HTTP only)
LuCI URL:   https://<domain>/ (uhttpd, HTTPS, self-signed)
```

For a real-cert flow:

```
[4] nodogsplash: gatewaydomainname='<domain>' (portal stays on HTTP port 80)
[5] nodogsplash: allow tcp port 443 so clients can reach uhttpd HTTPS
```

`configureNodogsplash()` (ssl.go:656) sets `gatewaydomainname` but explicitly
keeps `gatewayport` on 80 — i.e. NoDogSplash still intercepts on HTTP. Port
443 is allowed through `users_to_router` only so a client can manually reach
uhttpd's HTTPS for the **admin UI**.

### Why this matters for the QR camera

`navigator.mediaDevices.getUserMedia()` (the camera API the QR scanner uses)
requires a **secure context** in all modern browsers:
<https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts>.

A page is a secure context if served over `https://` or `wss://`, or from
`localhost` / `file:///` / the test origins. Plain HTTP from a non-localhost
origin is **not** a secure context (with the historical exception of some
LTE-attached devices, which is not the deployment target here).

So:

- **LuCI admin over HTTPS** — secure context — `getUserMedia()` works — QR
  scanner can run there. (This is probably the "browser mode" the user
  observed.)
- **Captive portal splash over HTTP via NoDogSplash** — **not** a secure
  context — `getUserMedia()` is undefined / blocked — QR scanner **cannot
  work** in the captive portal mini-browser.

The QR scanner library **is bundled** in the shipped captive portal site
(`packaging/files/tollgate-captive-portal-site/assets/qr-scanner-worker.min-D85Z9gVD.js`),
but the bundle is inert under HTTP.

### Why this isn't just a config flip

Making the captive portal itself serve over HTTPS breaks OS-level captive
portal detection:

- **iOS / iPadOS** probes `http://captive.apple.com/hotspot-detect.html` over
  HTTP and expects either a 200 with Apple's magic string (not captive) or a
  redirect (captive). HTTPS interception by NoDogSplash would be rejected at
  the TLS layer (self-signed cert) before any HTTP response, and iOS would
  treat the network as "no internet" rather than "captive portal."
- **Android** does the same with `clients3.google.com/generate_204`.
- **macOS**, **Windows**, and most Linux network managers behave similarly.

So the architecture tradeoff is real:
- HTTPS captive portal → camera works, but OS detection breaks (worse UX).
- HTTP captive portal → OS detection works, but camera cannot work (this
  issue).

### Three plausible directions (pick one)

1. **Accept the constraint** — close #95 as "won't fix in the captive portal;
   QR scanner is available in the admin SPA over HTTPS only." Provide a
   manual "paste Cashu token" input in the captive portal splash as a
   fallback for users without keyboard access (already common in Cashu
   flows).
2. **Dual-mode portal** — NoDogSplash stays on HTTP for OS detection; the
   splash page links to an HTTPS version of itself on the same host (e.g.
   `https://tollgate.lan/portal`) that re-loads with camera support. Tradeoff:
   user has to tap a link / accept a self-signed cert warning. Tested pattern
   on some hotel portals.
3. **Wait for OS-accepted HTTPS** — get a real cert via ACME / DNS-01 (the
   SNI multi-cert work in `612860a` is in the right direction) and serve the
   portal over real HTTPS. Real CA certs are accepted by OS captive portal
   detection. Requires the router to have a real domain and DNS-01
   validation, which is non-trivial for a device on a private LAN.

### Cross-device evidence needed

Before committing to a direction, we should have actual device-matrix
evidence (not just spec-reading) of:
- Does the QR scanner render at all in iOS / Android captive portal mini-
  browsers on the current `main` build?
- Does the HTTPS-admin SPA's QR scanner work on the same devices in a normal
  browser?
- Does any device show a different behavior (e.g. WebKit treating the captive
  portal page as a secure context due to it being served from a private RFC1918
  IP)?

Filed a separate test issue on `physical-router-test-automation` for the
device-matrix run. See "Related."

### Recommendation

For v0.5.0, **Option 1 (accept constraint, document, ship manual-token-paste
fallback)** is the lowest-risk path. The full architecture change should be
designed separately and is out of scope for the release. Update this issue's
title to reflect that it's a known architectural limitation, not a missing
config flag.

## Related

- Cross-device QR-scanner test issue on `physical-router-test-automation`
  (link TBD).
- PR #123 — SSL/HTTPS rewrite (LuCI admin only).
- PR #142 — Go-based SSL management (LuCI admin only).
- Commit `e8e252e` — serve HTTPS when TLS certificate files exist.
- Commit `612860a` — SNI-based multi-cert TLS support.
- MDN: <https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts>
