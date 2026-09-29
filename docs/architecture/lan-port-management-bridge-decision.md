# A Bridge of Its Own for the Wired LAN Ports — and the Half of That Request the Shipped Stack Cannot Deliver

> **Status: Proposed (2026-09-26).** The bridge this record proposes is
> adoptable; the request it was written for is **not fully satisfiable by a
> configuration change on the shipped stack**, and this document says so in
> plain terms in [The half that cannot work as
> specified](#the-half-that-cannot-work-as-specified) rather than describing a
> design that would silently not hold. Acceptance is a maintainer action — the
> drafting account may not accept its own proposal, and nothing here is in
> force until the rollout below has landed on a router and been measured there.
>
> **Amendment (2026-09-28): the verdict below is narrowed, not withdrawn.** It
> holds for a **second *gated* bridge**, and it does **not** apply to the
> operator's requirement — a wired-port client that still has to pay for WAN
> *and* can reach the administration surfaces is satisfiable by a configuration
> change on the shipped stack: a bridge-port-keyed re-key of the two
> admin-port guards, with no `br-mgmt` and no second gate. Read "the request
> it was written for is not fully satisfiable" as "the *second gated bridge*
> half of that request is not". See "Amendment - the requirement is satisfiable
> by a configuration change" at the end of this record. Status stays Proposed:
> the bridge proposal is neither accepted nor withdrawn here.

## Context

### What the operator measured (2026-09-26, GL-MT3000 bench, pre18)

The operator administers the router from a laptop on an **Ethernet cable into a
LAN port**. That port is a member of `br-lan`, which is also the bridge the open
guest SSID is on, so the two admin-port guards drop the administration surfaces
for him exactly as they do for a stranger on the Wi-Fi:

- `packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft:52-53`
  drops `tcp dport { 8090, 8443 }` on `iifname "br-lan"`, both families;
- `packaging/files/etc/nftables.d/32-luci-not-guest-reachable.nft:54-55` drops
  `tcp dport { 8080, 443 }` on `br-lan`, both families.

His words: "neither luci on 8080 nor the luci alternative config ui on port 8090
are reachable". His requirement: a `br-mgmt` "where the connected devices can
access luci and the luci alternative dashboard, but they still need to pay".

The report is a bench report from that pre18 run, not a tracked issue; the two
guards that produce the lockout were introduced by #566
(`31-admin-board-not-guest-reachable.nft`) and #588
(`32-luci-not-guest-reachable.nft`), and this record is the first place the two
are considered from the wired port's side.

The guards are behaving exactly as designed. Both are written for the captive
bridge by name, and both say in their own headers that the management path is
`br-private` (the private SSID) and loopback — not the wired port
(`31-*.nft:38-42`, `32-*.nft:32-40`). The module's comment on the admin board
tells an operator in this situation to "use the module CLI or LuCI on `:8080`"
(`31-*.nft:41-42`), which is the lockout he reported.

### Why the ask is right: the wired port should not share the guest's L2 domain

The wired port and the open guest SSID are one bridge, deliberately gated as
one. The cost of that sharing was already measured in this repo:
`packaging/files/etc/uci-defaults/99-tollgate-setup:1228-1247` records that
bridge port isolation **cannot** separate a wired host from a guest BSS —
netifd's `isolate` is bilateral (`br_private.h br_skb_isolated`) and was
measured on the bench to block nothing — and names the alternative mechanism in
as many words: "a dedicated br-guest with its own reject-by-default zone, or a
bridge-family nft rule keyed on the VAP ports". A bridge of its own for the
wired ports is therefore not only the operator's preference, it is the fix for
an exposure this repo could not otherwise close: an operator's administration
laptop currently shares a broadcast domain with strangers, ARP, mDNS and all.

### The three layers that assume exactly one captive gateway

The request needs **two bridges with different pre-auth reachability, both
still gated**. Every layer of the shipped stack assumes one gated interface.

**1. The daemon (nodogsplash 5.0.2, the version this stack runs —
`packaging/Makefile:59` declares `DEPENDS:=+nodogsplash +jq`, and every bench
note in this repo names 5.0.2).** A nodogsplash process manages **one** interface:
`config.gw_interface` is a single `char *` (`src/conf.h:146`), parsed from a
single value (`src/conf.c:760`) and required non-null (`src/conf.c:1388`). Its
firewall chains have **fixed names** in one network namespace
(`src/fw_iptables.h:35-44`: `ndsNET`, `ndsRTR`, `ndsAUT`, `ndsTRU`, `ndsOUT`,
`ndsBLK`, …), they are created with `-N` in **both** tables (`mangle` at
`src/fw_iptables.c:442-446`, `filter` at `:534-538`), the jumps into them are
interface-scoped (`:449-452`, `-i <gw_interface> -s <gw_iprange>`) but the chain
objects are not, and teardown **flushes and deletes them by name** —
`iptables_fw_destroy()` at `src/fw_iptables.c:689-747` is a `-F`/`-X` sweep of
every `nds*` chain in `mangle`, `nat` and `filter` that no second process's
lifetime is consulted about. Its own doc comment says as much (`:685-686`:
"used when we do a clean shutdown of nodogsplash, **and when it starts**"), and
`src/main.c:297-305` is that start path — destroy runs *before* `init`, and a
failed init destroys again and `exit(1)`s. A second instance therefore wipes the
first's enforcement **on its own boot path**, not only when someone restarts
one; and under `procd_set_param respawn` (`net/nodogsplash`
`files/etc/init.d/nodogsplash:191`) a failing instance re-enters that path — a
loop, not a one-off wipe.

**2. The OpenWrt packaging genuinely does support N instances** — and that is
the trap, because it looks like the answer and is not. In
`openwrt/packages` `net/nodogsplash`, the init script iterates the config file
(`config_foreach create_instance nodogsplash`, `files/etc/init.d/nodogsplash`)
and starts one procd instance per `config nodogsplash` section
(`procd_open_instance $cfg`), each with its own generated config file
(`/tmp/etc/nodogsplash_$cfg.conf`, written at `:172`, started at `:189-205`).
Per section it honours
`gatewayinterface` (`:125,139`), `gatewayport`, `ndsctlsocket` and
`fw_mark_{authenticated,trusted,blocked}` (`:141-147`), and those marks really
are per instance (`src/conf.c:154-156,229-231,898-901`). So the *process* layer
can start two gates; the *daemon* cannot isolate their enforcement. Concretely,
with two instances:

- both create the same chains; the second's `iptables -t … -N nds…`
  (`src/fw_iptables.c:442-446,534-538`) collides with the first's;
- **either instance starting or restarting wipes both bridges' enforcement**,
  because `iptables_fw_destroy()` is name-scoped, not interface-scoped — and it
  runs on the start path too (`src/main.c:297-305`). Restarts are not
  hypothetical: procd respawns, the fw4 hotplug restarts the daemon, and this
  module drives `/etc/init.d/nodogsplash start|stop|restart`
  (`src/cmd/tollgate-cli/main.go:705-776`). The silent consequence is *not*
  free internet on the captive bridge — there it is the opposite: with the
  marking jumps gone, `br-lan` traffic is unmarked and `20-nds-enforce.nft:33`
  (`iifname "br-lan" oifname != "br-lan" … reject`, inside `inet fw4`, so it
  outlives the restart — the resilience `31-*.nft:5-7` relies on) **rejects**
  it, so no `br-lan` client gets out, paying or not. The bridge that leaks is
  the *second* one: nothing in the shipped rules matches it once a design (A1)
  gives it a path to `wan`;
- the second instance's gated clients are only marked because the first
  instance's `ndsOUT`/`ndsBLK`/`ndsTRU` chains are what its jump lands in, so
  "who is paying" is no longer per bridge in any checkable way.

**3. This module assumes one gate, in five concrete places.**

- **The one `ndsctl` call site.** `src/valve/valve.go:96-102` runs
  `exec.CommandContext(ctx, "ndsctl", args...)` with **no `-s`**: the default
  control socket (`/tmp/ndsctl.sock`, `src/conf.h:77`), whichever instance owns
  it — and the second instance to start wins that path, because the socket is
  `unlink()`ed before `bind()` (`src/ndsctl_thread.c:111`). `ndsctl` *does* take
  a socket path (`src/ndsctl.c:62,232`); this module never passes one. That one
  function is the only `ndsctl` exec in the non-test tree, and every
  enforcement action goes through it: `authorizeMAC` (`valve.go:201-244`),
  `deauthorizeMAC` (`:281-308`), `GetClientStats` (`:912-961`),
  `CheckClientState` (`:1002-1041`) — the read-only probe the merchant's
  payment pre-flight and its stale-binding reconciliation both use, each
  through a per-merchant test seam (`src/merchant/merchant.go`, `clientProbe`
  and `staleBindings.probe`).
- **Session tracking is keyed by MAC only, never by bridge.** `openGates`,
  `gateEpochs`, `pendingCloseRetries` (`valve.go:125-147`) and
  `m.customerSessions[mac]` (`merchant.go`) carry no interface, so a MAC that
  moves between bridges has one session and no way to say which gate it belongs
  to.
- **The per-MAC sweep.** The usage monitor ticks every 2 s
  (`src/merchant/merchant.go:438`), and the reconciliation walks
  `valve.TrackedGates()` and probes each address with `ndsctl json <mac>`
  (`merchant.go:835`, `:902-906`, cadence `:696-707`). With one socket, every
  probe is answered by one instance.
- **Identity resolution is bridge-agnostic, which is what makes the failure
  quiet.** `/whoami`, `/ln-invoice`, `/balance`, `/usage`, `/session-state`
  (`src/main.go:1538-1558`) all resolve the caller through
  `clientMACFromSocket` (`:848-862`) → `getMacAddress` (`:864-898`), which reads
  the dnsmasq lease file `/tmp/dhcp.leases` (`:793`) and `/proc/net/arp`. Both
  work on any bridge, so a `br-mgmt` client resolves cleanly and can buy; the
  purchase then calls `valve.OpenGate`/`OpenGateUntil`
  (`src/merchant/lightning.go:753,761`) → `ndsctl auth <mac>` → the single
  instance answers `Client <mac> not found.` (the answer this module already
  distinguishes, `valve.go:264-271`) and the money path ends in the
  paid-but-not-granted escalation (`ErrAccessGrantNotApplied`, 503
  `access-grant-failed`).
- **The nft layers are `br-lan`-literal.**
  `20-nds-enforce.nft:22,25,28,33` (`iifname "br-lan"` for the three marks and
  the unmarked-to-WAN reject), `30-backend-firewall.nft:35-36`
  (`iifname != { "br-lan", "br-private", "lo" } tcp dport 2121 drop` — a client
  that cannot reach `:2121` cannot pay. Both LAN bridges are exempt: `br-lan`
  because the portal pays through this API, and `br-private` because the
  owner-facing board served on that network reads every value it shows from it.
  Exempting only `br-lan` left the board rendering with all of its data dead),
  `31-*.nft:52-53`, `32-*.nft:54-55`, and the
  zone-scoped `firewall.tollgate_in` rule that allows `:2121` from `lan`
  (`99-tollgate-setup:1697-1704`).

### Where the wired LAN ports are bound, and therefore who may move them

Not this repository, and not the feed recipe — **the base image**.

- This module ships **no** `/etc/config/network` at all (`packaging/files/etc/`
  has no `config/` directory), only **reads** the LAN device
  (`99-tollgate-setup:1779-1781`, `lan_dev=$(uci -q get network.lan.device)`,
  defaulting to `br-lan`), and writes only `network.lan.domain` (`:1121`) and
  `network.lan.ip6assign` (`:1725`).
- The feed recipe vendors the same writer (its copy of `99-tollgate-setup`
  carries the same `network.lan.domain`/`ip6assign` writes and no port list).
- The ports come from `/etc/board.d/02_network` at first boot: for this
  hardware `openwrt/openwrt` `openwrt-25.12`
  `target/linux/mediatek/filogic/base-files/etc/board.d/02_network:158-166` —
  `glinet,gl-mt3000` (and `gl-mt2500`, `gl-x3000`, …, and `openwrt,one`) are
  `ucidef_set_interfaces_lan_wan eth1 eth0` — which lands `eth1` as the LAN
  device in `board.json`, and `/bin/config_generate:109-119` turns that into
  `config device` + `option name 'br-lan'` + `list ports 'eth1'`. On a multi-port
  device the same call names `lan1 … lan5`
  (`02_network:98-103`, `glinet,gl-mt6000`).

Consequence: the ports list is **image-owned**, so the module must write the
move itself (it already writes `network` freely, and it already creates
`network.private`/`network.private_bridge` and `dhcp.private` the same way,
`99-tollgate-setup:1956-1967`), and it must do so **device-agnostically** — by
reading the port list that is currently on `br-lan` and moving it, never by
naming `eth1`.

## Decision

**D1 — The wired LAN ports move to a new bridge `br-mgmt`.** A new writer
(`setup_mgmt_bridge` in `99-tollgate-setup`) creates `network.mgmt`
(`proto static`, device `br-mgmt`) and `network.mgmt_bridge`
(`type bridge`, `name br-mgmt`), **moves** every port currently listed on
`br-lan` to it, and commits `network`. The `network device` section is
anonymous — `/bin/config_generate:109-119` emits it without a name — so
`network.@device[br-lan]` is **not valid UCI addressing**: the writer must find
the `@device[N]` whose `option name` is `br-lan` (`uci show network` piped to
`awk`, the idiom already in the script at `99-tollgate-setup:1731`) and move its
`ports` list. If `br-lan` has no port list, the writer **fails loudly and
changes nothing** (a bridge with no ports would leave the operator with a dead
cable and no diagnostic).

**D2 — `br-mgmt` gets its own fw4 zone, and it is a management zone with no way
out.** `firewall.mgmt_zone` (`name 'mgmt'`, `network 'mgmt'`,
`input 'ACCEPT'`) and **no forwarding to `wan`**. It must **not** join
`firewall.private_zone`: that zone is `input/output/forward 'ACCEPT'` and has a
`private → wan` forwarding (`99-tollgate-setup:2004-2006`), i.e. reusing it
would hand every wired client free internet — the exact hole this record exists
to not create. The admin listeners need no change to be reachable: they bind
`0.0.0.0`/`[::]` (`uhttpd.main` `:8080`/`:443`, `99-tollgate-setup:591-593,697-702`;
  `uhttpd.portal` `:2051`, `:617-619`; `uhttpd.trusted` `:80`, `:899-902`;
  `:8090` written here on `uhttpd.net4sats`/configUI, `:956-971`, and the opt-in
`:8443` on `uhttpd.admin` written by the feed's `92-tollgate-admin-setup` —
this script only clears that listener, `:1055-1059`).

**D3 — `br-mgmt` serves DHCP.** `dhcp.mgmt` (`interface 'mgmt'`), so the
operator's laptop gets an address and the router has a lease to resolve it by —
the same shape as `dhcp.private` (`:1966-1967`).

**D4 — `br-mgmt` is positively scoped: admin surfaces only.** An input fragment
for `br-mgmt` accepts DHCP/DNS, SSH and the four admin ports
(`443, 8080, 8090, 8443`) and **drops everything else**, counter-tagged. This is
deliberately an allow list and not a blacklist, because it has to carry a
property a blacklist cannot state:

**D5 — The customer/payment surfaces do not exist on `br-mgmt`.** `:2050`,
`:2051` and `:2121` are **not** on the `br-mgmt` allow list. A network this
module cannot gate must not be able to buy: as shown in Context, a `br-mgmt`
client resolves a MAC, can be sold a session, and the grant then fails in the
single instance's `ndsctl` — money taken, nothing delivered, `access_granted`
false. Blocking the payment surface is the only honest state until a real second
gate exists (F1/F2 below).

**D6 — Exactly one nodogsplash instance, pinned.** `setup_nodogsplash` keeps
writing `nodogsplash.@nodogsplash[0].gatewayinterface='br-lan'`
(`99-tollgate-setup:1476`) and `assert_nodogsplash_allow_entries` keeps writing
one `users_to_router` list (`:1370-1457`). No second `config nodogsplash`
section is ever created, by any shipped writer. This is stated as an invariant
with a drift guard because it is the one change that would look like it
implements this request while breaking the gate (see rejected alternative A1).

**D7 — The two guards and the enforcement fragment keep their scope, and are
pinned to it.** `31-*.nft`, `32-*.nft` and `20-nds-enforce.nft` keep matching
`iifname "br-lan"` and must **never** be extended to `br-mgmt`: a `br-mgmt`
client is not a guest, and the reason the admin ports may be reachable there is
that the bridge holds no stranger. The guest APs stay bound to
`network=lan` (`99-tollgate-setup:1268`), so the open SSID keeps the guards.

**D8 — The requirement's paywall half is explicitly not delivered here.** The
wired bridge is a management bridge: it reaches the administration surfaces
before any payment, and it reaches nothing else, including the internet. See
below for what it would take to make it a second *paywalled* network, and why
that is not this change.

### D9-D12 — The two settings this makes operator-configurable (2026-09-26)

> **Re-derived 2026-10-04 (rebase onto main after #605/#624/#625).** D9-D12
> were written against a tree where the private SSID was minted from the nym
> alone, the wired LAN ports sat on the captive bridge, and the admin password
> was generated with `od`. All three premises moved; the inline notes below
> state the re-derived reading at each affected point, and the defaults are
> unchanged — each is *more* of a no-op in the new world, not less. The
> citations to `99-tollgate-setup` were re-checked against the post-#605/#625
> file (line numbers below are the current ones).

The operator asked for two things to be configurable rather than compiled in:
the private network's credentials, and **which network may reach the
administration surfaces** (today the answer is "whatever is not the captive
bridge", written as a literal in two fragments). Both must be settable in the
config file and from the admin board. D9-D12 record the design; the
implementation is separate PRs, and the release order is at the end.

**D9 — The private network's SSID, passphrase and encryption become declared
values in `/etc/tollgate/config.json`, and UCI stays the runtime.** The fields
are `private_ssid`, `private_key` and `private_encryption`, flat and top-level
like every other settable scalar (a nested object gets no dot-path round-trip
test — see the settings-surface map). They are declared intent, not
configuration the service reads: hostapd starts from `/etc/config/wireless`, so
nothing consumes these keys directly. One applier
(`src/cli/operator_settings.go`) converges them onto UCI, and it runs from the
three places the value can change: after `config set`/`config save`, on
`tollgate config apply`, and at daemon start (so a hand-edited file — or a
`sysupgrade` that kept the config and regenerated UCI — converges without a
second command). The applier is a **compare-and-converge** writer, not a
write-always one: on a router whose UCI already matches, it reports `unchanged`
and does not commit UCI or bounce the wireless. That is what makes the daemon
start path safe at all — procd respawns this process, and a needless
`wifi reload` on every respawn would drop every associated client on the
private network.

Two asymmetries are deliberate and load-bearing:

- **An empty value is not an instruction: it means "keep what the router
  has".** `private_ssid` and `private_key` ship empty, because the first values
  are *minted*, device-by-device, by `setup_private_network`
  (`99-tollgate-setup:1896-2010`). Re-derived for #605: the SSID is now
  `<nym>-<code>`, built from the operator's stored nym and the **one stored
  device code** that also names the hostname and the captive SSID, and the
  passphrase is still the urandom-seeded word list (3 words + 2 digits, one
  `awk` from a single `/dev/urandom` read). The empty-means-keep default
  composes with that derivation rather than fighting it: the applier writes
  only declared non-empty values, and `setup_private_network` re-derives the
  SSID only while it is *machine-shaped* — a value this applier wrote (or a
  `tollgate network private rename`) is not machine-shaped, so the two
  writers cannot disagree; conversely an operator who never sets the field
  keeps the `<nym>-<code>` the setup minted, which is exactly the value the
  adoption order in #605 chose for an already-deployed router. An operator
  who never opens these settings must not be migrated onto a shared default,
  and an upgrade must never write an empty string over a live network — a
  router with an empty SSID has no management path at all. So the migration
  fills defaults **only** into the two enum-valued fields.
- **`private_encryption` ships with a value (`psk2+ccmp`) rather than empty**,
  because the module has always owned that one: `99-tollgate-setup` writes the
  literal on every full setup pass (`:1983`, `:1996` — re-checked 2026-10-04),
  so there is no operator value to preserve and the declared default is
  exactly what the router already has (the applier sees no drift and does
  nothing). The consequence, stated rather than implied: a mode changed by
  hand in `/etc/config/wireless` is reverted by the applier, which is
  stricter than today only between two full setup passes.

The enum is `psk2+ccmp` (default), `psk2+tkip+ccmp`, `psk-mixed+ccmp` — all
servable by the wpad the image installs. **SAE/WPA3 is deliberately absent**:
the shipped `wpad-basic-*` has no SAE support, so offering it would produce a
private network that does not come up, i.e. a lockout presented as a feature.
An open (`none`) mode is absent for a stronger reason: the private SSID is the
management network, and the board's login on `:8090` is a root-capable login
over cleartext HTTP (`31-*.nft:9-17`), so "encryption: none" would publish it.

**D10 — `admin_access` selects which network may reach the administration
surfaces.** One flat field, four values, spelled as the operator asked:

| `admin_access` | Administration ports (`:8090/:8443`, `:8080/:443`) |
|---|---|
| `both` (default) | reachable from `br-private` and `br-mgmt`; no rule is written |
| `br-private` | reachable from the private bridge only; `br-mgmt` is dropped |
| `br-mgmt` | reachable from the wired management bridge only; `br-private` is dropped |
| `loopback-only` | reachable from `lo` only; every other interface is dropped |

> Re-derived for #625 (2026-10-04): `br-private` is no longer only the private
> SSID — since the wired LAN ports moved onto it (#625, the minimal release
> path that superseded #607), it is the private SSID **and the physical cable**,
> i.e. every administration path the shipped stack has. `both` therefore means
> "the private SSID plus the wired ports plus loopback, exactly where the
> guards already allow" — still no rule written, still a no-op on upgrade. The
> `br-mgmt` column describes #601's future bridge; no shipped image has one,
> which is why the refusal below exists.

The value is enforced by **one generated fragment**,
`/etc/nftables.d/34-admin-access-scope.nft`, written by the same applier. It
is generated and never shipped: the package owns no file at that path, so an
`apk upgrade` cannot restore a stale scope, and the file's *absence* means
exactly "the default scope, which adds no rule". The number is **34**, not
33, because open #601 ships a static `33-mgmt-bridge-scope.nft`; whoever
lands second takes the later number, and this PR is the one that could
choose (the review note that asked for the coordination).

Four properties of this design are what the reviewer should check first:

1. **The default changes nothing on the wire.** `both` renders nothing and
   removes a fragment left by a previous non-default value, so shipping this
   feature is a no-op until an operator asks for something else. It is also
   why `both` — not `br-mgmt` — is the default: `br-mgmt` would drop
   `br-private` — since #625 the private SSID *and* the cable — and (on a
   `sysupgrade -n` that regenerates the port list, the residual case) that is
   every management path gone at once.
2. **It removes reach, never grants it.** The rules are `drop`s on a hook-input
   chain at priority `-1` — the same seam and priority `31-*.nft`/`32-*.nft`
   use. A drop in an early base chain is not undone by a later accept, so the
   setting is honoured even where D4's `br-mgmt` allow list accepts the same
   ports. Nothing in the fragment is an `accept`, and nothing widens the
   implicit accept side (fw4's zone input policies and loopback), so no value
   of this setting can open a surface that is closed today.
3. **The captive bridge is not in the fragment at all.** `br-lan` (or whatever
   the captive bridge is named) loses the admin ports unconditionally, by
   invariant 2, in `31-*.nft`/`32-*.nft`. Listing it here as well would
   double-count every dropped packet in the operator's diagnostics and would
   suggest the setting governs the guest block. It does not: **no value of
   `admin_access` can make the guest bridge reach the board**, and
   `planAdminScope` has no `br-lan` case at all.
4. **`br-mgmt` is refused while that bridge does not exist.** Naming it drops
   `br-private`, which since #625 is the private SSID **and the physical LAN
   ports** — i.e. every administration path the shipped stack has; before #625
   the same argument ran through "the cable is on the bridge invariant 2
   drops", and a `sysupgrade -n` that regenerates the port list is the one
   residual case where that older phrasing still applies. The applier
   therefore refuses, leaves the scope in force untouched, and says why;
   config.json still records what the operator asked for, so the setting
   takes effect as soon as the bridge exists. This is the repo's own lesson
   from the `redirect_https` lockout, applied before the fact rather than
   after.

`loopback-only` is the one value expressed as an exception
(`iifname != "lo"`) rather than as a list of names, because it is the one value
that has to cover interfaces the module has never heard of. It is also the one
value that tightens beyond the bridges this repo creates — an operator
administering over a VPN, a `wwan` uplink or a container bridge loses the admin
ports there too. That is what "loopback-only" means; the field's description
says so.

**D11 — The private passphrase is write-only, and the settings API sits behind
the board's session.** Three invariants, each of which had to be built rather
than asserted:

- **No read path returns the passphrase.** The schema marks `private_key`
  `secret: true`, and `config get` — the payload the board's Settings page
  renders and merges into a wholesale save — blanks it and reports
  `secret_set.private_key` instead, so the UI can say "set" without being told
  what. `config set` does not echo it back either (`Set private_key (value
  withheld)`). A read that cannot be trusted with the value is not new here:
  `tollgate network private status` has always printed it to a root console
  over the `0660` control socket, and that console stays as it was.
- **A wholesale save cannot erase it.** `config save` replaces the whole file,
  and the board's payload is *by construction* one with the secret blanked —
  so an empty incoming value means "unchanged" for every operator setting
  whose empty value already means "keep what the router has". Without that
  rule the first unrelated edit made from the board (say, a mint's price)
  would silently clear the WPA key of the management network. It is tested,
  with the exact payload shape the board sends.
- **The API is post-authentication.** Both settings are carried by
  `config_set`/`config_save`, which sit in the `tollgate` ACL group behind a
  board session — the `unauthenticated` group still grants exactly
  `["auth_status"]`. Nothing about the passphrase is exposed to a pre-auth
  caller, and the passphrase is never a parameter of any pre-auth route. The
  rpcd method map does not change.

One consequence of the applier living in the daemon: `tollgate network private
rename|set-password|set-encryption` now also records the value in config.json.
Without that, the two writers would disagree and the applier would put the old
value back at the next daemon start — the CLI's own change reverting itself is
worse than either writer alone. The three commands also now write **both**
private radios through one helper and refuse to write a section that does not
exist (`uci set` on a missing section creates a typeless one), which closes the
radio-drift the settings-surface map recorded.

**D12 — What this does not deliver.** Stated so it is not read as covered:

- **Nothing here sells internet on `br-mgmt`.** D8 stands: the wired bridge is
  a management bridge. F1/F2 remain the only paths to a paywalled wired
  network.
- **The nodogsplash pre-auth allow list is not touched.** The module's
  `assert_nodogsplash_allow_entries` still removes `:8090/:8443/:8080/:443`
  from `users_to_router`, and the feed's `92-tollgate-admin-setup` still adds
  `allow tcp port 8090|8443` on install — a contradiction this change does not
  resolve, because the nft guards are the layer that does not depend on the
  list. Making the two writers agree is its own change with its own blast
  radius (three writers, two repos).
- **The board's WiFi page is repointed in the portal repo**, not here: it edits
  one `wifi-iface` section at a time by raw UCI, so editing a private radio
  there would be reverted by the applier. That page and the new Settings
  surface land together in the portal PR.
- **The feed re-vendors the portal bundle** only after the portal change is
  merged: a portal-source change ships nothing until the staged bundle is
  rebuilt from the merged commit (the pin-vs-shipped-bytes rule).

### The half that cannot work as specified

**Scope, amended 2026-09-28.** This section is about a **second *gated***
bridge — a network that must be paywalled and carry its own pre-auth policy at
the same time. It is not about the operator's requirement, which needs no
second gate and *is* satisfiable by a configuration change; see the amendment
at the end of this record. Everything below stands as written for the
two-gated-bridge case.

**The stack cannot gate two bridges at once, so "admin surfaces pre-auth *and*
still captured by the gate and paywall on the same bridge" cannot be delivered
by configuration.** One nodogsplash process manages one interface
(`src/conf.h:146`), its iptables chains are fixed, shared names in one network
namespace (`src/fw_iptables.h:35-44`) and its teardown deletes them by name
(`src/fw_iptables.c:689-747`); this module drives exactly one control socket
(`valve.go:96-102`). Two instances are startable (procd, one per uci section)
and unsafe: either one restarting removes the other's enforcement, and the
money path can only ever talk to one of them.

Since one gate gates one L2 domain, and the pre-auth allow list
(`users_to_router`, `99-tollgate-setup:1370-1457`) is a property of the gate,
**the pre-auth policy of a captive network and its bridge are the same thing**.
Two different pre-auth policies therefore need two gates, and the shipped stack
has one. Every design that pretends otherwise is either (a) a second instance
(rejected, A1), (b) a per-client discriminator on the one gated bridge
(rejected for the shipped case, A2 — it reintroduces the guest hole for a MAC
spoofer), or (c) not gating the second bridge at all (this proposal).

What follows from that, stated for the operator rather than implied:

- **what he gets now**: his cable reaches `:8080`/`:443` and `:8090`/`:8443`
  before paying anything, on a bridge no stranger shares, and the open guest
  SSID is unchanged — same guards, same paywall, same portal;
- **what he does not get now**: a wired *customer* network. A wired client
  cannot buy internet on `br-mgmt`, and D5 stops it from trying. Internet is
  bought on the gated wireless network, as today;
- **what it would take** to have both, as two follow-ups, neither of which is a
  configuration change:
  - **F1 (preferred): per-instance isolation upstream.** Make nodogsplash's
    chain names and destroy path per instance (suffix the `nds*` names by pid or
    interface — `fw_iptables.h:35-44`,
    `fw_iptables.c:442-446,534-538,689-747`), give
    each instance its own `ndsctlsocket` (already a per-section uci option,
    init script `:141-147`), and teach `valve.go:96-102` to select the socket of
    the bridge the client is on (the bridge is knowable: `/proc/net/arp` carries
    the device, `main.go:887-895`). That is a nodogsplash change plus a small
    module change, and it keeps **one** enforcement authority.
  - **F2: a second, module-owned gate.** The nft-sets/`policy drop` design
    already specified for host mode (a `forward` chain that owns its drop
    policy, per-MAC accept sets, counters) extended to a second bridge, with
    `valve` growing a second backend. Bigger, and it creates a second way to be
    free — a second enforcement implementation that can disagree with the first.

  Only after F1 or F2 may `br-mgmt` be a captive network, and only then may
  `:2050/:2051/:2121` (`30-backend-firewall.nft:21-22`) be opened to it.

**A second bridge being cheaper than a second gate is the whole point.** D1
alone removes the lockout and the L2 exposure; it does not sell anything.

## Invariants

1. **One gated bridge.** Exactly one `config nodogsplash` section exists, and
   its `gatewayinterface` is the captive bridge the guest APs are bound to.
2. **No admin surface is guest-reachable.** `:8080/:443` and `:8090/:8443`
   remain dropped on the captive bridge for both families, for authenticated and
   unauthenticated clients alike — i.e. `31-*.nft:52-53` and `32-*.nft:54-55`
   are unchanged, and no shipped writer adds those ports to
   `users_to_router`.
3. **`br-mgmt` has no path off the router.** No forwarding to `wan` from the
   `mgmt` zone, no nodogsplash involvement, no marks.
4. **`br-mgmt` cannot transact.** The payment surface (`:2050`, `:2051`,
   `:2121`) is unreachable from it while the stack can gate only one bridge.
5. **Nothing about the guest path changes.** The guest APs stay on the captive
   bridge, the portal, `:80` stub and pre-auth list are untouched.
6. **No value of `admin_access` can make the captive bridge an administration
   path.** `br-lan` (the bridge the guest APs are on) is dropped by
   `31-*.nft`/`32-*.nft` unconditionally, and the generated scope fragment
   never names it — the setting's enum has no such value.
7. **The private passphrase is write-only.** No read route, no response body
   and no log line carries it; a read path reports only whether one is set.
8. **Both shipped defaults are no-ops on the wire.** `admin_access=both`
   writes no fragment and removes a stale one; `private_ssid`/`private_key`
   ship empty, which the applier treats as "keep what the router has". A
   router that upgrades onto this release and is administered exactly as
   before behaves exactly as before.

**Amended 2026-09-28 - how to read invariants 1-5 after the amendment at the
end of this record.** Invariants 1-5 hold unchanged in substance. Where an
invariant names the guards' *present expression* (invariant 2 cites
`31-*.nft:52-53` and `32-*.nft:54-55` as "unchanged"), the amendment replaces
that expression — the drop is re-keyed from the bridge name to the guest VAP
interfaces, in a `bridge`-family rule — while keeping the property the
invariant states: no admin surface is guest-reachable, and neither port set is
added to `users_to_router`.

## Consequences

### Positive

- The operator's reported lockout ends, without weakening a single guard: the
  admin surfaces answer on a bridge that holds only his own devices.
- The wired port leaves the guest's L2 domain — the exposure
  `99-tollgate-setup:1228-1247` recorded as unfixable by bridge-port isolation,
  using the mechanism that comment itself named.
- The customer path is untouched: the portal, the pre-auth list, the paywall of
  the wireless network and the two guards keep their present behaviour, so none
  of the pre18 release's measured assertions move.
- The change is small and reversible: new UCI sections plus one uci-defaults
  writer and one nft fragment, no Go changes, no portal changes.

### Costs and what the operator gives up

- **A wired client has no internet at all**, only the administration surfaces.
  This is a deliberate consequence of "one gate", not an oversight: it is what
  makes D5 honest. If the operator wants a wired network that sells internet,
  that is F1/F2, not a wider allow list.
- **The cutover is a moment of no-cable-access.** The writer runs from the full
  setup path; until `br-mgmt` is up the wired client has nothing. The private
  SSID and the module CLI remain the way back in
  (`31-*.nft:38-42`, `32-*.nft:32-40` name those two paths).
- **The port list is image-owned** (Context). A `sysupgrade -n` or a factory
  reset regenerates it from `02_network:/bin/config_generate`, putting the wired
  port back on `br-lan` until a full setup runs again. The same-version install
  path must re-assert the move for that reason — the writer is idempotent by
  construction, like every other writer in this script.
- **No payment on `br-mgmt` means no test surface there either**: the operator
  cannot reach the portal from the cable to test a purchase. Testing stays on
  the wireless path.
- The later, separately carded feature — a config field that toggles the LAN
  port between `br-mgmt` / `br-private` / the public-guest bridge — is out of
  scope here; D1's writer is written as one place that computes "which bridge
  the wired ports are on", so that field becomes a parameter rather than a
  second implementation. (D10 adds a *different* field, `admin_access`, which
  decides which network may reach the administration surfaces. It does not
  move a port: it only removes reach from the bridges it does not name, with
  `both` as the default so that the wired bridge D1 creates keeps the
  administration reach D4 gives it.)

## Alternatives rejected, and why

### A1 — A second nodogsplash instance for `br-mgmt`

Rejected: **it does not isolate, and its failure mode differs by bridge —
fail-closed on `br-lan`, fail-open on the second one.** The packaging supports
it (one procd instance per `config nodogsplash` section, its own generated
config file, per-section `gatewayinterface`/`ndsctlsocket`/`fw_mark_*`), which
is exactly what makes it attractive — but the daemon has fixed, shared chain
names (`src/fw_iptables.h:35-44`), creates them with `-N` in both tables
(`src/fw_iptables.c:442-446` mangle, `:534-538` filter) and **deletes them by
name** on teardown (`:689-747`), on the start path as well as on shutdown
(`src/main.c:297-305`). Either instance starting or restarting wipes the other's
enforcement, and nothing in either process notices: on `br-lan` every unmarked
client is rejected (Context), and the second bridge is as open as its own zone
leaves it, because nothing in the shipped rules matches it. Restarts are
routine: procd respawn, the fw4 hotplug, and this module's own
`/etc/init.d/nodogsplash restart` (`src/cmd/tollgate-cli/main.go:705-776`).
On top of that the money path is single-socket by construction (`valve.go:96-102`
with no `-s`), so the second instance can never be authorised by `valve` — a
second gate that cannot be opened or closed correctly, and that can silently
stop metering the first one. This is the alternative that "meets the
requirement", and it is the reason this record says the requirement is not
satisfiable as configuration.

### A2 — Keep one bridge, allow the admin ports for the operator's MAC

The cheapest path that functionally satisfies "admin surfaces before paying,
guest still blocked, paywall intact": leave the wired port on the captive bridge,
and add an `ether saddr <operator MAC>` `accept` for `{443, 8080, 8090, 8443}`
ahead of the two guards in `31-*.nft`/`32-*.nft`.

Rejected as the mechanism, and it is worth being precise about how it fails:

- **It re-opens the guest hole to anyone who can spoof.** The guard exists
  because "a customer-facing network must never answer a browser with the
  router's administration login" (`32-*.nft:16-23`), and the surface behind it
  is a root-capable login over cleartext HTTP with rpcd's session endpoint on the
  same origin (`31-*.nft:9-17`). This repo has already measured the premise that
  kills a MAC allow: an authorised MAC "is still harvestable from 802.11
  headers" (`99-tollgate-setup:1224-1225`). A stranger on the open SSID sets that
  MAC and the drop stops applying — the hole the guards exist to close, opened
  by configuration, with a comment claiming otherwise.
- **No L2 separation.** The operator's administration laptop keeps sharing a
  broadcast domain with strangers, which is the second thing the bridge fixes.
- **It is device-bound.** Privacy MAC rotation, a replacement laptop or a second
  admin device each need a new allow entry, and each stale entry is a standing
  exemption.
- It is nonetheless a **valid interim** if the operator needs admin reach from a
  device that cannot be moved to `br-mgmt` — recorded here so it is not
  rediscovered as a "fix", with the spoofing caveat attached.

### A3 — Leave everything as it is and re-add `:8080/:443/:8090/:8443` to the pre-auth list

Rejected: that is the defect `32-*.nft` was written to fix, measured on the
bench on 2026-09-25 (a paying guest typing `http://<router>/` landed on the LuCI
login). The superseded reachability ADR
(`docs/architecture/luci-https-pre-auth-reachability-decision.md`) documents the
reversal; this record does not undo it.

### A4 — Make remote administration safe instead of bridge-scoping it

Rejected for this decision (and not evaluated on its merits): reaching `:8090`
over cleartext HTTP with a root-capable login behind it is a transport question,
`31-*.nft:9-17` gives its own reasoning for the guard, and reversing it is a
different decision with a different owner. It is noted only so the maintainer can
see it was considered.

### A5 — Leave the wired port on `br-lan` and move the guest SSIDs instead

Rejected: it trades a solved problem for an unsolved one. Whichever bridge the
guests are on is the bridge that needs the guards, the paywall and the portal;
moving them changes the bridge name in a dozen files and the wired port is still
in the same L2 domain as the guests unless it is separated — which is D1.

## What must be true before this ships

Every item is an assertion someone can run; "measured on the bench" is not
evidence for an offline test, and an offline test is not evidence for the
hardware. Both tiers are listed.

**Offline (fake `uci`, no router, no network — the repo's existing tier, e.g.
`tests/uci-defaults-private-subnet_test.sh`, `tests/packaging/luci-not-guest-reachable_test.sh`)**

1. `setup_mgmt_bridge` creates `network.mgmt`, `network.mgmt_bridge`,
   `dhcp.mgmt` and `firewall.mgmt_zone`, and **moves** the port list: after a
   run on a fixture whose `br-lan` device lists `eth1`, `br-lan` lists none and
   `br-mgmt` lists `eth1`. A fixture with an empty `br-lan` port list changes
   nothing and fails loudly. *Not runnable against the existing offline tier as
   it stands*: the shim in `tests/uci-defaults-private-subnet_test.sh:74`
   answers `show` with `:` (a silent no-op), while the repo's own idiom for
   enumerating `network` sections is `uci show network | awk`
   (`99-tollgate-setup:1731`) — a writer using it would see no ports and take
   the fail-loudly branch on every fixture. The implementing PR must teach the
   shim `show`/`get`, or seed flat `network.@device[N].ports` keys in the
   fixture, and prove the negative in the same change (assertion 2).
2. The move is idempotent: a second run adds nothing and duplicates nothing
   (the repo's `uci` shim trap applies — an unhandled verb must not pass
   vacuously).
3. No shipped file gives any uci section other than
   `nodogsplash.@nodogsplash[0]` a `gatewayinterface`; exactly one
   `config nodogsplash` is created on every setup path, and it is `br-lan`.
4. `31-*.nft` and `32-*.nft` still drop their ports on `br-lan` only (protocol
   coverage and both families), and **no** shipped fragment names `br-mgmt` in a
   `drop`/`reject` for those ports.
5. The new `br-mgmt` fragment: `nft -c -f` passes against a `table inet
   fw4 {}` wrapper, with a negative control (a fragment missing the drop must
   fail the check); its allow list contains `443, 8080, 8090, 8443` and its drop
   covers `2050`, `2051` and `2121`. *This harness does not exist in the tree
   today* — `grep -rn "nft -c\|table inet fw4 {"` matches nothing outside this
   record — so the implementing PR must add it (a wrapper file plus the
   `nft -c -f` invocation). The existing fragment tier validates statically
   instead: it greps for the expected rules, checks the fragment opens no
   `table` of its own, and checks it reuses no chain name
   (`tests/packaging/luci-not-guest-reachable_test.sh:90-105`). Adding the
   parser-backed check is this assertion's actual work, not an assumption.
6. `firewall.mgmt_zone` has no forwarding to `wan`, and the writer does not
   reuse `firewall.private_zone`.
7. The same-version (reinstall/upgrade) path reaches the same writer, so a
   router that already has `/etc/tollgate-setup-done` at the current version
   still moves the ports (`tests/uci-defaults-same-version-allowlist_test.sh`
   shows the tier).
8. `make go-battery` from the repo root is unaffected (no Go changes).

**Bench, after the install (GL-MT3000, one owner, artifact identity read back
per the repo's deploy rules)**

9. **The port really moved.** `uci show network | grep -A3 mgmt_bridge` lists
   the wired port; `ip -d link show <wired port>` shows `master br-mgmt` and
   **not** `master br-lan`; `ip -d link show br-lan` does not list it.
10. **Admin surfaces answer from the cable, pre-auth.** From the wired client,
    before any purchase: `:8090` answers (board), `:8080` answers (LuCI) and
    `:443` completes a TLS handshake. `:8443` answers **only when the opt-in
    listener exists** — it is written by the feed's `92-tollgate-admin-setup` and
    this script only clears it (`99-tollgate-setup:1055-1059`), so on a router with
    no TLS identity there is no listener and a correct build must not be failed
    for its absence. `:22` answers **only if dropbear listens on the `mgmt`
    network**: nothing in `99-tollgate-setup` configures SSH, so that dependency
    has to be named rather than assumed.
11. **The cable has no internet.** From the wired client: `ping 9.9.9.9` fails,
    a TCP connection to a WAN address fails, and the fragment's drop counter
    increments (`nft list chain inet fw4 <mgmt chain>`).
12. **The cable cannot buy.** `:2121`, `:2050`, `:2051` are refused/dropped from
    the wired client (so no Lightning quote and no token can be posted), and no
    `PAID`/`access_granted` event happens for that MAC.
13. **The guest is unchanged.** From a client on the open guest SSID with a MAC
    the router has never seen: `:8090`, `:8443`, `:8080`, `:443` are dropped;
    `:2050`/`:2051` answer; the portal completes a purchase end to end and the
    gate opens (i.e. the paywall still works exactly as on pre18).
14. **One gate.** `uci show nodogsplash` shows one section;
    `pgrep -fc nodogsplash` is 1; `ls -l /tmp/ndsctl*` shows one socket.
15. **`br-mgmt` clients still resolve.** `logread -e dnsmasq` shows a lease for
    the wired client on the `mgmt` network (`getMacAddress`,
    `src/main.go:864-898`); this is what keeps a future F1 implementation
    possible without redesigning identity.
16. **The module still administers the gate from a full round trip**: status,
    `whoami`, a purchase and a session close on the *wireless* path behave as
    they do on pre18 — the wired change must not have touched the money path.
17. **Recovery**: with the cable moved, the private SSID and the module CLI
    still reach the box (the two documented recovery paths), and re-running the
    full setup converges without duplicating sections.

`docs/rc-tester-guide.md` §7 must be updated in the same change: it currently
tells a tester that `tcp 8090/8443` are dropped for "a wired-LAN or guest-SSID
client" and that this is the guard, not TLS. After this change that is true of a
**guest-SSID** client and of a `br-lan` client, and false of the wired client,
which now reaches the board and LuCI and has no internet.

**For D9-D12 (the operator settings).** 18-22 are offline and must be runnable
with no router, which is where they are: the scope plan and the fragment
renderer are pure functions with two injectable seams (`ifacePresent`,
`nftablesDir`), and the secret contract is exercised through the real config
manager in a temp dir.

18. **The default is a no-op on the wire.** `admin_access=both` renders the
    empty fragment, writes no file, and removes one left by a previous value;
    a second run reports `unchanged` (`TestRenderAdminScopeFragmentDefaultIsEmpty`,
    `TestApplyAdminAccessWritesThenConverges`).
19. **Every non-default scope renders rules that only drop, at the guards'
    own seam** — one rule per address family, `hook input priority -1`,
    `policy accept`, the four admin ports, and **no `br-lan` anywhere in a
    rule** (`TestRenderAdminScopeFragmentShape`, `TestRenderAdminScopeFragmentLoopback`).
20. **`br-mgmt` is refused while the bridge does not exist, and a refusal
    changes nothing** — the fragment in force is byte-identical afterwards
    (`TestPlanAdminScope`, `TestApplyAdminAccessRefusalLeavesTheRouterAlone`).
    The negative control is the same test with the interface present, which
    must be accepted.
21. **The passphrase never comes back.** `config get` returns a `*Config`
    whose `private_key` is empty plus `secret_set.private_key=true`;
    `config set private_key` does not echo the value in its message or its
    data; a wholesale `config save` of the exact payload the board sends
    (secret blanked) preserves the stored passphrase and still applies the
    unrelated edit in the same payload
    (`TestHandleConfigSetWithholdsSecretValue`,
    `TestHandleConfigSavePreservesStoredSecret`, `TestRedactSecretFields`).
22. **The config chain still gates, both ways.** The four new keys are in the
    schema, flat and editable, with the enum the ADR states; the schema/struct
    drift tests, `defaults_parity_test.go` and `tests/contract/js-schema-lint.mjs`
    pass; an unknown `admin_access` or `private_encryption` is refused on the
    `config set` path **and** on the wholesale `config save` path, which
    bypasses per-key validation (`TestSchemaOperatorNetworkFields`,
    `TestValidateValueRejectsUnknownEnums`, `TestHandleConfigSaveValidatesOperatorEnums`).

**Bench, for D9-D12** (same box, same single-owner rules; carried by the same
card that measures 9-17, and required before the settings are documented as
working rather than as designed):

23. **Setting the scope from the board moves the wire, and unsetting it moves
    it back.** Re-derived 2026-10-04 for #625 — the wired client is on
    `br-private` now, not `br-mgmt`, so the moving leg must use a value that
    drops it: with `admin_access=loopback-only`, `nft list chain inet fw4
    admin_access_scope` shows the `iifname != "lo"` drop and a wired client
    cannot reach `:8090` (nor can a private-SSID client); back to `both`, the
    chain file and the chain are gone and the wired client reaches it again.
    (With `admin_access=br-private` the wired client *keeps* `:8090`: the
    drop names `br-mgmt`, which no shipped image has. The pre-#625 reading —
    "a wired client cannot reach `:8090`" under `br-private` — described
    #601's world and is no longer measurable on main.)
24. **The board reaches the surface it is configuring.** A change made from
    the board's Settings page survives a reboot and is still in effect
    (i.e. the applier at daemon start agrees with what the board wrote).
25. **The passphrase round-trips without ever being read.** Setting a new
    private passphrase from the board associates a client with it on the
    private SSID, and the value appears in no response the board receives.
26. **The guest is unaffected.** From a MAC the router has never seen on the
    open SSID, `:8090`/`:8443`/`:8080`/`:443` are dropped and the portal still
    sells, for every value of `admin_access`.

## Rollout / PR sequence

1. **This document** (docs-only): the decision, the evidence, the rejected
   alternatives, and the assertion list.
2. **The bridge and its scope** (one PR): the `setup_mgmt_bridge` writer plus the
   `br-mgmt` fragment, the pinning tests, the `docs/rc-tester-guide.md` §7
   correction, and a `CHANGELOG.md` entry under `Changed / Internal`.
3. **Bench measurement, then the release cut.** The pre18 assertions must be
   re-measured on the build that carries this change before any feed bump
   (assertions 9-17 above), because two of them — 11 and 12 — are the difference
   between "the operator can administer his router" and "a stranger has free
   internet".
4. **Then, separately, F1 or F2** if a *paywalled* wired network is still
   wanted. That decision is not made here, and `br-mgmt` must not be described
   as a customer network until it lands.
5. **The two operator settings (D9-D12), module half** (one PR): the four
   `config.json` fields with their schema, the applier
   (`src/cli/operator_settings.go`), the redaction and preserve-on-save rules,
   the `config apply` and `network private set-encryption` verbs, the daemon
   start-path convergence, this document's D9-D12 section, and the offline
   tests 18-22. The shipped defaults are no-ops, so this PR can land before
   anything depends on it.
6. **The board half** (portal repo, one PR): the Settings surface for all four
   fields (the secret rendered as a write-only input that says "set", the two
   enums as selects), and the WiFi page's private-radio edit repointed from raw
   UCI to `config_set` so the two writers cannot disagree.
7. **Bench measurement (23-26), then the release cut**: the feed re-vendors the
   portal bundle from the merged portal commit and re-pins the module, and the
   settings are exercised on the box before they are documented as working.

## Notes

- **Provenance of the external evidence.** The daemon citations are the
  nodogsplash `v5.0.2` tag (`src/conf.h`, `src/conf.c`, `src/fw_iptables.h`,
  `src/fw_iptables.c`, `src/ndsctl.c`, `src/ndsctl_thread.c`) — the version this
  repo names in its own comments (`20-nds-enforce.nft:3`, `CHANGELOG.md`) and
  the version `openwrt/packages` `net/nodogsplash/Makefile` builds
  (`PKG_VERSION:=5.0.2`). The OpenWrt-init-script citations are that package's
  `files/etc/init.d/nodogsplash`, which on 2026-09-26 exists **only on the
  packages feed's `master`** (`net/nodogsplash` is absent from
  `openwrt-25.12` and `openwrt-24.10`, verified by tree listing), so a router
  running it got it from `master` or from a vendored copy. Nothing in this
  record depends on a version newer than the one shipped: the single-interface,
  fixed-chain, single-socket facts above are all v5.0.2 facts.
- **Why this is an ADR and not a config change.** The repo's rule is decision
  first (`docs/architecture/`), and this one has a fact in it the requester did
  not have: a **second *gated* bridge** cannot be delivered by the stack as
  built, and the amendment at the end of this record narrows the record to
  exactly that. The right time to learn that is before the change, not from a
  router whose gate has been torn down on one bridge and left absent on the
  other.
- **What is unchanged, deliberately**: `users_to_router` (`99-tollgate-setup:1370-1457`),
  the `:8080`/`:443` removal logic, the `:80` trusted stub
  (`setup_uhttpd_trusted_entry`), `20-nds-enforce.nft`'s mark values
  (`0x10000`/`0x20000`/`0x30000`) and its `br-lan` scope, the two guards, and
  `firewall.tollgate_in`'s `:2121` rule. A reader looking for a diff in those
  files should find none.
- **The `:2121` ACL is the marker of a paywalled network.**
  `30-backend-firewall.nft:21-22` allows payments only from `br-lan` and `lo`.
  Whoever opens F1/F2 must add their new bridge to that set in the same commit,
  with the guard that a bridge added there is a bridge that sells.
- **The one-line summary for a maintainer**: the wired ports can have a bridge
  of their own, that bridge can reach the administration surfaces before
  paying, and it cannot also be a network that sells internet — not with one
  nodogsplash, and not with two instances on one router.

## Amendment - the requirement is satisfiable by a configuration change (2026-09-28)

**Update 2026-09-28 (operator-approved design; a docs-only amendment to this
record).** The verdict above is **narrowed, not withdrawn**. "…the request it
was written for is not fully satisfiable by a configuration change on the
shipped stack" remains **correct as applied to a second *gated* bridge**, and
everything this record says about that case stands unchanged in substance: the
one-interface nodogsplash (`src/conf.h:146`), the fixed `nds*` chain names in
one network namespace, the name-scoped `iptables_fw_destroy()` on the start
path, the procd respawn loop, alternatives A1/A2/A3 and the F1/F2 follow-ups.
What was wrong was **extending that verdict to the operator's requirement**.

The requirement recorded in Context — **a wired-port client must still be
required to PAY for WAN access, and must be able to reach LuCI
(`:8080`/`:443`) and the admin/config UI (`:8090`/`:8443`)** — *is* satisfiable
by a configuration change on the shipped stack: no `br-mgmt`, no second
bridge, no second nodogsplash instance, no Go change. What follows is the
mechanism, the measured constraint that shapes it, and what it does and does
not fix.

**AM-1 - key the two admin-port drops on the guest VAP interfaces, not on the
bridge.** Both guards drop on `iifname "br-lan"` today —
`31-*.nft:52-53` for `{8090, 8443}`, `32-*.nft:54-55` for `{8080, 443}`, both
families, counters kept. The wired port is a member of `br-lan` (see "Where the
wired LAN ports are bound, and therefore who may move them"), so the
operator's own cable matches those rules: that is the reported lockout.
Re-keying the drop to the **guest VAP interfaces** changes the *match
expression* and nothing else:

- the wired port (`eth1` on this hardware, or whatever port the base image puts
  on `br-lan`) stops matching, so `:8090`/`:8443` and `:8080`/`:443` become
  reachable from it, pre-auth;
- the wireless guests keep matching — the guest BSSes stay bound to the captive
  bridge (`99-tollgate-setup:1268` sets `wireless.<iface>.network='lan'`), so
  both guards keep dropping them, and the #566/#588 behaviour together with its
  pre18 bench measurement is unchanged;
- **the wired port stays a member of the gated `br-lan`**
  (`99-tollgate-setup:1476` writes
  `nodogsplash.@nodogsplash[0].gatewayinterface='br-lan'`), so
  `20-nds-enforce.nft:33` and nodogsplash's marks still apply to it: it is
  still redirected by the portal, still has to pay, and gets WAN only after
  payment. The commerce path, the portal and the pre-auth list are untouched.

This is the mechanism this repo already names in its own words — "a dedicated
br-guest with its own reject-by-default zone, or a **bridge-family nft rule
keyed on the VAP ports**" (`99-tollgate-setup:1246-1247`). It is also why
`br-mgmt` is not needed *for the requirement*: the guard was over-broad, not
the bridge.

**AM-2 - the re-key is a port-keyed rule, not a string swap (measured).**
`iifname` does not name the same device in both nftables families, and the
shipped guards live in fw4's `inet` table. Measured in a network namespace on a
Linux host (kernel `7.0.0-34-generic`, `nftables v1.1.6`, `br_netfilter`
loaded with `bridge-nf-call-{iptables,ip6tables,arptables}=1`, a bridge `br0`
with one port and traffic delivered to the bridge's own IP address):

- in an **`inet`**-family `input` hook, `iifname` is the **bridge**: a rule
  keyed on the bridge name matched every packet (2/2), and the same rule keyed
  on the bridge *port* matched **0**;
- in a **`bridge`**-family `input` hook, `iifname` is the **bridge port**: the
  bridge name matched 0, the port name matched all of them, and a
  port-keyed `drop` there blocked the traffic (drop counter incremented, ping
  failed).

Consequences, stated plainly because they invert the naive reading:

- inside the existing `inet` fragments, `iifname "br-lan"` is what matches
  *every* bridged client, wired and wireless alike — not a defect, just the
  expression doing what its name says;
- substituting the VAP names into `31-*.nft`/`32-*.nft` as they stand would
  match **nothing**: both guards would become silent no-ops and the *guests*
  would reach the board and LuCI. That is a worse state than the lockout, and
  it fails open with no error anywhere;
- the re-key therefore moves the drops to a **`bridge`-family, port-keyed**
  rule, which is where the port name is visible. Same policy, same ports, both
  families, counters kept — a different key, in the family that can express it.

This measurement settles the match semantics of the mechanism, not the shipped
rule: the bench pass is still owed (wired client reaches `:8090`/`:8443` and
`:8080`/`:443` pre-auth **and** is still redirected and pays; a wireless guest
is still dropped; it survives `fw4 reload`, reboot and sysupgrade), exactly as
the assertion list above requires.

**AM-3 - the interface set is derived, and the rule must fail closed.** The VAP
names are not stable: `phyN-apM` follows radio/PHY enumeration and can change
across firmware or hardware, so a guard keyed on four hardcoded names can stop
matching after an upgrade and become a no-op with no error anywhere. The set is
therefore derived at `fw4 reload` from the module's own source of truth — the
wireless interfaces bound to `network='lan'` (`99-tollgate-setup:1268`) — and
an empty or failed enumeration **falls back to the blanket `br-lan` drop with a
log line**, i.e. to today's behaviour (safe, still gated) rather than to no
drop at all. The derivation, the fallback, its negative control and the
boot-time assertion are tracked and pinned by t_8590499a below; this record
requires that they exist, because they are what keeps AM-1 from decaying into a
silent hole.

**AM-4 - `br-mgmt` REMAINS PROPOSED, on its own separate merit.** Nothing here
withdraws D1-D8 or the bridge proposal, and re-keying the guards does **not**
close the exposure this record exists for: the wired port keeps sharing one
broadcast domain with strangers — ARP, mDNS, SSDP, broadcast — and it keeps
doing so on the *same* bridge as the open guest SSID. AM-1 narrows *who may
reach the administration surfaces*; it does not separate *who may see whom*.
That separation is what D1 buys, and it is why the two changes are independent:
AM-1 is about the guard's scope, D1 is about the wired port's L2 domain. The
paywall half of a **second** network (D8, F1/F2) is exactly as unreachable as
before — a wired *customer* network still needs a second gate, while under AM-1
a wired client pays on the same gate the guests use.

**AM-5 - the title's "half".** Read "the half of that request the shipped stack
cannot deliver" in this record's title as *the second gated bridge*, not as the
operator's requirement. Where the body says the request cannot be satisfied by
the stack as built, it means the two-gated-bridge case; AM-1 is the
configuration change that satisfies the requirement as stated.

**AM-6 - line references re-checked at the commit this amendment branches from
(`54e8c3683e3b583298410946dc8eb804e846d93d`).** The two guard citations are
still exact (`31-*.nft:52-53`, `32-*.nft:54-55`). The citations to the
isolation comment that names the VAP-keyed alternative had **drifted**:
`99-tollgate-setup:974-993` was that comment when this record merged (#600) and
is now the admin credential gate, so both citations are corrected here to
`99-tollgate-setup:1228-1247` (the comment block that carries the measurement,
with the named mechanism at `:1246-1247`). The re-check was then extended to
every `99-tollgate-setup` citation in this record, and the drift was not
confined to that comment: nineteen further references had been written against
an older revision of the script and now pointed at unrelated content — the
allow-list assert (`:1108-1124` → `:1370-1457`, three sites), the
`gatewayinterface` write (`:1214` → `:1476`), the `:2121` zone rule
(`:1427-1441` → `:1697-1704`), the LAN-device read (`:1517-1519` →
`:1779-1781`), the `network.lan.domain`/`ip6assign` writes (`:867`/`:1463` →
`:1121`/`:1725`), the private network/bridge/dhcp creation (`:1693-1707` →
`:1956-1967`), the `uci show network | awk` idiom (`:1469` → `:1731`, two
sites), the `private → wan` forwarding (`:1734-1743` → `:2004-2006`), the
uhttpd listener citations (`:337-339,443-448` → `:591-593,697-702`;
`:364-365` → `:617-619`; `:646-648` → `:899-902`; `:689-709` → `:956-971`),
the guest-SSID `network=lan` binding (`:1014` → `:1268`), the 802.11-harvest
quote (`:969-972` → `:1224-1225`) and the `:8443` clear (`:804-805` →
`:1055-1059`). All are corrected to the ranges that carry the cited facts at
the branch commit. The external binding claim is
unchanged: `openwrt/openwrt` `openwrt-25.12`
`target/linux/mediatek/filogic/base-files/etc/board.d/02_network:158-166` still
groups `glinet,gl-mt3000` into `ucidef_set_interfaces_lan_wan eth1 eth0`, and
`/bin/config_generate:109-119` still turns that into a `br-lan` device whose
`ports` list carries the wired port.

**AM-7 - a note folded in at the D9-D12 rebase (2026-10-04).** #625 landed
on main after this amendment was written, as the **minimal release path** for
the same requirement: `setup_lan_ports_private` moves the base image's wired
port list onto `br-private` (guards untouched), superseding #607's full role
machinery for this release and leaving `br-mgmt` (D1-D8, #601) to land after
it. That is a *third* mechanism beside AM-1's re-key and D1's bridge, and it
chose differently on purpose: a cabled client is owner-class (internet, no
payment), with paying-wired scoped post-release. Consequences for this
record: the wired administration path in D10's table is `br-private` (see the
re-derived note there); the re-key of AM-1 remains what it was — a proposal
on card t_8590499a — and nothing here prefers it over what shipped.

**Tracking.** The implementing work is card **t_8590499a** on the
`tollgate-module-basic-go` board ("Re-key the admin-port guards from br-lan to
the VAP interfaces (wired client: pays + admin, guests: still dropped)"), which
carries AM-1 to AM-3 as its design and the bench verification as its definition
of done. The implementing PR is tracked by the card and is the place a landed
mechanism will be cited from; this record stays the decision. Status stays
**Proposed**: acceptance is a maintainer action and the drafting account may
not accept its own proposal.
