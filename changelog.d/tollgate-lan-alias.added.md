- **`tollgate.lan` now resolves on every install path.** The package writes the
  fixed name `tollgate.lan` (and `tollgate.local`) to `/etc/hosts` and to
  `dhcp.@dnsmasq[0].address`, pointing at the router's **management** address
  (`network.private.ipaddr`, `br-private`) — where the board and LuCI answer. A
  manual install now gets the name the wizard always set, so the admin UIs are
  reachable by name on both paths. It deliberately does **not** set
  `nodogsplash.@nodogsplash[0].gatewaydomainname`: setting that re-arms the
  pre-auth redirect loop of #428. Idempotent on both the full-setup and the
  same-version paths (prior `/tollgate.lan*` entries are purged before the fresh
  one is written).
