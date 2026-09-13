# TollGate `v0.6.0-alpha2` — tester guide

**Channel:** `alpha` (a release candidate, not a stable release).
**Audience:** you have an OpenWrt router, you can SSH into it, and you are
willing to lose whatever is on that router.

This guide covers, in order: what this release supports, installing it from
the package feed, upgrading, removing, rolling back, what to do when something
fails, how to report a result we can act on, and what "alpha" means here.

## How to read the verification markers

Every command in this document was executed before publication, but not every
command could be executed on router hardware. Two markers are used:

- **VERIFIED (rehearsal)** — the command was run against a real OpenWrt
  25.12.5 userland (`apk-tools 3.0.5`, `x86_64`) installing a rehearsal
  package from a local copy of the feed layout described below. The exact
  output quoted is real output from that run.
- **UNTESTED ON A ROUTER** — the command could not be exercised without a real
  device (anything that needs `procd`, `ubus`, `logread`, the captive portal,
  or the published feed host). It is documented intent, not a verified claim.
  §10 lists all of them.

The published feed host is **not final**: it is written below as
`<FEED-BASE-URL>` and must be filled in when the testing channel is published.
The URL *shape* (full URL to the index **file**, not a directory) is what a
25.12 router requires — that part is verified.

---

## 1. What this release supports (read this first)

> **PLACEHOLDER — the final supported matrix is set by what the RC acceptance
> test passed on hardware, and is not decided yet.** The table below is the
> current proposal, and is filled in by the maintainer before this guide is
> announced. Architectures that were built but never installed on hardware are
> published as **"built, untested"** — that is not a support claim.

| OpenWrt release | Architecture | Status |
|---|---|---|
| 25.12.x (apk) | `x86_64` | Install rehearsal passed on a real 25.12.5 userland (container). Router-hardware acceptance test **pending**. |
| 25.12.x (apk) | every other architecture the build matrix produces | **built, untested** — no hardware acceptance test yet |
| 24.10.x and earlier (opkg) | any | **NOT supported** — see below |
| snapshots / master | any | **NOT supported** for testers |

**Why 24.10 and earlier are not supported.** Their OpenWrt SDK toolchains ship
Go 1.21 / 1.23 and cannot build this module (it requires a current Go
toolchain), so no package for those releases is produced by this build. There
is also no opkg feed: publishing an empty or unsigned opkg index would read to
a router as a broken feed. 24.10's `opkg` has signature checking enabled by
default (`option check_signature` in `/etc/opkg.conf`), so a hand-built
unsigned index does not even load cleanly.

**If you are on 24.10 or earlier:** the feed is not for you. The release
announcement also lists prebuilt package files (the "file-download channel",
fetched from the announced artifact URLs with their `sha256` values). Those
are **best-effort one-offs**: no feed, no automatic upgrade, no tested rollback
path, no compatibility promise, and nothing we can fix for you if they do not
work. Whether the package would run at all on your release line is untested —
it was built for 25.12.

---

## 2. Before you install

- A supported router (§1) with working SSH and root access.
- About **30 MB free** in `/overlay` (the two installed binaries are ~28 MB in
  total in the rehearsal build).
- **Back up first** — copy `/etc/tollgate/` (wallet and identities) and
  `/etc/config/tollgate` somewhere off the router. Removing or rolling back the
  package does not delete those paths (verified — see §5), but an alpha is an
  alpha; keep a copy.
- Do not install this on a router you or anyone else depends on.

---

## 3. Install from the feed (OpenWrt 25.12)

Three steps: install the feed's signing key, point `apk` at the testing
channel, install the package.

### 3.1 Install the feed signing key

`apk` refuses an index it cannot verify against a trusted key, so the key
comes first. It is a public key; installing it is a file download, not a
secret exchange.

```sh
wget -O /etc/apk/keys/tollgate-testing.pem https://<FEED-BASE-URL>/keys/tollgate-testing.pem
```

**VERIFIED (rehearsal)** — download followed by `ls -l /etc/apk/keys/`:

```
-rw-r--r--    1 root     root           178 Jun 29 12:59 openwrt-25.12.pem
-rw-r--r--    1 root     root           178 Sep 12 21:28 tollgate-testing.pem
```

The file is a short PEM public key (`-----BEGIN PUBLIC KEY-----` on the first
line). If `wget` reports anything other than success, stop and read §7 — an
index download that cannot be verified is the most likely failure on a real
router.

### 3.2 Point `apk` at the testing channel

The repository line is a **full URL to the index file**, not a directory:

```sh
echo "https://<FEED-BASE-URL>/testing/25.12/x86_64/packages.adb" \
  > /etc/apk/repositories.d/tollgate-testing.list
```

- `packages.adb` is the 25.12 index format. `APKINDEX.tar.gz` does not exist on
  this release line — if you see someone using it, that is old material.
- The path shape `<channel>/<openwrt-line>/<arch>/packages.adb` follows the
  channel layout the feed publishes. **Provisional:** the serving side
  (channel host, path layout, cache headers) was still being finalised when
  this guide was written; the line above is the intended shape and is the one
  the rehearsal used. If the announcement prints a different path, the
  announcement wins.
- The testing channel is deliberately separate from any production channel:
  same host or not, it has its own path, its own index and its own key. A
  production router never sees this release in `apk upgrade`, and unsubscribing
  is deleting this one file.

### 3.3 Refresh the index

```sh
apk update
```

**VERIFIED (rehearsal)**

```
 [https://downloads.openwrt.org/releases/25.12.5/targets/x86/64/packages/packages.adb]
 [https://downloads.openwrt.org/releases/25.12.5/packages/x86_64/base/packages.adb]
 ...
 [http://<rehearsal-feed>/testing/25.12/x86_64/packages.adb]
OK: 11240 distinct packages available
```

Exit status 0, your feed line listed, and a package count. (The
`downloads.openwrt.org` lines are the stock OpenWrt feeds the router already
has; the rehearsal was an offline copy of a router's feed list.) If the count
drops or your line is listed as unavailable, see §7.

### 3.4 Install

```sh
apk add tollgate-wrt
```

**VERIFIED (rehearsal)**

```
(1/2) Installing jq (1.8.1-r2)
  Executing jq-1.8.1-r2.post-install
(2/2) Installing tollgate-wrt (0.6.0_alpha2-r1)
OK: 39.1 MiB in 138 packages
```

`tollgate-wrt` is the package name. It installs two binaries —
`/usr/bin/tollgate-wrt` (the service) and `/usr/bin/tollgate` (the CLI) — plus
`/etc/init.d/tollgate-wrt`. `jq` is a dependency and is pulled in
automatically.

**Do not pin a version on this line.** `apk add tollgate-wrt=<version>` writes
a pin into `/etc/apk/world` that then blocks normal upgrades (verified — §6).

### 3.5 What a successful install looks like

```sh
apk list --installed tollgate-wrt
ls -l /usr/bin/tollgate /usr/bin/tollgate-wrt /etc/init.d/tollgate-wrt
tollgate status
tollgate version
```

**VERIFIED (rehearsal)** for the package listing and the files:

```
tollgate-wrt-0.6.0_alpha2-r1 x86_64 {tollgate-wrt} (GPL-3.0-only) [installed]
-rwxr-xr-x    1 nobody   nogroup       1380 /etc/init.d/tollgate-wrt
-rwxr-xr-x    1 nobody   nogroup   10253038 /usr/bin/tollgate
-rwxr-xr-x    1 nobody   nogroup   17732119 /usr/bin/tollgate-wrt
```

**VERIFIED (rehearsal)** for the CLI, with the service running:

```
# tollgate version
TollGate Version
version: v0.6.0_alpha2
commit: dcf8c5d
build_time: 2026-09-13T00:00:00Z
go_version: go1.26.0
openwrt_version: OpenWrt 25.12.5 r33051-f5dae5ece4

# tollgate status
running: true
version: TollGate v0.6.0_alpha2
uptime: 17.799201311s
config_ok: true
wallet_ok: true
network_ok: true
```

**Version strings, so you know what you are looking at:**

| where | value |
|---|---|
| release tag / announcement | `v0.6.0-alpha2` |
| package version (`apk list --installed`) | `0.6.0_alpha2-r1` |
| runtime version (`tollgate version`) | `v0.6.0_alpha2` |

apk normalises the hyphen into an underscore for its own version ordering; the
`-r1` release revision is set when the package is built. The runtime string is
the apk-safe form as well. The exact `-r` number and commit for the published
build are printed in the announcement — compare rather than assume.

**The CLI talks to the running service over `/var/run/tollgate.sock`.** If the
service is not running, `tollgate status` and `tollgate version` report
`failed to communicate with TollGate service: ... connect: no such file or
directory` (VERIFIED). That is a service problem, not a broken installation —
the version check that never needs the service is
`apk list --installed tollgate-wrt`. Neither binary accepts a `--version` flag
(VERIFIED: `tollgate --version` → `unknown flag: --version`); use the
`version` subcommand.

**Service status on the router — UNTESTED ON A ROUTER:**

```sh
/etc/init.d/tollgate-wrt status      # needs procd/ubus
logread -e tollgate | tail -50       # needs syslog; the service logs via syslog
```

The rehearsal container has no `procd`/`ubus`/`logread`, so neither of these
was executed. On a real router, expect the init script to report `running` and
the log to show the service starting, the wallet initialising and
`CLI server initialized and listening on Unix socket` (that last line was
observed in the rehearsal with the binary started by hand).

---

## 4. Upgrade to a newer build in the channel

Upgrade this package only:

```sh
apk update
apk upgrade tollgate-wrt
```

**VERIFIED (rehearsal):** `apk upgrade tollgate-wrt` exits 0 (the rehearsal
container was already on the newest build in its channel, so it reported
`OK: 39.1 MiB in 138 packages`; the through-an-upgrade case was exercised with
the plain `apk upgrade` form below).

**Do not run a bare `apk upgrade` on a router you care about** unless you
accept that it upgrades every other package too. VERIFIED: on a rehearsal
router with a mixed feed list, bare `apk upgrade` rewrote 43 stock packages
and several post-upgrade scripts failed, purely because the container has no
`procd`. `apk upgrade tollgate-wrt` keeps the blast radius at one package.

**UNTESTED ON A ROUTER:** that the package's post-install logic restarts the
service on the router, that `/etc/config/tollgate` survives an upgrade
byte-for-byte, and that the wallet keeps working afterwards. The config file
is not written by the package (it is created on first run by the
`99-tollgate-setup` script), which is normally how OpenWrt keeps it across
upgrades, but that has not been confirmed on a device.

---

## 5. Remove

```sh
apk del tollgate-wrt
```

**VERIFIED (rehearsal)**

```
(1/2) Purging tollgate-wrt (0.6.0_alpha2-r1)
(2/2) Purging jq (1.8.1-r2)
  Executing jq-1.8.1-r2.pre-deinstall
OK: 11.4 MiB in 136 packages
```

After it: `/usr/bin/tollgate`, `/usr/bin/tollgate-wrt` and
`/etc/init.d/tollgate-wrt` are gone, and the package's `apk list --installed`
entry is empty.

**Do not do this unless** you can put the package back — keep the repository
line and key installed (§3.1–3.2) so that `apk add tollgate-wrt` works again,
and keep the backup from §2.

**What removal does and does not delete (VERIFIED):** dependencies that were
installed automatically with it are removed too (`jq` above). Runtime data
under `/etc/tollgate/` is **not** touched: a marker file written into that
directory before `apk del` was still there afterwards, and the directory
itself survived. Files that came from the package (the captive-portal site
directory, `ecash/`) go away with it, and so do the package's `uci-defaults`
scripts — reinstalling restores them.

If you do not plan to reinstall: do not remove the package to "clean up"
before reporting a problem. Remove only when you are done, and report first.

---

## 6. Roll back to a known-good version

Rollback is an install of an older version, pinned:

```sh
apk add 'tollgate-wrt=<known-good-version>'
```

**VERIFIED (rehearsal)**

```
(1/1) Downgrading tollgate-wrt (0.6.0_alpha3-r1 -> 0.6.0_alpha2-r1)
```

Exit status 0, and `apk list --installed tollgate-wrt` shows the older
version. Ordering matters and is real: apk knows `0.6.0_alpha3-r1` is newer
than `0.6.0_alpha2-r1`, which is why the same command upgrades, downgrades, or
does nothing, depending on which version you name.

**`--force-downgrade` does not exist on 25.12.** Older material (and some
tickets) tell you to run `apk add --force-downgrade ...`. That is not an
apk-tools 3.0.5 option — VERIFIED: `ERROR: command line: unrecognized option
'force-downgrade'`. The command above is the rollback on this release line; the
`--force-downgrade` wording belongs to opkg-era routers (opkg on 24.10 does
have that flag, but there is no feed for 24.10 — §1).

**The trap: rolling back pins you.** `apk add 'tollgate-wrt=<version>'` writes
`tollgate-wrt=<version>` into `/etc/apk/world` (VERIFIED:

```
# grep tollgate /etc/apk/world     (before the pin)
tollgate-wrt
# ... after the rollback ...
tollgate-wrt=0.6.0_alpha2-r1
```

). While that pin is there, `apk upgrade tollgate-wrt` will **not** move you
to a newer build (VERIFIED: it stayed on the pinned version). When you are
ready to follow the channel again:

```sh
apk add tollgate-wrt            # drops the pin; /etc/apk/world goes back to "tollgate-wrt"
apk upgrade tollgate-wrt        # VERIFIED: Upgrading tollgate-wrt (0.6.0_alpha2-r1 -> 0.6.0_alpha3-r1)
```

**Do not use `apk upgrade --available` as a rollback.** VERIFIED: it is a
whole-system "reset every package to whatever the repositories offer"
operation — in the rehearsal it rewrote 43 unrelated stock packages and
reported 6 script errors. It is not a targeted downgrade.

---

## 7. When something goes wrong

These are the failures that were reproduced in the rehearsal, with the exact
messages apk prints.

| What you see | What it means | What to do |
|---|---|---|
| `WARNING: updating and opening <url>: UNTRUSTED signature` and `apk update` exits non-zero; `apk add` then says `unable to select packages ... (no such package)` | The index could not be verified against a trusted key | Do **not** add `--allow-untrusted`. Re-do §3.1, check `ls -l /etc/apk/keys/` for the key file and its permissions, then §3.3. Report it if it still fails |
| `ERROR: wget: exited with error 4`, `WARNING: ... unexpected end of file`, `1 unavailable, 0 stale` | The feed was not fetched — wrong URL, no route, or host down | Check the repository line character by character (§3.2), retry, then report. Nothing was half-installed (VERIFIED) |
| `ERROR: tollgate-wrt-...: failed to extract usr/bin/tollgate: Connection aborted` | The downloaded package is truncated/corrupt | Re-run `apk add tollgate-wrt`. VERIFIED: the package is not installed, `/usr/bin/tollgate` does not exist afterwards. Report the router if it repeats — that could be the mirror, not you |
| `ERROR: unable to select packages: ... error: uninstallable / arch: mipsel_24kc` | The index you are pointed at does not contain your architecture | Report your `apk --print-arch` output. VERIFIED: there is no wrong-arch fallback — apk refuses rather than installing something for another architecture |
| `tollgate status` → `failed to communicate with TollGate service ... no such file or directory` | The service is not running, or the socket is missing | The install is intact. Check `/etc/init.d/tollgate-wrt status`, then `logread -e tollgate \| tail -50`, and include both in a report |
| `apk upgrade tollgate-wrt` does nothing after a rollback | The version pin in `/etc/apk/world` is still active | §6 — drop the pin with `apk add tollgate-wrt` |

**Never use `--allow-untrusted`.** It disables the verification that makes the
feed trustworthy, and it never becomes necessary on a correct setup: the
rehearsal reproduced both halves — an unsigned index fails
(`WARNING: ... UNTRUSTED signature`, `apk update` exit 1, `apk add` installs
nothing), and the *same* package installs from the same channel without the
flag once the index is signed. With `--allow-untrusted` the unsigned channel
installed happily, which is exactly the hole the rule closes. If you have a
feed problem, fix the key or the URL — do not reach for that flag, and do not
paste it into a report as advice.

---

## 8. What to expect from an alpha

- **Not production software.** Do not put funds, users, or anything you need
  on this router. A router that matters should stay on the stable release.
- **The wallet path is being hardened.** The Cashu wallet's failure paths
  (cancelled payments, drained wallets, interrupted funding) are the active
  area of work, and this build may still contain defects there.
- **Funds-loss reports are top priority.** Any symptom that could mean money
  left the wallet the wrong way — a balance that changes when nothing was
  bought, a sale that is credited but not paid, a cancel or drain that behaves
  oddly — is a **stop-ship** report (severity S1 in §9). Tell us immediately;
  we would rather halt the release than learn about a second case.
- **Known limitations for this build** are listed in `RELEASE-NOTES.md` under
  "Verification status" — a version/setup-rerun path that has only ever been
  exercised in a checkout, plus the router-only behaviours. Read it before
  concluding that something you found is a new bug.
- **Everything can move.** The version string, the channel path, the index
  format and the upgrade path are all still being settled; an RC can change
  under you between builds, and this guide with it.
- **There is a rollback** (§6), and it is worth knowing before you install
  rather than after.

---

## 9. How to report a result

An actionable report carries facts, not impressions. Run these and paste the
**output**, not a screenshot or a paraphrase:

```sh
cat /etc/openwrt_release
apk --print-arch
cat /etc/apk/repositories.d/tollgate-testing.list
apk list --installed tollgate-wrt
sha256sum /usr/bin/tollgate /usr/bin/tollgate-wrt
tollgate version
logread -e tollgate | tail -50
```

Plus, in your own words: the router model; the release and architecture you are
on; what you expected; what actually happened; whether the service was running
at the time; and whether you are reporting a clean install, an upgrade, a
removal, or a rollback.

`tollgate version` needs the service running (§3.5). If it is not running,
say so and send the `apk list --installed` line instead — a report without a
version and an architecture is untriaged and we will ask once, then close it.

**Where to send it:** this release defines exactly one intake channel, and
[tester-intake.md](tester-intake.md) is its authoritative description —
the pinned issue *“Tester reports — TollGate v0.6.0-alpha2 (alpha channel)”* in
the project's public issue tracker
(<https://github.com/OpenTollGate/tollgate-module-basic-go/issues>), where your
report is a **comment**. Do not open a new issue for a report, and do not send
the same report anywhere else as well. The intake document also states the
triage rule (a report without a package version and an architecture is
untriaged, is asked once, then closed), what each severity means, the
funds-related stop-ship rule, and the honest support matrix.

**Severity, so you can see how it will be handled:**

- **S1** — any wallet/funds symptom, any loss of funds. Stop-ship: the channel
  may be pulled before anyone investigates further.
- **S2** — the service is broken, crashes, will not start, or the install
  leaves the router in a bad state.
- **S3** — cosmetic, documentation, or anything you are unsure about.

**Never paste any of the following into a report, a chat, an issue or a
screenshot:**

- `tollgate config get` output, or the contents of `/etc/tollgate/`
  (identities, wallet database) — it contains your keys;
- a wallet file, a seed phrase, a mnemonic, or an `nsec`;
- a Cashu token, or the output of `tollgate wallet drain cashu`;
- router credentials, WireGuard keys, or private network passwords.

If you think you sent a secret by accident, say so immediately without pasting
it again — that is a recoverable situation, and it is one we would rather hear
about than discover later.

---

## 10. What a real-router test still has to confirm

Everything below is documented here but was **not** executed on hardware, and
this guide does not present it as verified:

1. The published feed host and path (`<FEED-BASE-URL>`, channel layout and
   cache headers) — the serving side was still being built when this was
   written.
2. The real, signed index and its key, downloaded over HTTPS from that host.
3. The real `0.6.0_alpha2-r1` package bytes: filename, `sha256`, and that the
   provided `sha256` matches the artifact published in the announcement.
4. `/etc/init.d/tollgate-wrt` `enable`/`status`/`start` under `procd`, the
   service starting on boot, and the daemon staying up ≥ 5 minutes.
5. `logread` output, and `tollgate logs` (which wraps it).
6. First-run behaviour: `99-tollgate-setup` running from `uci-defaults`,
   `/etc/config/tollgate` being created, and WiFi/portal configuration being
   preserved across an upgrade.
7. The captive portal and the payment flow end to end — nothing in this guide
   exercises them, and no package install can.
8. Rollback executed on a router with a real user configuration in place.
9. Whether 24.10 or earlier can run the package at all (it is untested and
   unsupported — §1).
10. The final supported-architecture matrix (§1), and therefore which
    architectures may be announced as tested rather than "built, untested".
