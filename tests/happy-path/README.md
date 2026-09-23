# Happy-path regression suite (published artifact)

Boots a **published** TollGate package and checks that the customer-facing happy
path still works. Everything it exercises comes from inside the artifact:

* the module binary — `<artifact>/extract/usr/bin/tollgate-wrt`
* the captive-portal SPA — `<artifact>/extract/etc/tollgate/tollgate-captive-portal-site`

It contacts nothing outside the machine, needs no secret, no Cashu token and no
payment. That is what makes it runnable in CI on the bytes a user would install.

## Run it

```bash
# 1. extract the published package (OpenWrt .apk is a custom v3 format, NOT
#    gzip/zstd - the .ipk IS a tarball)
docker run --rm -v /var/tmp/hp-artifact:/out alpine:edge sh -c \
  'apk add --no-cache apk-tools-static && /sbin/apk.static extract --allow-untrusted \
    --destination /out /out/tollgate-wrt_<version>_<arch>.apk'

# 2. run the suite (HP_PYTHON must be a python with Playwright for the portal checks)
HP_PYTHON=/home/c03rad0r/tg-e2e/venv/bin/python \
  bash tests/happy-path/run.sh --artifact /var/tmp/hp-artifact --out /var/tmp/hp-green
```

Exit 0 = happy path intact; non-zero = broken. Every check prints one line:

```
HPCHECK <id> <PASS|FAIL|SKIP> <detail...>
HPRESULT total=23 pass=20 fail=0 skip=2 known=1
HPEXIT 0
```

Options: `--artifact DIR` (required) · `--out DIR` · `--strict` (promote
absent-but-expected surfaces such as `/session-state` and the in-page renewal CTA
from SKIP to FAIL — use it once a release is known to ship them) ·
`--skip-portal` / `--skip-module` · `--keep`.

## What it covers

* **Artifact identity** — the module binary and the portal bundle are present in
  the package, and their sizes/hashes are printed (so a run is attributable).
* **Environment** — the artifact's binary runs natively (musl x86_64 loader),
  the **reused** `tests/cloud-lab/fake-ndsctl.sh` seam is on PATH and records
  calls (self-test), and an **offline stub mint** is up.
* **API contract** — `GET /` is `kind:10021`, `/whoami`, `/balance`, `/usage`
  shapes, the `/ln-invoice` no-quote **400 status-poll contract**, and
  `/session-state` when the artifact has it.
* **Enforcement** — no gate moves on an unredeemable token; the gate DOES open
  on a recognised payment, proven by the log the fake `ndsctl` wrote.
* **Portal in a real browser** — purchase UI renders, the mint list comes from
  the artifact's own advertisement, the device MAC is read from the live module,
  the purchase CTA is present, the granted-session card appears, and the expired
  view plus the in-page renewal CTA when the bundle ships them (portal #60).

## What it does NOT cover

* **Real silicon.** No kernel, nftables/fw4, conntrack, `ndsctl` or Wi-Fi
  behaviour — the artifact runs in userspace with a faked `ndsctl`. Router-level
  testing stays with `physical-router-test-automation`.
* **Real mints and real ecash.** The mint is a local stub and no payment is ever
  settled; a customer paying a real mint is not exercised here.
* **`/session-state` and the renewal CTA on pre-#541 artifacts.** They are
  reported as SKIP with the reason (measured on `v0.6.0-alpha4-pre15`: the mux
  falls through to `/`). Re-run with `--strict` once a release ships upstream
  #541's endpoint; the skip is honest coverage, not a pass.
* The installer, the feed publish, image/vendor drift, and the module's own Go
  unit/contract suites (`go-battery`).

## Known issues (tolerated, not fixed here)

`known-issues.txt` lists checks that are understood, reproducible and NOT fixed
by this suite. A known issue still prints `HPCHECK ... FAIL`, but does not make
the run exit non-zero — a suite that is permanently red on a pre-existing defect
stops being read. `--strict` promotes every entry back to fatal, and deleting the
line makes it fatal again, so that file is the complete list of what the suite
tolerates and is meant to shrink.

Current entry: **`portal:lightning-lane-against-live-module`**. The portal echoes
the mint URL from the advertisement's `price_per_step` tag verbatim (the shipped
default config writes it without a trailing slash) while the wallet keys a
registered mint as `"<url>/"`, so `POST /ln-invoice` answers
`400 {"error":"failed to create lightning invoice"}` (`error="mint does not
exist"`). The Cashu lane is unaffected — it does not take a mint URL from the
client — which is why the operator's manual pre15 pass (token paste) succeeds.
Fix direction: normalise the mint URL on both sides of the lookup
(`tollwallet.MintURLMatches` / `normalizeMintURL` already exist for this class).

**Caveat on that entry:** in the live-module Lightning check the client MAC does
not resolve inside the harness (`mac=00:00:00:00:00:00`), so that single failure
is over-determined. The trailing-slash mechanism is the one demonstrated by the
2x2 matrix recorded in `known-issues.txt` (config with/without slash x request
with/without slash); a fix must re-verify with a resolvable MAC.

## Evidence from the first run (2026-09-23)

* **GREEN** against the published `v0.6.0-alpha4-pre15` `x86_64` `.apk`
  (sha256 `e724c74ea108d159f6d8f5ddbb221cbf2b2f740381f5ad85f6f332bb76f5b533`,
  9,136,019 bytes; fetched twice and matched against the release API's digest):
  `total=23 pass=20 fail=0 skip=2 known=1`, exit 0.
* **RED** against a mutant artifact whose `usr/bin/tollgate-wrt` boots and serves
  `:2121` but answers the wrong shapes: `total=15 pass=8 fail=6 skip=1`, **exit 1**
  — and `boot:module-listening` still PASSES, which is the point: a suite that
  only asserted "something is listening" would have gone green.
