# Router happy-path harness (real hardware)

The operator's manual pass over a flashed MT3000, as a script that exits non-zero
when the box is not what it claims to be.

```bash
# against a real router, comparing it to the package you think you shipped
bash tests/router-happy-path/run.sh --apk /path/to/tollgate-wrt_<ver>_aarch64_cortex-a53.apk

# the vantage is derived (guest unless :8090 answers); name it when you know it.
# A guest-side run is the expected default for a tester; run this one as well when
# the admin board is part of what you are gating:
bash tests/router-happy-path/run.sh --apk <pkg> --vantage mgmt

# no hardware needed: prove the harness itself still detects breakage
bash tests/router-happy-path/selftest/run_selftest.sh
```

Read-only by default. The only write is one `POST` with an **empty body** (it
carries no proof, so it cannot redeem anything); it exists to prove the payment
lane rejects a tokenless request. Nothing else mutates the router, and the
default run spends nothing.

```
RHPCHECK identity:portal:entry PASS splash.html 2959 B sha256 8268215ee9043879 == package copy
RHPCHECK identity:portal:assets PASS 17/17 shipped assets byte-identical on http://192.168.1.1:2051
...
RHPRESULT total=58 pass=47 fail=0 skip=11 warn=0
RHPEXIT 0
```

## Why this exists

Two real findings from 2026-09-23 were invisible to every check we had:

1. **A shipped bundle that did not contain the fix its pin claimed.** The version
   string, the changelog and the release notes all agreed; the bytes on the router
   did not match the bytes in the package.
2. **A portal served on a different port than assumed.** The surface the operator
   looked at was not the surface the package feeds.

Neither is visible to an "is something listening" smoke test, and neither is
visible to anything that reads a version number. Both are visible to a byte
comparison, which is what section 1 does: it fetches every asset the router
serves and compares each one, by leading sha256, against the **same path** inside
the supplied package.

## What it asserts, in order

`vantage` says which vantage can assert the row: **both** = a guest-side run
asserts it in full, `mgmt` = private network or on-box only (see the vantage
section below; a guest-side run is the expected default for a tester).

| # | section | vantage | what must be true |
|---|---|---|---|
| 0 | preflight | both | TCP liveness on 22 / 80(the captive port) / 2050 / 2051 / 2121 / 8080 / 8090 / 443, **each port retried** (a single burst races the box), and the box is **idle** (`session_active=false`) before anything below is attributable |
| 1 | build identity | both (admin sub-lane: mgmt) | every shipped portal + admin asset is byte-identical to the package; every reference the live entry document makes resolves inside the package; the entry chunk is content-hashed; optional `--expect-entry` pin |
| 2 | surfaces | both, except `:8090` | `:80` 307s to `:2050/splash.html?redir=…`; `:2050` is the cache-bust **stub** and its own resolved redirect target is `:2051/splash.html?_cb=…`; `:2051/splash.html` serves the SPA; `:2121/` is `kind:10021`; `:8080` 307s to https **and that target answers 200**; `:8090` serves the admin SPA (**mgmt**); a br-lan client must get **nothing** from `:8090` (**guest**, `surface:8090-admin-spa-not-guest-reachable`); the SPA entry is **not** also served on `:2050` |
| 3 | captive chain | both | an unauthenticated deep-path request is 307'd to the splash with the original URL URL-encoded in `redir=`; following the stub's own expression lands on the SPA (200, `id="root"`); the stub keeps a `<noscript>` fallback that works |
| 4 | API shapes | both | `/` `kind:10021` in **full** mode (mints advertised), `/whoami`, `/balance`, `/usage`, `/session-state`, `/identity`, CORS preflight for the cross-origin portal→API call, and the **socket-identity contract**: a `?mac=` the caller sent is reported as ignored (`X-TollGate-Mac-Claim-Ignored`) while `X-TollGate-Client-MAC` names the client the module answered for |
| 5 | Lightning quote | both | `GET /ln-invoice` with no quote is `400 {"error":"quote is required"}` — **that is a status poll, not a fault** — and it does not grant access |
| 6 | money path | both | an empty-body POST is rejected (`400 kind:21023`), and an opt-in paid purchase (see below), which asserts against the module's OWN answer (`paid:grant-identity`) instead of the `?mac=` the harness sent |
| 7 | on-box (opt-in) | mgmt | with `--ssh`: installed package version, on-box file hashes vs the package, and a **non-empty** live `backend_input_firewall` chain |

Check ids are stable and greppable (`identity:*`, `surface:*`, `captive:*`,
`api:*`, `ln:*`, `money:*`, `paid:*`, `paid2:*`, `ssh:*`, `net:*`, `pre:*`,
`vantage:*`). Every id except the ones listed as SKIP below is fatal on FAIL; a
**WARN** (`warn=N`, `RHPWARNED`) is reported, greppable, and never fatal.
**PROVISIONAL** is the one status that is not a verdict — it is printed as an
`RHPPROVISIONAL <id> ...` **note**, not as an `RHPCHECK` line at all, and only
section 0 prints it (the liveness burst, which cannot know what the rest of the
run will reach). The verdict always follows with a real `RHPCHECK` line for the
same id, so every id has exactly one `RHPCHECK` line per run and a gate that
greps `^RHPCHECK <id> ` sees one answer, not two.

## Vantage: what a run can assert, and from where

A tester's machine is a client on the guest network (`br-lan`). That is the
vantage a human actually has, so it is the default: `--vantage auto` (the
default, also `RHP_VANTAGE`) probes `:8090` — if it answers an HTTP request the
run is **mgmt**, otherwise it is **guest**. `--vantage guest|mgmt` overrides the
derivation, and either way the transcript's first line says which one this run
resolved to and why.

| check | guest (`br-lan`, the tester's default) | mgmt (private network / on-box) |
|---|---|---|
| `net:tcp-*` | asserted, retried | asserted, retried |
| `identity:portal:*`, `surface:2051-*`, `captive:*`, `api:*`, `ln:*`, `money:*` | asserted in full | asserted in full |
| `identity:admin:*` | **named SKIP**: `:8090` is unreachable from `br-lan`, so there is nothing to compare. The SKIP names the guard file and the lane that does assert it | asserted in full |
| `surface:8090-admin-spa` | not run | asserted: `200` + a content-hashed entry chunk |
| `surface:8090-admin-spa-not-guest-reachable` | asserted: **PASS on `000`**, FAIL if the board answers at all | not run |
| `paid2:*` (opt-in, see below) | asserted — this is the seat the lane requires | **FAIL**, named `paid2:vantage`: the shipped enforcement rule matches `iifname "br-lan"` only, so from here the egress probe does not traverse the gate and would answer `204` on a box whose gate is shut. Nothing is sent |
| `ssh:*` | opt-in (`--ssh`) | opt-in (`--ssh`) |

`:8090` is blocked for `br-lan` clients **by design**
(`packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft`, #566),
so a guest-side probe correctly gets nothing. The guest lane therefore asserts
the **absence** rather than dropping the port: `surface:8090-admin-spa-not-guest-reachable`
PASSES on `000` and FAILS if anything answers, and an `RHPNOTE` states that the
admin SPA itself is the mgmt/on-box lane's assertion. Nothing is skipped
silently, and the mgmt assertion is **not** weakened for the guest lane's
benefit: from the private network (or on-box with `--ssh`) `:8090` must serve the
admin SPA, and that is the check a release gate should use.

**A guest-side run is the expected default for a tester or the review club.** Run
the mgmt lane as well when the admin board is part of what you are gating.

## The section-0 TCP burst is retried, and a race is not a defect

The liveness burst is the **first** thing that touches those ports, right after
the previous section's work, on a box that may still be converging. Measured on
the bench MT3000: `:22` was reported dead by the burst *during a run that held an
SSH session to that very port*, and `:443` was reported dead and then answered
`200` later in the same transcript. Same class as the documented `429`.

* every port is probed up to `RHP_TCP_TRIES` (default 3) times, `RHP_TCP_BACKOFF`
  (default 1 s) apart, with `RHP_TCP_PACE` (default 0.25 s) between ports. A port
  that answers on a later attempt PASSES, and the line names the attempt that
  answered.
* a port that fails every attempt gets an **`RHPPROVISIONAL` note** (not an
  `RHPCHECK` line, so it can never be mistaken for the verdict) and is not counted
  in the totals. Before the summary, the verdict resolves every such port against
  the rest of the run: if a check that **demonstrably reached that port** PASSed
  later, the id becomes a **WARNING** that quotes that PASS. A preflight line the
  run itself refutes must never be a red line, and a transcript must never hold a
  FAIL for a port the run went on to use.
* **credit comes from a completed request, never from an id that merely names the
  port.** A check records reach evidence when it completes a request against a
  port (`reach <port> <id>`), and the port is taken from the URL the request
  actually landed on — so `surface:8080-target-200`, whose fetch follows a `307
  Location`, credits wherever that Location pointed, not `:443`. A dead `:443`
  therefore stays fatal even when the rest of the run is healthy; a
  `grep`-based rule of the shape "some PASS id mentions the port" would demote it.
* a port that answers **nowhere** in the run stays fatal, and the FAIL is printed
  by the verdict (`no other check in this run completed a request against :<port>
  either: this is FINAL, not a race`), so each `net:tcp-*` id has exactly one
  `RHPCHECK` line per run: PASS, WARN, or FAIL. The `net:tcp-*` ids are not
  decoration: they are what says the box is up at all.
* a port whose lane is not running is reported without being fatal, and the
  reason is printed: with no `--ssh`, `:22` is a WARNING (`no phase of this run
  depends on :22 -- the on-box SSH lane is opt-in`), and it is fatal again the
  moment you pass `--ssh`.

## The paid lane is OPT-IN, and nothing here touches ecash by default

```
RHP_CASHU_TOKEN      the operative gate. Unset => no token is ever sent.
RHP_SPEND_MAX_SATS   required whenever a token IS supplied: the harness refuses
                     to redeem anything without an explicit ceiling.
```

With `RHP_CASHU_TOKEN` unset (the default) the paid checks report SKIP with that
reason, and `paid:spends-nothing-by-default` records the fact. With a token set,
the harness re-checks the box is idle, refuses if the token's declared value
exceeds `RHP_SPEND_MAX_SATS`, refuses if this client's MAC cannot be resolved
(never redeem against the `00:00:00:00:00:00` sentinel), then POSTs the token to
`:2121/?mac=<mac>` and asserts `200 kind:1022` plus `session_active
false -> true`.

**Honest status of that lane.** It was exercised on hardware for the first time
on 2026-09-25 (pre17 on the bench MT3000, a 64-sat testnut token) — and it was
**dead before it could spend anything**: `lib/cashtoken.py` read the NUT-00
version character at `token[6]`, the first *payload* character, so every token
failed inspection with `unknown Cashu token version character 'o'`. The decode is
fixed (`token[5]` / `token[6:]`), and `selftest/cashtoken_selftest.py` now pins
both the good and the malformed path, so the lane cannot go dead silently again.
The lane itself remains code-reviewed rather than continuously proven: a real
purchase costs real sats, so only an operator run with a small token proves it.

## The SECOND purchase is OPT-IN too — and it is the club's main loop

Buying once is only half the happy path. The step allotment is 21 MiB, so a
reviewer spends the first one within minutes and buys again — and that second
purchase is what failed on real hardware (2026-09-25, pre17 on the MT3000): the
portal showed a **new** allotment, `/balance` agreed, and the client still had no
internet — no OS sign-in prompt either, so the client was neither redirected nor
served. The paid lane above only ever buys ONCE, so nothing in this suite could
see it. Hence `paid2:*` and `--second-purchase`:

```bash
# exhaust the first allotment first (browse/download your step size from the guest SSID),
# and run from that same guest seat: the lane's egress probe only means something
# where the enforcement rule applies (iifname "br-lan"), so it asserts --vantage guest
# and refuses to run (and to spend) from the management vantage.
sudo RHP_SECOND_PURCHASE=1 RHP_CASHU_TOKEN_2='cashuB...' RHP_SPEND_MAX_SATS=64 \
     bash tests/router-happy-path/run.sh --apk <published.apk> --second-purchase --vantage guest
```

| id | what it means |
|---|---|
| `paid2:vantage` | the run resolved to the **guest/client seat** — the only one that can carry this lane. The shipped enforcement rule matches `iifname "br-lan"`, so a probe from the management vantage never traverses the gate and could answer `204` on a box whose gate is shut; from anywhere but the guest seat this check is a named **FAIL** and **nothing is sent** |
| `paid2:first-allotment-spent` | the box reports NO active session. A "second purchase" on a live session is a renewal of an open gate, so the lane **fails** instead of pretending the run was valid |
| `paid2:token-supplied` | a second token was supplied (nothing is sent without it) |
| `paid2:spend-declaration` | `RHP_SPEND_MAX_SATS` is an integer and the second token is inside it — same guards as the first lane |
| `paid2:token-inspected` | the second token parses; its self-declared value is compared against the ceiling |
| `paid2:gate-shut-before` | the SAME egress probe, taken **before** the second token is posted, must answer anything **other than** `200/204`-with-no-redirect. The lane refuses to spend when it does not — no value moves — because the pair `gate-shut-before` → `gate-open` is a **transition**: a gate that was never shut cannot satisfy it, and "open after" would prove nothing about the re-purchase |
| `paid2:purchase-accepted` | `POST /?mac=<same mac>` → `200 kind:1022` |
| `paid2:grant-identity` | the module's **own** answer names the client the re-purchase was granted to (the signed `device-identifier` tag, else `X-TollGate-Client-MAC`), and it must be this run's `/whoami` MAC. The `?mac=` above does not decide it — the grant goes to the socket the request came from — so without this check a PASS could name an address the module never granted, and the gate checks below would not be this client's answer |
| `paid2:balance-restored` | `/balance` reports `session_active: true` **with a positive allotment** — that is the row's claim, and it is asserted rather than implied: an active session with `allotment: 0` is a different state and FAILs here |
| **`paid2:gate-open`** | **the customer's own data path**: `RHP_EGRESS_PROBE_URL` (default the Android probe) must answer **200/204 with no redirect**, and it is only read as a PASS when `paid2:gate-shut-before` observed the gate **SHUT** before the token was posted. This is the check the reported defect fails. A `307` to `:2050/splash.html?redir=…` means the client is still intercepted; **no answer at all** means it is neither redirected nor served — the two failure shapes are named in the FAIL detail, and the second one is the one the operator saw |

The point of the ordering: on the failing box `paid2:balance-restored` was
**green** and `paid2:gate-open` was **red**. A suite that stopped at the balance
would have called that box healthy — which is why the last check reads the wire,
not the module's memory of the session. And that last check is read only against a
gate that was seen **shut** first: `paid2:gate-shut-before` is the same probe taken
before the second token is posted, so what the pair asserts is the *transition*
(shut → open), which a gate that never closed cannot satisfy.

Requires the **guest seat** (a client on `br-lan`), an already-spent first
allotment, a second token and a reachable probe URL. `RHP_EGRESS_PROBE_URL` is
worth pointing at whatever the customer's OS actually probes (Android
`generate_204`, Apple `hotspot-detect.html`, Windows `connecttest.txt`, Firefox
`success.txt`): the check is the OS's own question, asked from the customer's seat.
The vantage is not a preference here but a precondition — `paid2:vantage` fails
from anywhere else, because the enforcement chain the probe is supposed to cross
matches `iifname "br-lan"` and a request from the management plane never enters it.

## The two purchase lanes are one-run-exclusive

`paid:*` and `paid2:*` buy for the **same client**, and the second lane's whole
value is that it starts from a state the *operator* reached: the first allotment
spent, the client deauthorised. If one run did both, the paid lane would buy
first and the second lane would then be reporting the state the harness itself had
just created — its precondition check would go red and read as the operator's box
failing.

So when the second purchase is requested (`--second-purchase` /
`RHP_SECOND_PURCHASE=1`), the paid lane **stands down**: `lib/api_check.py`
reports every `paid:*` id as a `SKIP` whose detail says one-run-exclusive,
`run.sh` prints an `RHPNOTE` saying `RHP_CASHU_TOKEN` is set and was **not sent**
(no value moved), and `paid:spends-nothing-by-default` says the same. Setting both
token variables in one run is a configuration error, and it is reported as one
instead of as a defect on the box.

The correct shape is two runs:

```bash
# run 1: buy once (and then spend the allotment by hand, from the guest seat)
sudo RHP_CASHU_TOKEN='cashuB…' RHP_SPEND_MAX_SATS=64 \
     bash tests/router-happy-path/run.sh --apk <published.apk>
# run 2: the re-purchase, once the box reports no active session
sudo RHP_SECOND_PURCHASE=1 RHP_CASHU_TOKEN_2='cashuB…' RHP_SPEND_MAX_SATS=64 \
     bash tests/router-happy-path/run.sh --apk <published.apk> --vantage guest
```

## Three traps this harness encodes on purpose

* **This firewall DROPS ICMP.** `ping` is not a liveness test here; a live router
  was once reported down by exactly that mistake. Every liveness decision in
  `run.sh` is a TCP connect, and `net:icmp-not-a-liveness-test` greps the
  harness source so a future edit cannot quietly reintroduce `ping`.
* **The module rate-limits its root handler, per client IP** — 10 requests/minute
  by default, `TOLLGATE_RATE_LIMIT_RPM` on the box. `GET /`, `/session-state`
  (which falls through to the root handler) and the payment `POST` all share that
  budget, so a **back-to-back** hardware run collects a `429`. A `429` here is a
  THROTTLE, not a regression, and painting ten red lines from one limit is
  precisely the confusion this harness exists to remove: `lib/api_check.py` and
  `run.sh` honour the server's `Retry-After`, retry, and then pace the rest of
  the run, and the transcript carries an `RHPNOTE` that says so. Two self-test
  cases cover both halves (`http-429-recovered`, `http-429-always`). If you do
  see a `429` survive the retries, wait a minute and re-run before reading it as
  a defect.
* **Router SSH is password/key gated.** The operator adds a key by hand (or types
  the password). All on-box checks are therefore behind `--ssh` / `RHP_SSH=1` and
  report SKIP otherwise — never a silent "pass".

Two smaller ones, both learned the hard way and both now asserted rather than
assumed: `:2051/` answers **403 by design** (the portal docroot has no
`index.html`; the entry document is `/splash.html`), and `:2050` is a **stub**,
so "the portal" must never be assumed to be one port.

Two more, each with its own section above: **the vantage** (a guest-side run
cannot assert `:8090` and says so out loud), and **the retried section-0 burst**
(a port that answers on a retry, or anywhere later in the run, is a WARNING, not
a fatal preflight failure).

## What it does NOT cover

* **Rendering.** No browser is launched. That the SPA *mounts* and that the
  purchase UI is usable is covered by `physical-router-test-automation`'s
  Playwright specs — this harness is the artifact-identity companion to those,
  not a replacement. Deploy/mutation stays with the kit: nothing here installs,
  flashes, reboots or reconfigures anything.
* **Real payments by default** (see above).
* **On-box state by default** — the ssh section is opt-in.
* **Multi-hop / chained TollGate rigs** and Wi-Fi-client behaviour.

## Known-honest SKIPs (not failures, not passes)

* `api:session-state` — SKIP when the served body is byte-identical to `GET /`
  (the Go default mux falls through; measured on the shipped `v0.6.0-alpha5`
  build). `--strict` promotes it to a FAIL, which is what you want once a release
  ships upstream #541.
* `api:identity-shape` — SKIP on 404 (the identity routes need a merchant key in
  `identities.json`).
* `identity:expected-entry` — SKIP without a pin. Set `RHP_EXPECT_ENTRY` (or
  `--expect-entry`) to `NAME:SIZE:SHA256` and the artifact's own entry chunk
  becomes part of the gate; that is the check that would have caught finding (1)
  above at release time.

## Self-test: the harness is verified without hardware

`selftest/run_selftest.sh` stands up a localhost stub (`selftest/stub_router.py`)
that answers every surface the harness asserts, from a throwaway fixture package.
It then runs the harness clean — must be GREEN — and once per mutation, e.g. a
flipped asset byte, a stub that stops redirecting, a captive port that stops
enforcing, a degraded advertisement, a sentinel MAC, an accepted empty token, an
`/ln-invoice` that answers 200 — and asserts that the intended check id
goes RED and the process exits 1. Two of the cases target the harness's own
tally: a helper that dies, and a helper that exits 0 having emitted nothing, must
each go red as `helper:<phase>` instead of quietly removing a whole phase's
checks from the count.

```
SELFTESTRESULT total=62 ok=62 bad=0
SELFTESTEXIT 0
```

It needs python3 (+ openssl for the TLS hop) and no network. It runs in CI. The
rule it enforces: *a check that has never been seen failing is decoration, not
evidence* — and a hardware-only suite rots precisely because nobody can see it go
red on demand.

Coverage is **measured, not claimed**, re-derived from a `--keep` run of this
commit on 2026-09-26: the 62 cases emit 77 distinct check ids between them — a
single clean run emits 60 on the `mgmt` lane this rig's baseline pins — and 52 of
them are driven red at least once. The 25 that never go red offline
are exactly the ones this rig cannot break, and they are named here so nobody has
to guess: the opt-in paid lane (`paid:*` — 6, needs `RHP_CASHU_TOKEN`), the on-box
lane (`ssh:*` — 4, needs a router key), the per-port liveness ids (`net:tcp-*` —
6 of 7: a stub that stops listening is not a state a single rig run can hold),
`vantage:mode` (reported, never fatal by construction), the ids inside the purchase lanes whose red path is "the operator did
not supply the token" (`paid:token-supplied`, `paid:session-flip`,
`paid2:requested`, `paid2:spend-declaration`, `paid2:token-inspected`,
`paid2:purchase-accepted`, `paid2:balance-restored` -- a missing token is
not a defect to model; the lane's own RED is `paid2:gate-open`, which is
driven red), the
`net:icmp-not-a-liveness-test` source guard itself (it can only go red if someone
reintroduces `ping`, which is the edit it forbids), and seven shape ids not yet
mutated (`api:whoami-shape`, `api:identity-shape` — SKIP on 404,
`artifact:package`, `captive:spa-noscript-fallback`,
`identity:admin:refs-in-package`, `ln:no-quote-not-granted`,
`surface:<luci>-luci-307`). Both purchase lanes now RUN offline on a fixture token
(`paid-lane-fixture`, `paid-lane-rejected`, `renew-gate-opens`,
`renew-gate-stuck`) — the control the paid lane never had, which is how a decode
bug that killed *every* token survived in a merged harness.
`selftest/run_selftest.sh --keep` leaves every per-case transcript behind, so the
list can be re-derived rather than trusted.

## Evidence: first runs on the bench (MT3000 @ 192.168.1.1, OpenWrt 25.12)

| run | artifact | result |
|---|---|---|
| GREEN | `tollgate-wrt_0.6.0_alpha5_aarch64_cortex-a53_portalcu102ln004.apk`, sha256 `45e7d1760767789a0d0d16be2c61c9a0af542625469f4d401bd98a4e91dd9969` | `total=58 pass=47 fail=0 skip=11`, exit 0 |
| NEGATIVE CONTROL (older package) | `tollgate-wrt_0.6.0_alpha4_pre15_aarch64_cortex-a53.apk`, sha256 `1eccfa74681875dc3181ca9ef9756426e49f0247bea6c3c575adb6f2baeaae79` | `total=65 pass=44 fail=10 skip=11`, **exit 1** |
| pin matches the artifact | alpha5 package + `RHP_EXPECT_ENTRY=index-ldb-r6jB.js:359545:8540e183…` | `identity:expected-entry PASS`, exit 0 |
| pin does NOT match the artifact | alpha5 package + pre15's `RHP_EXPECT_ENTRY=index-BDGoMmEt.js:360000:86129a08…` | `identity:expected-entry FAIL … the pin claims index-BDGoMmEt.js 360000 B`, **exit 1** |

The negative control is the one that matters: the same live router, the same
code, only an older package supplied. `identity:portal:entry` failed with

```
live splash.html is 2959 B sha256 8268215ee9043879 but the package ships 2959 B
sha256 8041e6566586c0ac (the router is NOT running the package under test)
```

and 5 assets that pre15 ships were `HTTP 404` on the box, i.e. the "shipped
bundle that is not on the router" class. Note that `surface:*`, `captive:*` and
`api:*` stayed green in that run — correctly: those surfaces *were* healthy, only
the identity was wrong. That is exactly the distinction this harness exists to
make.

The last two rows are the pin check used the way it is meant to be used at
release time: the pin the card quoted for pre15 (`index-BDGoMmEt.js`, 360000 B,
sha256 `86129a08…` — reproduced byte-exactly from the pre15 package during this
work) is *falsified* by the package actually deployed, with no router access
required to see why.

## Control pair: the socket-identity check, measured without hardware

`tests/happy-path/run.sh` now carries the same contract as `api:identity-contract`
(`HPCHECK api:client-identity`). Both were run on 2026-09-26 against one extracted
package root, swapping only the module binary, so the delta is attributable:

| binary | result |
|---|---|
| the published package's own (sha256 `bfdee306f2cef2ea79117e4c24b76fcff898e905d9841e5d4012a1b07f8250b5`) | `HPRESULT total=24 pass=20 fail=2 skip=2 known=0`, exit 1 — `api:client-identity` FAIL: *"GET /whoami reported the claim as '' (want 02:11:22:33:44:55 …)"* on all five client-scoped routes |
| this branch's `x86_64` build | `HPRESULT total=24 pass=22 fail=1 skip=1 known=0`, exit 1 — `api:client-identity` PASS (the identity named is the socket's `02:00:00:00:00:20`, the `?mac=` claim reported as ignored), and `portal:expired-view` FAILs identically in **both** runs, i.e. it is a pre-existing portal-lane red on that package root and not this change |

That is the whole point of the check: a rig posting a token "for" a MAC it is not
using used to get a grant for the SENDER's socket with nothing on the wire saying
so, and the rig read the result as "the gate never opened".

## Files

```
run.sh                      the harness (bash + curl; one summary, one exit code)
lib/apk-artifact.sh         .apk (ADB v3) / .ipk extraction, cached by sha256
lib/identity_check.py       served-bytes vs package-bytes fingerprint
lib/api_check.py            preconditions, API shapes, LN quote contract, paid lane,
                            second-purchase lane
lib/cashtoken.py            Cashu token inspection for the spend gate
lib/stub_chain.py           resolves the :2050 stub's own JS redirect expression
selftest/run_selftest.sh    offline GREEN/RED proof for every check id
selftest/stub_router.py     the localhost stub the self-test drives
selftest/cashtoken_selftest.py  pins the NUT-00 decode the spend gate depends on, and
                            emits the fixture token the offline purchase lanes use
```

Related: `tests/happy-path/` (same happy path, but a published artifact in
userspace with no silicon), `physical-router-test-automation` (deploy, flashing,
Playwright E2E, the hardware lock).
