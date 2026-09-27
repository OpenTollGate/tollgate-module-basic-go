# One device code — hostname + captive SSID + private SSID

## Status: Decided (2026-09-27)

A router carries **one** device code: four characters of `[A-Z0-9]`, minted
exactly once, stored in UCI, and reused forever. Every name a human reads off
the router is built from it, so the three names can never disagree:

| Identifier | Value | Writer |
|---|---|---|
| hostname | `tollgate-<code>` | module `setup_hostname`, installer `brandingCommands` |
| captive SSID | `TollGate-<code>` | module `setup_public_wifi`, installer `brandingCommands` |
| private SSID | `<nym>-<code>` | module `setup_private_network`, installer `brandingCommands` |

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
both sides pin the same case table so a change to one fails the other's tests:

* module: `tests/uci-defaults-device-code_test.sh`
* installer: `branding_test.go`

### Adoption order — the store is authoritative

1. `tollgate.device.code` from `/etc/config/tollgate` (validated as exactly four
   `[A-Z0-9]`; a junk value is re-derived, never trusted).
2. a **machine-shaped hostname** (`tollgate-OQ3Q`, `TollGate-OQ3Q`,
   `Net4sats-OQ3Q`) — this is what the installer has always written.
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
keeps the brand prefix (`TollGate-` / `Net4sats-`), because reseller-mode
upstream discovery in `src/wireless_gateway_manager` matches `"TollGate-*"`
**case-sensitively** (`discovery_log.go`, `vendor_element_manager.go`,
`upstream_manager.go`).

### What each path does

| Path | Device code | hostname | captive SSID | private SSID |
|---|---|---|---|---|
| module, first boot / version-changing install | resolved + stored | rewritten when brand-default or machine-shaped (#444 preserved) | derived | derived |
| module, verify/repair (same version) | resolved + stored | **not touched** | derived | not touched |
| installer deploy | resolved + stored | derived | derived | derived |

The verify/repair path deliberately does **not** rewrite the hostname: it is the
path a same-version reinstall takes, its contract is that the operator's state is
left alone, and a router that has never been through a full setup under a device
code gets its hostname converged by the next version-changing install. Converging
it there would also re-key the router's TLS identity (`ensure_admin_tls_identity`
provisions a certificate covering the current hostname), which is not something a
reinstall of the same build should do.

### The escape hatch

`tollgate network private rename <name>` (`docs/operator-guide.md`) is honoured:
the setup script re-derives the private SSID **only** when the current one is
missing or machine-shaped (`<nym>-` + four characters, or `<nym>-` + digits, the
older numeric form). A renamed SSID is neither, so it is preserved. The PSK is
never re-derived.

## Consequences

* **A code is stable across reinstall, upgrade and a sysupgrade that keeps
  settings** (OpenWrt keeps `/etc/config/*`; the store is a config file, so it is
  kept with the rest of `/etc/config`). A **wiped** sysupgrade or a factory reset
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
