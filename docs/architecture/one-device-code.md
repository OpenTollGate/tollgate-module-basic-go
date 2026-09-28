# One device code — hostname + captive SSID + private SSID

## Status: Decided (2026-09-27)

A router carries **one** device code: four characters of `[A-Z0-9]` — minted
as four hex characters (`[0-9A-F]`, the BusyBox-safe hexdump idiom shared
byte-for-byte with the installer's resolver and pinned by its
`branding_test.go`; new mints are hex, while the *acceptance* domain stays
`[A-Z0-9]` because codes adopted from earlier writers — including the
pre-device-code installer's full-alphanumeric mints — must survive) — minted
exactly once, stored in UCI, and reused forever. Every name a human reads off
the router is built from it, so the three names can never disagree:

| Identifier | Value | Writer |
|---|---|---|
| hostname | `tollgate-<code>` | module `setup_hostname`, installer `brandingCommands` |
| captive SSID | `TollGate-<code>` | module `setup_public_wifi`, installer `brandingCommands` |
| private SSID | `<nym>-<code>` | module `setup_private_network`, installer `brandingCommands` |
| portal banner (`nodogsplash.gatewayname`) | `<captive SSID> Portal` | module `setup_nodogsplash` **and** the verify/repair convergence in `99-tollgate-setup`; installer `brandingCommands` writes `<captive SSID>` with **no** ` Portal` suffix — see "The portal banner is written by three places" below |

The store is `/etc/config/tollgate`:

```
config device 'device'
	option code 'OQ3Q'
	option nym  'c08r4d0r'
```

## Context — the measured drift

Bench MT3000, 2026-09-26: `hostname=tollgate-OQ3Q`, the open SSID was
`tollgate-OQ3Q` in the morning and `tollgate-0GLK` after a later deploy, and the
private SSID carried a suffix from a third mint path. Three names, one router.

Three independent causes, all of them the same defect (nothing was ever stored,
so nothing could ever be reused):

1. **`packaging/files/etc/uci-defaults/99-tollgate-setup` re-minted on every
   full setup.** `RANDOM_SUFFIX=$(hexdump …)` → `GATEWAY_NAME`, and the private
   SSID minted its own `c08r4d0r-${RANDOM_SUFFIX}` from the same value. A
   version bump therefore re-randomised both SSIDs. The hostname was a constant
   (`TollGate`) plus a separate brand migration, so it never carried a code at
   all.
2. **The installer re-minted on every deploy** (`deploy.go`, `nodeName :=
   "tollgate-" + suffix` from `crypto/rand`) and wrote only the hostname, the
   captive SSID and the nodogsplash gateway name. `brandingCommands` skips
   `private_radio*` on purpose ("Skip private_radio* (admin LAN) and `*_uplink`
   (WAN repeater)"), so the private SSID kept a code from a different mint — and
   the numerate/alphanumeric alphabets differed between the two writers.
3. **The verify/repair path read the SSID back** out of the live config instead
   of deriving it, which is how a re-minted SSID survived a reinstall untouched.

## Decision

**One store, one adoption order, one mint alphabet, shared by both repos.** The
contract is duplicated deliberately (two repos, two languages of record), and
both sides pin the same case table so a change to one fails the other's tests —
with **one known exception, the nym charset**, which is stated here rather than
claimed to match (see "The nym charset differs between the two writers below"):

**SSID recognition set (who counts as a TollGate upstream).** The captive SSID
carries the brand's prefix, so SSID-based recognition — reseller-mode upstream
selection, scan classification, the vendor-element score heuristic — must match
every prefix the writer can emit: `TollGate-` and the whitelabel `Net4sats-`,
case-insensitively (the pre-device-code installer wrote lowercase
`tollgate-<code>` SSIDs; those routers remain in the fleet). A Net4sats-branded
TollGate is a valid upstream. The single Go home for this set is
`hasTollGateSSID` (wireless_gateway_manager); the shell side's equivalent is
`load_brand`'s table. The definitive, brand-independent identification remains
the vendor IE, staged behind `VendorIEDiscovery` (default off) — when that
wiring lands, SSID matching stays as the weak heuristic, not the gate.

* module: `tests/uci-defaults-device-code_test.sh`
* installer: `branding_test.go`

### Adoption order — the store is authoritative

1. `tollgate.device.code` from `/etc/config/tollgate` (validated as exactly four
   `[A-Z0-9]`; a junk value is re-derived, never trusted).
2. a **machine-shaped hostname** (`tollgate-OQ3Q`, `TollGate-OQ3Q`, or a
   whitelabel build's `<brand>-OQ3Q`) — this is what the installer has always
   written.
3. a **machine-shaped captive SSID** (`TollGate-OQ3Q`, `tollgate-0GLK`).
4. **mint** — four characters of `[A-Z0-9]` from `/dev/urandom`, BusyBox
   `hexdump` idiom (no `od` on the target).

Only step 4 mints, and only when nothing above produced a code. Steps 2 and 3
exist for routers that are already deployed: the store is empty on every one of
them, so an upgrade adopts the code the router is *already known by* instead of
collecting a third one. That is what heals the bench box on the next install.

### The nym

`<nym>` is the operator's own prefix for the private SSID (`c08r4d0r` by
default). It is stored in the same section so both writers agree, and it is
adopted from an existing machine-shaped private SSID, so a module deployed under
another nym keeps it. It is used for the **private** SSID only — the captive SSID
keeps the brand prefix (`TollGate-` / a whitelabel build's `<brand>-`), because
reseller-mode
upstream discovery in `src/wireless_gateway_manager` matches `"TollGate-*"`
**case-sensitively** (`discovery_log.go`, `vendor_element_manager.go`,
`upstream_manager.go`).

### What each path does

| Path | Device code | hostname | captive SSID | private SSID |
|---|---|---|---|---|
| module, first boot / version-changing install | resolved + stored | rewritten when brand-default or machine-shaped (#444 preserved) | derived | derived |
| module, verify/repair (same version) | resolved + stored | **not touched** | converged when machine-shaped, an operator's own name preserved | not touched |
| installer deploy | resolved + stored | derived | derived | derived |

The verify/repair path deliberately does **not** rewrite the hostname: it is the
path a same-version reinstall takes, its contract is that the operator's state is
left alone, and a router that has never been through a full setup under a device
code gets its hostname converged by the next version-changing install. Converging
it there would also re-key the router's TLS identity (`ensure_admin_tls_identity`
provisions a certificate covering the current hostname), which is not something a
reinstall of the same build should do.

The captive SSID follows the same rule on that path
(`captive_ssid_for_code`), which is **also the behaviour it had before this
change**: the old code read the live SSID back and re-asserted it, so an
operator-named open network survived a reinstall. The convergence applies to a
name this stack wrote — brand prefix (either brand, either case) plus a
machine-shaped suffix — and to a missing one; anything else is the operator's own
name and is preserved. A reinstall must not rename a network the operator named,
and the open SSID is the one network a visitor has to re-pick by hand. The FULL
path and the installer's branding still write the brand's name, which is what
they have always done. `nodogsplash.gatewayname` is machine-owned on every path,
so the repair path converges it with the SSID instead of leaving the portal
banner on the pre-convergence name — and both module paths write the SAME string,
`<captive SSID> Portal` (see "The portal banner is written by three places").

### The escape hatch

`tollgate network private rename <name>` (`docs/operator-guide.md`) is honoured:
the setup script re-derives the private SSID **only** when the current one is
missing or machine-shaped (`<nym>-` + four characters, or `<nym>-` + digits, the
older numeric form). A renamed SSID is neither, so it is preserved. The PSK is
never re-derived.

### The nym is charset-checked, from wherever it comes

The private SSID is `<nym>-<code>`, and the nym can come from the store, from an
existing machine-shaped private SSID, or from the default. **All three paths are
charset-checked on this side** (`safe_nym`, `[A-Za-z0-9_-]`), and a value that
fails falls back to the default nym rather than being carried. The installer's
check is NOT the same charset — see "The nym charset differs between the two
writers below":

* the store's value was already checked, and the value is operator-writable;
* an **adopted prefix** was not, and it is arbitrary text read off the router.
  That matters because the private SSID is not only written here: the installer
  builds its write as a *single-quoted* `uci -q set …` line, on a chain joined
  with `&&` and run as root. A quote in the value would end the quote, break the
  chain before the commits, and — with the right bytes — be read as shell syntax.
  The installer refuses such a value outright (`ssidSafeForShell`); this side
  never builds one.

Both halves pin the behaviour: `tests/uci-defaults-device-code_test.sh` and
`branding_test.go` (`TestDeviceIdentityScriptAdoptionOrder` case
"a value that cannot be quoted is neither adopted nor preserved",
`TestPrivateSSIDCommandRefusesAValueItCannotQuote`).

One asymmetry is deliberate and worth stating for an operator: a renamed private
SSID containing a quote is safe **here** (this script hands values to `uci` as
argv, with shell quoting), so it stays the operator's name across a reinstall —
but the installer's write cannot carry that value and refuses it, so the same
network converges on `<nym>-<code>` at the next **deploy** (an operator action,
not a reinstall). Each layer is individually safe; the end state is the code.

### The nym charset differs between the two writers — stated, not claimed to match

Read off both trees (2026-09-27), the two charset checks are **not** the same:

| side | check | refuses |
|---|---|---|
| module `99-tollgate-setup` `safe_nym` | `case "$1" in ''\|*[!A-Za-z0-9_-]*) return 1` | anything outside `[A-Za-z0-9_-]` — spaces, dots, `@`, quotes |
| installer `deploy.go` `ssid_safe` / `ssidSafeForShell` | `case "$1" in ''\|*"'\"*) return 1` | only the empty string and a value containing a single quote |

**Consequence — a stored-nym flap.** The installer's check exists to protect its
*single-quoted* `uci -q set` chain, and it is strictly weaker than this side's.
For an existing private SSID the module refuses but the installer accepts, e.g.
`my nym-AB12`:

* the installer adopts the prefix `my nym` (`ssid_safe` passes) and stores
  `tollgate.device.nym='my nym'`;
* the module reads that stored value, `safe_nym` refuses it (a space), falls back
  to the default and stores `tollgate.device.nym='c08r4d0r'`.

The two writers therefore overwrite each other's stored nym on alternate runs.
**This is a real cross-repo divergence, tracked here rather than fixed here**:
the right fix is ONE charset — this side's, which is the stricter and the one the
private SSID is validated against at every read — and it belongs in
`OpenTollGate/tollgate-installer` (`deploy.go`, `ssid_safe`/`ssidSafeForShell`),
where the value is only ever written back inside the single-quote chain. Until
that lands, a private SSID whose prefix is not `[A-Za-z0-9_-]` is not a stable
nym source across the two writers, and the module's stricter refusal (fall back
to the default nym) is the safe end of the flap.

### The portal banner is written by three places

`nodogsplash.@nodogsplash[0].gatewayname` is the string a captive client reads on
the splash page, and three writers touch it:

| writer | value | committed? |
|---|---|---|
| module full setup (`setup_nodogsplash`) | `"${GATEWAY_NAME} Portal"` | yes — the full path's `commit_all` |
| module verify/repair convergence (reinstall of the same version) | `"${GATEWAY_NAME} Portal"` — same string | yes — committed in the block that writes it (see below) |
| installer `brandingCommands` | `id.SSID` → `TollGate-<code>`, **no** ` Portal` suffix | yes (the installer's own `uci commit`) |

Both module paths now write one identical value, and the repair path **commits it
where it writes it**: the block runs before the nodogsplash export-diff snapshot
(`nds_before`) is taken, so a gatewayname-only change could never be the reason
that commit fires. Without the explicit commit the write stayed a `/tmp/.uci`
session delta on a settled router — the config file kept the old banner, the
convergence was lost at the next reboot, and any later `uci commit nodogsplash`
(the stale `gatewaydomainname` delete, or an allow-list repair) re-applied the
delta on top of the file. That is guarded by
`tests/uci-defaults-gatewayname-banner_test.sh`, which drives BOTH paths against
a delta-aware `uci` (committed state + a pending-delta file) and pins the same
committed value from each plus an empty gatewayname delta.

The installer's spelling is the remaining cross-repo divergence: it writes the
banner **without** the ` Portal` suffix, and it is the writer that runs last on a
deployed router, so a deploy after a setup flips the banner back. Reconciling it
means adding the same suffix in `brandingCommands` (`OpenTollGate/tollgate-installer`)
— a one-line change in that repo, listed here as a follow-up rather than claimed
as done. The banner keeps its ` Portal` suffix on this side because that is the
string every deployed router already shows (the full setup has written it since
before this decision), so changing it would be a user-visible rename with no
driver.

## Consequences

* **A code is stable across reinstall, upgrade and a sysupgrade that keeps
  settings.** The repo's preservation contract is the listed set in
  `packaging/files/lib/upgrade/keep.d/tollgate`, and `/etc/config/tollgate` is
  **listed there** (OpenWrt also keeps `/etc/config/*` wholesale, but the listed
  set is the one this repo documents and tests). A **wiped** sysupgrade or a factory reset
  has no store by definition and mints a new code — the only case in which the
  code changes.
* **Already-deployed routers change names once**, on the first install that
  carries this change: the captive SSID converges on the adopted code (and on the
  `TollGate-` prefix, which the installer had been writing lowercased), and the
  private SSID converges on `<nym>-<code>`. The paired PSK does not change, so an
  admin device re-joins the renamed SSID with the same password. Clients see one
  rename, not a re-key.
* **The hostname is part of the identity now.** The module's hostname is
  `tollgate-<code>` instead of the constant `TollGate`, which is what makes the
  name readable at a glance (and what the installer has been writing all along).
  The TLS behaviour follows: `setup_hostname` runs before
  `ensure_admin_tls_identity` on both setup paths, so the certificate is
  provisioned (or re-provisioned) for the settled hostname and `redirect_https`
  is re-derived afterwards.
* **The store is committed inside `setup_device_identity`**, not in the driver's
  single pass at the end. A failure later in the setup must not lose the code —
  the next run would then mint a different one, which is the drift this store
  exists to stop.
* The installer still writes the hostname unconditionally (it does not preserve a
  custom hostname), which is pre-existing behaviour and out of scope here.

## Non-goals

* **Renaming the captive SSID prefix to lowercase** (the shape this change was
  originally described with: `captive SSID=tollgate-<code>`): rejected. Reseller
  discovery and every third-party client in the wild match `TollGate-*`; the
  Android client matches it case-insensitively, but the module's own upstream
  discovery does not, and today it is already broken for installer-branded
  (lowercase) SSIDs. The unified thing here is the CODE, not the prefix case.
* **The per-device random private PSK** (t_868d0fa7) is implemented in
  `src/cli/network.go` / config schema v0.0.9 and touches neither this store nor
  the uci-defaults path.
* No new CLI verb: `tollgate.device.code` / `.nym` are readable and writable with
  plain `uci`.

## Cross-repo shipping

* module: this repo (`packaging/files/etc/uci-defaults/99-tollgate-setup`).
* installer: `OpenTollGate/tollgate-installer` (`deploy.go` — the router-side
  resolver and `brandingCommands`), which is the writer that runs *last* on a
  deployed router.
* To reach a router both halves travel the feed: the module change lands first,
  then the feed re-pin ships it.
