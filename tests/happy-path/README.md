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
HPRESULT total=24 pass=23 fail=0 skip=1 known=0
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
  shapes, the `/ln-invoice` no-quote **400 status-poll contract**,
  `/session-state` when the artifact has it, and the **client-identity
  contract**: every client-scoped route names the client it answered for
  (`X-TollGate-Client-MAC`) and reports a `?mac=` claim it did not honour
  (`X-TollGate-Mac-Claim-Ignored`) instead of dropping it silently. The probe
  names a MAC it is not using, and the money route is probed with an empty body,
  so nothing can be redeemed.
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

**The list is currently empty.** The one entry it carried,
`portal:lightning-lane-against-live-module`, was fixed on 2026-09-23: the portal
echoes the mint URL from the advertisement's `price_per_step` tag verbatim (the
shipped default config writes it without a trailing slash) while the wallet keys
a registered mint as `"<url>/"`, so `POST /ln-invoice` answered
`400 {"error":"failed to create lightning invoice"}` (`error="mint does not
exist"`) and a default install could not sell via the Lightning lane at all. The
module now canonicalises the client-supplied mint URL — through the same
`tollwallet.NormalizeMintURL` / `MintURLMatches` mint identity the registry keys
derive from — before the lookup. `known-issues.txt` keeps the write-up of the
defect and the fix for the record.

### Precondition for the live-module Lightning lane: a resolvable client

The lane POSTs `/ln-invoice` and the module resolves the client's identity from
the **socket** (`clientMACFromSocket`, `src/main.go`), refusing the request when
it cannot: a client-supplied `mac` is not an identity. Off-router the harness's
browser talks to `127.0.0.2`, which appears in no lease and no ARP table — which
is exactly how the old entry's failure became over-determined. To exercise the
lane, seed the module's own lease source (`dhcpLeasePath`, default
`/tmp/dhcp.leases`) with the client address first:

```bash
printf '1700000000 02:00:00:00:00:20 127.0.0.2 hp-client *\n' > /tmp/dhcp.leases
```

With that in place the lane reaches the mint, and the check asserts the real
thing: the module answers 200 with a bolt11 for the un-slashed URL the portal
sends. Without it, the lane fails on identity (`device-unresolved`) rather than
on the mint URL — an honest failure, but not this suite's subject.

## Evidence from the first run (2026-09-23)

* **GREEN** against the published `v0.6.0-alpha4-pre15` `x86_64` `.apk`
  (sha256 `e724c74ea108d159f6d8f5ddbb221cbf2b2f740381f5ad85f6f332bb76f5b533`,
  9,136,019 bytes; fetched twice and matched against the release API's digest):
  `total=23 pass=20 fail=0 skip=2 known=1`, exit 0.
* **RED** against a mutant artifact whose `usr/bin/tollgate-wrt` boots and serves
  `:2121` but answers the wrong shapes: `total=15 pass=8 fail=6 skip=1`, **exit 1**
  — and `boot:module-listening` still PASSES, which is the point: a suite that
  only asserted "something is listening" would have gone green.

## Evidence from the fix run (2026-09-23, pr/mint-url-normalisation)

The defect the first run tolerated, and its fix, measured on the same harness with
the client identity resolvable (the precondition above):

* **RED** — the published `v0.6.0-alpha4-pre15` `x86_64` package re-extracted from
  its `.ipk` (module sha256 `87dacb0ee7497e82f2ebe858ff4fdf0a700924cbe8597aa48a9e8309382f7add`):
  `HPRESULT total=23 pass=20 fail=0 skip=2 known=1`, `HPEXIT 0` — the same
  `portal:lightning-lane-against-live-module` FAIL, with the portal's own request
  recorded in the evidence as
  `POST …/ln-invoice?mac=00%3A00%3A00%3A00%3A00%3A00 -> 400 … [sent: {"amount":210,"mint_url":"http://127.0.0.1:<port>"}]`
  (no trailing slash) and the module answering `error="mint does not exist"`.
* **GREEN** — the branch built for the same `x86_64` target
  (module sha256 `fb669fe772dc3bca1fe345fa834e6802991ba6c39628638ad45e3624f185b688`;
  the portal half is the published bundle, unchanged) with the entry deleted from
  `known-issues.txt`: `HPRESULT total=23 pass=22 fail=0 skip=1 known=0`,
  `HPEXIT 0`, and the lane's own line reading
  `portal:lightning-lane-against-live-module PASS granted=True; … -> 200 {… "invoice":"lnbcstub1qqqq…"}`.
  The two checks that moved are that lane (FAIL → PASS) and `api:session-state`
  (SKIP → PASS, because the branch also ships upstream #541's endpoint that pre15
  predates).

The same shape, for the **socket-identity contract** (`api:client-identity`, added
2026-09-26). One extracted package root, only the module binary swapped:

* **RED** — the package's own binary
  (sha256 `bfdee306f2cef2ea79117e4c24b76fcff898e905d9841e5d4012a1b07f8250b5`):
  `HPRESULT total=24 pass=20 fail=2 skip=2 known=0`, **exit 1**, with
  `api:client-identity FAIL … GET /whoami reported the claim as '' (want 02:11:22:33:44:55 …)`
  on `/whoami`, `/balance`, `/usage`, `/session-state` and `POST /`.
* **GREEN** — this branch's `x86_64` build dropped into the same root:
  `HPRESULT total=24 pass=22 fail=1 skip=1 known=0`, with the check reading
  `api:client-identity PASS a ?mac= claim is reported as IGNORED … and the identity
  named is the socket's (02:00:00:00:00:20)`. The single remaining FAIL is
  `portal:expired-view`, identical in BOTH runs — pre-existing on this package root,
  not this change.
