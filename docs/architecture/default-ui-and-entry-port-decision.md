# The Board Is the Default Face — Which UI Answers the Entry Ports, and What a Cross-Link May Be

> **Status: Proposed (2026-09-26).** This record is the *decision* the operator
> took on 2026-09-26; it ships nothing. Acceptance is a maintainer action — the
> drafting account may not accept its own proposal — and nothing here is in
> force until the mapping has landed on a router and been measured there. The
> implementation is deliberately split across two repositories and one release
> boundary, because half of it cannot be shipped safely on its own (D4, and
> [What must be true before this ships](#what-must-be-true-before-this-ships)).

## Context

### The operator's decision (2026-09-26)

The TollGate board becomes the **default face** of the router: the
hostname/entry point opens the **board**, not LuCI. LuCI stays available but
secondary, through (a) a cross-link **button** in the board UI and (b) a config
**switch** for what the hostname/ports serve. Two constraints came with it:

1. cross-links must be **HTTPS only** — never `http://<host>:8090`, "a password
   over plain http is exactly what we are removing";
2. the LuCI link must only appear **where LuCI is actually allowed**
   (`br-private` / `br-mgmt` / loopback), or it is a dead link.

The decision also has to resolve a live inconsistency: the release docs
describe the admin UI's URL as `https://<hostname>.lan/` while `:443` serves
**LuCI** (D8).

### The mapping as it ships, verified in-tree (`upstream/main @ 37c51415`)

| UI | HTTP | HTTPS | uhttpd section | webroot | written by |
|---|---|---|---|---|---|
| LuCI | `8080` | `443` — only while an identity exists | `uhttpd.main` | `/www` | module `99-tollgate-setup` (`setup_uhttpd` `:335-360`, `setup_uhttpd_tls_identity` `:428-478`) |
| TollGate board | `8090` | `8443` — only while `/etc/uhttpd.crt` exists | `uhttpd.admin` | `/www/<brand>` | portal `92-tollgate-admin-setup`, staged by `packaging/portal-build.sh:14-17` and installed by the feed |
| Guest portal | `2051` (+ the `:80` stub) | — | `uhttpd.portal`, `uhttpd.trusted` | portal SPA | module `99-tollgate-setup` |

Neither admin pair is guest-reachable: `packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft:52-53`
drops `:8090/:8443` on `br-lan`, `32-luci-not-guest-reachable.nft:54-55` drops
`:8080/:443` on `br-lan`, both families, and neither pair is in nodogsplash's
pre-auth allow list (`assert_nodogsplash_allow_entries`,
`99-tollgate-setup:1108-1198`). `br-mgmt` (the wired management bridge, PR #601)
admits the same four ports. The management path — `br-private` (the private
SSID) and loopback — is the absence of a drop, not a written allow
(`lan-port-management-bridge-decision.md`, D2/D7).

### Three defects the decision has to answer, all read from source

**1. The docs call LuCI's URL the admin UI's URL.** `docs/rc-tester-guide.md`
§7 ("The admin UI, and what its certificate warning means", `:410-447`) tells a
tester to *log in at* `https://<hostname>.lan/` (`:419`) and only then, in a
separate bullet (`:442-447`), that the board has listeners of its own on
`:8090`/`:8443`. Both statements are true and the pair is ambiguous: `:443`
answers **LuCI**, so the section's own heading sends the reader to the wrong UI.
That ambiguity is what an operator read as "the board's URL is
`https://<hostname>.lan/`" (the report behind this card), and it was introduced
by #593, whose subject was TLS identity, not which UI a URL answers.

**2. The board's existing cross-link is the thing being removed.** The board
SPA's login page renders

```jsx
href={`http://${window.location.hostname}:8080/`}   // admin/src/routes/login.tsx:336 @ portal origin/main
```

— plain HTTP, a hardcoded port, and an admin login on the far side of it. Under
this record's mapping `:8080` is the board's **own** entry port, so the anchor
would also be self-referential. It has to go, not be re-pointed.

**3. The board's HTTPS listener presents the image's placeholder identity.**
Portal `92-tollgate-admin-setup` keys the opt-in `:8443` listener off
`/etc/uhttpd.crt` + `/etc/uhttpd.key`, which is the OpenWrt image's placeholder
(`subject CN=OpenWrt`, no SAN that covers any router — the identity #593 removed
from the entry listener). The module's generator writes
`/etc/tollgate/ssl/server.{crt,key}` (`src/cmd/tollgate-cli/ssl.go:23-31`), and
`ssl.go:1143-1155` keeps `/etc/uhttpd.crt` only as a **fallback** for
`uhttpd.main`. So after #593 the entry listener has a verified identity and the
board's TLS listener still has one that covers nothing: an HTTPS-only cross-link
cannot point at `:8443` as shipped.

### Why "the entry point" is a port question — and why it forces two webroots

uhttpd resolves **one docroot per instance** (`home`), and CGI paths are
resolved *against* it: the board's `92-tollgate-admin-setup` header records the
2026-09-20 MT3000 incident, where `home=/www` on the board's port let uhttpd's
default `cgi_prefix=/cgi-bin` serve the real LuCI CGI on `:8090` (#566). LuCI's
webroot must be `/www`; the board's must not be. Two docroots, therefore two
instances — and *"which UI answers the entry point"* is exactly *"which instance
owns the entry listener pair"* (`:8080` HTTP + `:443` HTTPS + the
`redirect_https` derived from the identity, `99-tollgate-setup:428-478`).

No third mechanism exists, and each was checked before rejecting it:

- **a path prefix on one instance** — LuCI's CGI is `home`-relative, so a board
  under `/www/tollgate` and LuCI under `/www` are two docroots, which is the
  two-instance case again;
- **a redirect document at the entry** — `/www/index.html` is image-owned (it is
  LuCI's meta-refresh), so a shipped copy would be restored or deleted by the
  next `apk` transaction;
- **a hostname alias** — DNS selects an address, not a port; the board and LuCI
  are the same address already, and `:443` must answer someone.

## Decision

**D1 — The switch is a flat `config.json` field, `entry_ui ∈ {board, luci}`,
default `board`.** It is declared in the module's schema (`Editable`, `Enum`,
same `Default` in `NewDefaultConfig()`), so the board's settings page renders it
generically with no SPA code (the recipe in the operator-settings surface map),
and it is read by the uci-defaults writers with `jq` (`DEPENDS:=+jq`,
`packaging/Makefile:59`). A missing file, a missing key, or an unparseable value
resolves to **`board`**: the switch fails toward the operator's decision, and no
value may ever resolve to "bind no admin listener at all" — a router whose
switch is `read` as unusable must still answer on one of the two pairs.

The module's own rule applies and is not waived here: *a `config.json` option is
declared intent only; nothing consumes it* (surface map §0). The value becomes
behaviour only through the writers of D2/D3 plus a Go applier that writes the
listeners and reloads uhttpd (precedent: `ssl.go` + `reloadServices`), so that
`tollgate config set entry_ui board` is delivered without a reboot.

**D2 — The switch decides which UI owns the entry pair; the other owns the
secondary pair. Ports move, webroots do not.**

| `entry_ui` | entry pair (`8080` + `443` + derived redirect) | secondary pair (`8090` + `8443`) |
|---|---|---|
| `board` (default) | **board** — `uhttpd.admin`, `home=$ADMIN_HOME`, `error_page=/index.html`, `ubus_prefix=/ubus`, no `cgi_prefix`/`lua_prefix` | **LuCI** — `uhttpd.main`, `home=/www` |
| `luci` (legacy) | **LuCI** — `uhttpd.main`, `home=/www` | **board** — `uhttpd.admin`, as today |

`https://<hostname>.lan/` therefore answers the board by default, and
`http://<hostname>:8080/` is its plain-HTTP entry that redirects to `:443` while
a covering identity exists (D6's redirect rule, unchanged from #593).

Two properties make this the cheap option and not a rewrite:

- **The port *sets* do not change, only their meaning.** `31-*.nft:52-53`,
  `32-*.nft:54-55`, the `br-mgmt` scope and the pre-auth list keep their ports,
  so no guard is re-scoped and the guest path is untouched. What moves is which
  UI answers on each pair.
- **One value reverts the whole change** — `entry_ui=luci` is the shipped
  router, by construction, so a bench that misbehaves has a one-line rollback
  that does not require a downgrade.

**D3 — One writer per section; the identity follows the listener, not the
section name.** `uhttpd.admin` is written by the portal's
`92-tollgate-admin-setup` — the only writer that knows the brand webroot (the
feed substitutes `__ADMIN_HOME__` at package build time,
`packaging/portal-build.sh:37-38`) — and `uhttpd.main` by the module's `99`.
The switch decides which pair each section binds, and **both scripts compute the
mapping from the same value**, with a cross-repo single-rule test in the shape
of the portal's `packaging/tests/test-redirect-https-single-rule.sh` failing if
either half decides the mapping on its own.

The TLS side follows the listener: the section that owns `:443` gets the
provisioned identity (`/etc/tollgate/ssl/server.{crt,key}`, fallback rules of
#593) and its `redirect_https` is derived for that section by the one predicate
(`tollgate ssl covers`). That is a required change on the module side:
`uhttpdCertPath()` and `applyRedirectHTTPS()`
(`src/cmd/tollgate-cli/ssl.go:858-880`) read `uhttpd.main.cert` **by section
name** and must read *the certificate of the section that owns `:443`*, at both
the Go and the shell layer (`setup_uhttpd_tls_identity`). Without it, in `board`
mode the module would keep provisioning the **secondary** listener while the
entry served the image placeholder — the defect #593 removed, moved one port to
the left. Correspondingly, the portal's `:8443` must stop keying off
`/etc/uhttpd.crt`.

**D4 — A half-converged pair degrades to today's mapping; it never becomes a
bind fight.** One port can be bound by one instance, and the two halves are
delivered by different installers through different paths — the module's
postinst runs `90`/`99`, the feed's recipe brings `92` — while at boot the
numeric order is `90, 92, 99`. A router carrying a new `99` and a pre-switch
`92` would have both sections claiming `8090`, and the loser does not start:
an admin UI disappears. So the mode-aware `92` announces the mapping protocol
(a marker; see the offline assertions), and `99` honours `entry_ui=board` **only
when that marker is present**. Without it, `99` repairs to the `luci` mapping,
logs one WARNING naming the feed re-vendor, and leaves the operator's switch
value recorded in `config.json` for the release that carries both halves.

The rollout consequence is explicit: **the module half may ship with the
`board` default only in a release whose feed pin (`vendor.lock.json`
`portal_commit`, the feed recipe's `PKG_SOURCE_VERSION`) carries the mode-aware
`92`.** The feed is the atomicity boundary — the same shape as the `br-mgmt`
record's promotion order. A module-only release ships the switch and the
mapping machinery with the default still `luci`, which is a behaviour no-op.

**D5 — A cross-link is a router answer, HTTPS-only, or it is not rendered.**
The board renders the LuCI link only from what the router says:

```
tollgate ui links --json   ->  {"entry_ui":"board",
                                "luci":{"url":"https://<host-as-used>:8443/","reason":null}}
                            |  {"entry_ui":"board","luci":{"url":null,"reason":"no covering identity"}}
```

plus the equivalent rpcd method, so the SPA never guesses a port and never
builds a URL by string surgery. `url` is non-null only when (i) the UI it names
has a **live TLS listener** whose identity covers this router — the same
predicate and the same fail-closed direction as #593 — and (ii) that UI's port
pair is inside the admin-access scope (the sibling field, card `t_868d0fa7`).
Null means **no button**, with `reason` surfaced on the settings page and by the
CLI; a button that answers "connection refused" is the dead link this record
forbids. The existing `http://<host>:8080/` anchor is deleted in the same change
(Context, defect 2).

**D6 — Plain HTTP on an admin pair redirects while a covering identity exists.**
The derived-redirect rule is not board-specific: whichever UI owns the entry
pair derives `redirect_https` for itself exactly as `uhttpd.main` does today
(`uhttpd-redirect-https-ownership-decision.md`), and LuCI's secondary pair does
the same for its own `:8443`. An admin login is never *advertised* over plain
HTTP; where no covering identity exists there is no TLS listener to redirect to,
the listener stays HTTP (as today, `:8090` is HTTP by design) and the fix is
`tollgate ssl apply` — recorded here plainly rather than implied, because a
fresh router before provisioning would otherwise have the board's login on
cleartext at the entry, which is the property this record exists to remove from
cross-links.

**D7 — An admin port is never in nodogsplash's pre-auth allow list, in either
mode.** Portal `92-tollgate-admin-setup` still does

```sh
for port in 8090 8443; do uci add_list nodogsplash.@nodogsplash[0].users_to_router="allow tcp port $port"; done
```

on every install/upgrade (portal `origin/main`), contradicting the module's
`del_list` in `assert_nodogsplash_allow_entries` (`99-tollgate-setup:1187-1194`).
Today the packet-filter guard is what makes that harmless. Under this record the
ports that block names are, in one mode or the other, **LuCI's admin login** —
so the block goes, and the portal repo grows the drift guard the module already
has (`tests/packaging/admin-board-not-guest-reachable_test.sh` cannot cover a
gitignored build product).

**D8 — The docs name both UIs, in one table, in one place.** This PR corrects
`docs/rc-tester-guide.md` §7 to say what `https://<hostname>.lan/` actually
answers in the build under test, to name the board's own pair, and to state the
HTTPS-only rule for admin cross-links. This record is the source of the mapping;
the guide is not a second opinion about it. The stale comment in
`31-admin-board-not-guest-reachable.nft:45-47` ("`:8080` (LuCI, kept reachable
pre-auth on purpose)") contradicts `32-*.nft` and is corrected with the mapping
change, not left as the next reader's trap.

**D9 — `:80` is not the admin entry point and does not become one.** "The
hostname opens the board" is true for `https://<hostname>.lan/`. A bare
`http://<hostname>/` is answered by the guest entry stub on `uhttpd.trusted`
(`setup_uhttpd_trusted_entry`, `99-tollgate-setup:590-664`), which redirects a
guest to the portal — that is the customer's entry and it stays the customer's.
Any design that made `:80` answer an admin login would hand every pre-auth guest
an admin surface, which is the rule #566 and #588 exist to enforce.

## Invariants

1. **No admin surface is guest-reachable.** Both pairs are dropped on the
   captive bridge for both families, pre- and post-authentication;
   `31-*.nft:52-53` and `32-*.nft:54-55` keep their port sets.
2. **The two UI pairs carry identical network scope.** The board and LuCI are
   reachable from the same set of networks in every mode. D6's cross-link liveness
   depends on it (no per-client answer exists — see Alternatives, A5), so a test
   fails if one pair's scope diverges from the other's.
3. **One binder per port.** Exactly one uhttpd section lists `:443`, exactly one
   lists `:8080`, exactly one lists `:8090`, exactly one lists `:8443`.
4. **The identity follows the listener.** `:443` (whichever section owns it) is
   served by the provisioned identity when one covers this router, and only then.
5. **No admin port in the pre-auth allow list**, on any setup path.
6. **`entry_ui` resolves to a mapping or to `luci`** — never to "no admin
   listener".
7. **A cross-link is `https://`, same host, router-supplied port, present only
   with a covering identity.**

## Consequences

### Positive

- The operator's own words become the shipped behaviour: the entry point answers
  the board, LuCI is secondary and reachable from it.
- Nothing in the guest path moves. No guard, no pre-auth entry, no portal
  listener, and no `ndsctl`/valve code is touched — the change is uhttpd
  listener placement plus one SPA button.
- The switch is visible and editable in the board's settings page for free (a
  flat scalar in the schema), and reverting is one value.
- The board's TLS listener stops advertising an identity that covers nothing —
  a security fix on its own, independent of the default flip.

### Costs, and what does not happen for free

- **Two writers must agree, from two repositories.** The mapping is computed in
  `99` and `92`; the single-rule test and the D4 marker are the only things
  standing between that and a router with one admin UI unbound.
- **The default flip is release-gated** (D4). A module-only release changes
  nothing visible; the operator decision is delivered by the release that vendors
  both halves.
- **A router without a covering identity has no `:443`.** Its board is then
  plain HTTP at `:8080`, as the board's `:8090` is today — D6 records it rather
  than pretending the flip is TLS-only.
- **The SOP surface moves**: the guide, the operator guide's "where do I log in"
  paragraph, and every internal note that says "LuCI is on `:8080`" or "the board
  is on `:8090`" describe the legacy mapping afterwards. A grep for the four port
  numbers in the two repos is part of the implementing PR, not an afterthought.
- **No bench evidence exists for any of this yet.** The assertions below are the
  only honest statement of what "done" means.

## Alternatives rejected, and why

### A1 — Add `:443`/`:8080` to the board's instance and leave LuCI's listeners alone

Rejected: both instances would list the same ports. uhttpd takes one binder per
port (the second instance's listener fails and, under procd, crash-loops), and
"one URL, two UIs" has no resolution that a browser could act on. The pair has
to be *moved*, which is D2.

### A2 — Redirect at the entry with a shipped `/www/index.html`

Rejected: `/www` and its `index.html` belong to LuCI's package. A file this
module ships there is not the file the next `apk` transaction leaves in place,
and a silent revert of the operator's default face is worse than not having it.

### A3 — A hostname alias for the board

Rejected: DNS selects an address, not a port. Both UIs are already the same
address; the operator's decision is about what the *entry point* answers, and on
this stack that is a port decision.

### A4 — Have the module own the entry instance's webroot as well (one writer)

Considered seriously, because one writer cannot drift. Rejected: the board's
webroot is a **build-time** substitution (`__ADMIN_HOME__` by the feed's
Makefile, `portal-build.sh:37-38`), so the module would have to keep its own
brand→webroot map — a map that already existed once, in the legacy brand-gated
configUI writer this record's D4 forbids (it hardcoded one brand's webroot and
claimed `:8090` behind the board's back) — and would bind `:443` to a docroot it
merely believes is right. A wrong guess there is an entry point that answers
nothing. Two writers that read one switch is the smaller risk, and D4's marker
bounds it.

### A5 — Gate the link per client, by the network the client came from

Rejected for now, and this is the constraint the card's second constraint is
really about. The board's backend is rpcd over the uhttpd instance's
`ubus_prefix`, and its plugin sees the **request payload only** (`openwrt/rpcd/tollgate`
pipes it through `cat`), so the router cannot say which network the browser is
on. The one place that *can* answer per socket is the module's own identity
resolver, and at the time of this decision the admin path could not reach it:
`30-backend-firewall.nft` dropped `:2121` for
`iifname != { "br-lan", "lo" }`. **Corrected 2026-10-04:** that exemption set
now names `br-private` too (`iifname != { "br-lan", "br-private", "lo" }`),
because the board is served on `br-private` and reads every value it shows from
`:2121` — with the drop in place the board rendered while all of its data died,
which is a defect, not a deliberate customer-surface boundary. The per-client
mechanism is therefore no longer blocked by the packet filter; the rejection
below rests on the cost of the mechanism itself, not on unreachability. So the
design keeps liveness a router-wide
fact and makes invariant 2 stand in for the per-client answer: a client that can
load the board at all is a client inside the admin scope, and both UIs share
that scope. If the scopes ever need to diverge, that is a change to this record
plus a per-client mechanism on the board's backend — not a silently narrower
link.

### A6 — Leave `:8090`/`:8443` in the pre-auth allow list (the portal's block)

Rejected: it is the guest-reachable admin login #546/#566/#588 removed, and
under this record the ports it names belong to one UI or the other in every
mode (D7). The guard is the second layer; the list must not be the first hole.

## What must be true before this ships

Every item is something a person or a script can run. An offline test is not
evidence for the hardware, and "measured on the bench" is not evidence for a
test.

**Offline (fake `uci`, no router, no network — the repo's existing tier, e.g.
`tests/uci-defaults-same-version-allowlist_test.sh`,
`tests/packaging/luci-not-guest-reachable_test.sh`)**

1. `entry_ui=board` moves **both** pairs: after a run on a fixture, `uhttpd.main`
   lists `8090`/`8443` and `home=/www`, `uhttpd.admin` lists `8080`/`443` with
   the board webroot and `error_page=/index.html`. `entry_ui=luci` reproduces
   today's mapping exactly, asserted against the literal port lists.
2. A missing `config.json`, a missing key, `entry_ui=""`, `entry_ui=garbage` and
   an absent `jq` all resolve to the **same** mapping as `board`, and none of
   them leaves an admin UI with no listener (invariant 6, asserted by counting
   listeners, not by reading log lines).
3. **No port is bound twice**: for each of `8080`, `443`, `8090`, `8443`, the
   fixture's final `uhttpd` state has exactly one section listing it
   (invariant 3). This is the assertion that would have caught the torn pair.
4. The **D4 marker path**: with `entry_ui=board` and **no** marker, `99` repairs
   to the `luci` mapping and logs exactly one WARNING naming the feed re-vendor;
   with the marker present, it binds the `board` mapping. Both directions, plus a
   negative control that the marker alone (with `entry_ui=luci`) changes nothing.
5. The derived redirect follows the **listener**: with a covering identity,
   whichever section owns `:443` has `redirect_https='1'` and the other has `'0'`;
   with a placeholder identity, both are `'0'` (#593's rule, applied per listener).
   The Go side answers the same value for the same fixture
   (`uhttpdCertPath`/`applyRedirectHTTPS`), which is the single-rule property.
6. No shipped writer adds `:8080`/`:443`/`:8090`/`:8443` to `users_to_router` on
   any setup path, in either mode — and the **portal** repo gets its own copy of
   that guard, since `92` is a gitignored build product in this tree
   (`tests/packaging/admin-board-not-guest-reachable_test.sh` covers the module
   half only).
7. The scope-equality guard (invariant 2): a fixture in which `31-*.nft` and
   `32-*.nft` are given different `iifname` sets fails, and the admin-access
   field (card `t_868d0fa7`) is asserted to produce one scope for both pairs.
8. `tollgate ui links --json` returns `url:null` with a non-empty `reason` when
   there is no covering identity or no live TLS listener, and a single
   `https://` URL — same host, the router's port — when there is; the SPA renders
   no anchor for `null` (a component test, not a vision check).
9. `entry_ui` round-trips the config chain the repo already tests: `gofmt -l .`,
   `go vet ./...`, `go build ./...`, `go test -race -count=1 -tags testenv ./...`
   from `src/`, the schema/struct two-way drift tests, `defaults_parity_test.go`,
   `node tests/contract/js-schema-lint.mjs`, `bash tests/contract/build-purity.sh`,
   plus the added `assertFieldValue` case (the surface map's four traps).
10. `docs/rc-tester-guide.md` and `docs/operator-guide.md` name the same mapping
    the code computes — a grep-level drift test over the four port numbers, in
    the shape of the repo's existing doc-contract checks, so #593's conflation
    cannot come back as prose.

**Bench (GL-MT3000, one owner, artifact identity read back per the repo's deploy
rules; `br-mgmt` needs PR #601's port move first)**

11. **The entry answers the board.** From the management path: `https://<hostname>.lan/`
    serves the board's own document (title/first paint of the SPA, not LuCI's
    meta-refresh), and `GET /cgi-bin/luci` on that listener 404s to the board
    (`error_page`), i.e. the 2026-09-20 incident does not recur in reverse.
12. **LuCI is secondary and reachable.** `:8443` completes a TLS handshake with
    an identity that covers the hostname used, and answers the LuCI login; `:8090`
    answers it over HTTP.
13. **The button.** With a covering identity the board renders the LuCI link and
    it is `https://`, same host, the router's port; with the identity removed
    (`tollgate ssl remove`) the link is **absent** and `tollgate ui links` names
    the reason. Both states, one artifact.
14. **Nothing new is guest-reachable.** From the open SSID pre-auth and
    post-auth: `:8080`, `:443`, `:8090`, `:8443` all dropped (the current bench
    result, unchanged), and the portal still answers on `:2050`/`:2051`.
15. **The management path still works from the cable**: `.lan/` on `br-mgmt`
    reaches the board's entry pair pre-auth (PR #601's scope), and `tollgate ssl
    status` reports the certificate each listener serves.
16. **The switch is live**: `tollgate config set entry_ui luci` flips the mapping
    and reloads uhttpd without a reboot; back to `board`; both directions probed
    on `:443`/`:8090`.
17. **The upgrade path that D4 exists for**: a router with a **pre-switch** `92`
    and the new module keeps the legacy mapping, logs the single WARNING, and
    loses no admin UI on `:8080`/`:8090`; then the feed re-vendor (both halves)
    makes `entry_ui=board` take effect on the next install without a manual step.
18. **Rollback**: `entry_ui=luci` on the fully-vendored build reproduces the
    pre-change bench results for items 11-16 (a downgrade is not required).

## What this record does not decide

- **The admin-access network field** (`br-private | br-mgmt | both |
  loopback-only`) and the private-SSID credentials are card `t_868d0fa7`, which
  folds into `lan-port-management-bridge-decision.md`. This record depends on
  that field's *scope* (D5's condition ii, invariant 2) but does not choose its
  values or its writer.
- **A paywalled wired network** is card `t_ba0558f2` (F1/F2 in the `br-mgmt`
  record). Nothing here depends on it.
- **Whether `:8443` should remain LuCI's secondary HTTPS port or LuCI should get
  a dedicated lower port** is deliberately left as-is: moving ports that two
  repositories, four guard fragments and a public documentation set already name
  is a bigger change than the operator's decision requires, and D2 shows the
  swap needs none of it.

## Implementation slices (one logical change per PR, per `AGENTS.md`)

| # | Repo | Change |
|---|---|---|
| 1 | `tollgate-module-basic-go` | `entry_ui` in `config.json` + schema + defaults + migration + version bump; `99`'s mapping writer and the D4 marker gate; the identity-follows-listener change in `setup_uhttpd_tls_identity` and `ssl.go`; the mode-aware rewrites of `drop_admin_listeners` / `purge_foreign_configui_sections` / `sanitize_uhttpd_main_configui_port`; `tollgate ui links`; offline suite 1-10 above; this record. |
| 2 | `tollgate-captive-portal-site` | `92` becomes mode-aware (entry vs secondary pair, marker, the provisioned identity, the pre-auth block removed, `:8443` no longer keyed off `/etc/uhttpd.crt`); the login page's `http://<host>:8080/` anchor replaced by the `ui links` render; the portal-side guard for invariant 5/7; portal tests. |
| 3 | `FreedomTechFeed/packages` | Re-vendor the bundle and the `92` from slice 2 and re-pin the portal commit in the same release that turns `entry_ui=board` on by default (D4) — the atomicity boundary. |
| 4 | bench | Card-sized: the bench assertions 11-18, on the artifact that carries slices 1-3. |
