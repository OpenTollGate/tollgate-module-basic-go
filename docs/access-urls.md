# TollGate admin-UI access URLs

TollGate ships two administration surfaces on the router's management
network (`br-private`, `br-mgmt`, or loopback). Each surface is served
by its own `uhttpd` instance with a strict, single-port-owner design.
The plain-HTTP and TLS listeners for one surface never share a port
with the other surface, and the plain-HTTP ports redirect to the
TLS ports only when a certificate is in place.

| Surface | What to type | Where it lands | uhttpd instance | Notes |
|---|---|---|---|---|
| **TollGate config UI (board)** | `http://tollgate.lan:8080/` | `https://tollgate.lan/` | `uhttpd.admin`, webroot `/www/<brand>` | The SPA dashboard for the router. Owns the **entry pair** under the default `entry_ui=board`. |
| **LuCI (OpenWrt admin)** | `http://tollgate.lan:8090/` | `https://tollgate.lan:8443/` | `uhttpd.main`, webroot `/www` | `tollgate.lan` resolves to the router's LAN IP (default `192.168.1.1`). |

Which surface owns which pair follows the `entry_ui` field of
`/etc/tollgate/config.json` (`board` | `luci`, default `board`); the table
above is the `board` mapping. The `luci` mapping is the inverse (LuCI on
`:8080`/`:443`, the board on `:8090`/`:8443`) — a router showing it is on a
pre-flip build (or has the switch set to `luci`), not broken. Confirm the live
mapping with `cat /etc/tollgate/entry-ui-mapping` (must print `board`) or
`uci show uhttpd | grep -E 'listen_http|listen_https'`.

## Why the `https://<host>:8090` and `https://<host>:8080` forms do not work

uhttpd has a **single-port-owner design** on a TollGate router:

- `uhttpd.admin` (the board) owns **`:8080` (plain HTTP)** and **`:443` (TLS)**.
- `uhttpd.main` (LuCI) owns **`:8090` (plain HTTP)** and **`:8443` (TLS)**.

Each listener speaks exactly one protocol. A TLS handshake sent to a
plain-HTTP listener will fail, so:

- `https://tollgate.lan:8080` does not work — `:8080` is plain HTTP; the
  TLS endpoint for the board is `:443`.
- `https://tollgate.lan:8090` does not work — `:8090` is plain HTTP; the
  TLS endpoint for LuCI is `:8443`.
- `http://tollgate.lan:8080/` and `http://tollgate.lan:8090/` are the
  correct user-facing entry points; uhttpd redirects them to their TLS
  counterparts when `redirect_https=1` and the certificate covers the
  host being used.

Live evidence from a bench router:

```sh
netstat -lntp | grep uhttpd
# uhttpd.admin (board) -> :8080 + :443
# uhttpd.main  (LuCI)  -> :8090 + :8443

curl -s -D- http://10.60.32.1:8080/
# HTTP/1.1 307 Temporary Redirect
# Location: https://10.60.32.1/
```

## Reachability

Guests on the open (captive) SSID are intentionally prevented from
reaching either admin surface, whichever mapping is live:

- `/etc/nftables.d/31-admin-board-not-guest-reachable.nft` drops
  `:8090` and `:8443` on `br-lan`.
- `/etc/nftables.d/32-luci-not-guest-reachable.nft` drops `:8080` and
  `:443` on `br-lan`.
- Neither admin port is in `nodogsplash`'s pre-auth `users_to_router`
  allow list.

The fragments are named for the UI they guarded when they were written
(pre-flip, when `:8090`/`:8443` was the board); they key on **port
numbers, not sections**, so after the flip each fragment's set belongs
to the other UI — and the outcome is unchanged: all four admin ports
are dropped on `br-lan` in either mapping.

If a connection to `:8090`, `:8443`, `:8080`, or `:443` is refused from
a wired-LAN or guest-SSID client, that is the guard working as designed,
not a UI failure.

## See also

- `docs/architecture/default-ui-and-entry-port-decision.md` — which UI
  answers the entry ports, and the constraints on cross-links.
- `docs/architecture/uhttpd-redirect-https-ownership-decision.md` —
  when and why the `:8080` → `https://` redirect is enabled.
- `docs/rc-tester-guide.md` — the release-candidate tester guide used by
  the review club.
