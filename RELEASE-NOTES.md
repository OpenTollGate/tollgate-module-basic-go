# TollGate v0.6.0-rc1 (tollgate-wrt)

**Released**: 2026-10-05
**Channel**: `rc` — a feature-frozen release candidate. The feature set is
frozen; only stop-ship fixes will change before `v0.6.0` stable.

<!-- markdownlint-disable MD013 -->

`v0.6.0-rc1` is the release candidate cut after the fund-safety blockers
measured by the conformance lane were fixed. Its theme is the same as
alpha4's — *funds safety and honest failure* — carried to the three
invariants the Go line must hold before it can call itself a payment
gateway:

1. **One economic payment buys at most one economic session grant** — even
   when the same note is submitted twice concurrently (#639).
2. **An ambiguous Cashu outcome never re-exposes deterministic derivation
   outputs** — a processed swap whose response was dropped is UNKNOWN, not
   FAILED, and the same blinded messages are never re-sent (#640, the
   #257/#266/#480 wallet-brick class).
3. **A received payment is owed service or a recoverable claim** — a
   gate-open failure after a successful `Receive` becomes a durable,
   restart-surviving owed entitlement that converges when NoDogSplash
   accepts (#403).

Everything else in this RC is hardening: test credentials out of the
public tree, a commit-derived release epoch, config validation that
refuses dead credentials, and one secret-redaction extension.

## Fund-safety fixes (the release blockers)

- **Concurrent duplicate payment refused before the mint (#639).** The
  conformance lane measured one note POSTed twice in parallel granting
  **two** sessions (2× allotment). `PurchaseSession` now marks the note
  in flight from the moment the money-moving call starts until its result
  is consumed; a concurrent second submission is refused locally
  (`payment-duplicate-inflight`), before any money moves. Sequential
  duplicates are still refused by the mint as already-spent, unchanged.
- **No same-body retry after an ambiguous mint outcome (#640).** The
  wallet client re-sent an identical money-moving POST body on network
  errors — so a mint that processed a swap and dropped the response
  received the same blinded messages again, which a strict mint answers
  with error 10002 (wallet brick). Fixed in `gonuts-tollgate` v0.12.2
  (network errors return an ambiguous-outcome error immediately; the 429
  same-body retry is kept, as a rate-limit answer precedes processing).
  The customer-facing notice says the outcome is unknown and **not to
  resend the note** (`payment-outcome-unknown`), and an unanswered
  request no longer condemns a healthy mint in the health tracker.
- **Owed entitlements (#403).** A successful `Receive` followed by an
  `ndsctl auth` failure used to leave the value with the operator and the
  customer with nothing. The paid purchase is now recorded durably
  (`owed-grants.json`, atomic + fsync, written before the response),
  retried with backoff until it succeeds or its window passes, reloaded
  at startup, and applied exactly once. The customer is told access will
  start automatically and **not to pay again**
  (`payment-received-grant-pending`). Refund (returning the value)
  remains deliberately out of scope: an entitlement that expires ungranted
  is a loud operator action, recorded and kept for audit.

## Release hardening

- The hardware-fleet tests no longer ship router/Wi-Fi credentials or a
  hardcoded artifact URL (#509/#528; rotate previously-exposed values).
- ngit stage 1 stamps `SOURCE_DATE_EPOCH` from the commit, never the
  runner clock (#529) — reproducibility on the lane that actually
  publishes.
- `config set private_key` refuses out-of-bounds passphrases at write
  time instead of persisting credentials the applier refuses forever
  (#636).
- `config get` no longer returns the identities' Nostr private keys:
  blanked, reported via `secret_set`, preserved on save (#635).
- `make release-check VERSION=v0.6.0-rc1` orchestrates every pre-release
  gate (Go battery, contract, packaging, the three invariant groups, the
  conformance fast subset, release-matrix cross-check, reproducibility,
  version consistency) into one `READY FOR HARDWARE` verdict.

## Config schema changes

None versus alpha4 — schema stays `v0.0.9`. Deployed `config.json` files
upgrade unchanged. (The `private_key` field gained enforced bounds; a
valid deployed config was already inside them.)

## Experimental / unsupported

- The wallet sidecar (`WalletPort` over AF_UNIX) remains scaffolding:
  unit-tested, not router-validated, not a supported backend.
- Reseller/two-router mode and Lightning (LNURL-p) sales are shipped as
  before; hardware acceptance for this RC covers the Cashu path first.
- `tollgate-clientd` is not part of this release.

## Known limitations

- No refund path for an expired owed entitlement (operator action; the
  record and reference are kept).
- Session metering is still process-memory state: a restart loses
  in-flight usage accounting (known gap, unchanged).
- Payout melts and drain ignore mint input fees (#414); expiry of
  keysets can still strand balances (#417).
- **The conformance lane measures two `service-or-refund` rows as red,
  and they are the honest state of the kill/timeout windows**: when the
  daemon dies between the mint's acceptance and the session grant
  (`pay-kill-post-receive-pre-session`), or the swap response is dropped
  after processing (`swap-timeout-retry`), the token is spent at the mint
  while the customer has no session. The output-re-use and double-count
  invariants PASS on every row; closing the service arm requires the
  designed business-transaction record (#502): a new WalletPort
  checkstate surface, a durable receive-intent journal, and an explicit
  operator policy for granting against value recoverable only via
  NUT-09 restore — deliberately not improvised before this release
  (#497's research-first rule). The late-outcome journal for a `Receive`
  that outlives its deadline likewise records the outcome for the
  operator but does not auto-grant (#498/#502).
- **The min_steps wire default is a live spec question**: TIP-02 carries
  a tentative `default 0` while this implementation defaults/floors to 1
  (clients reject 0; a crash-reset router advertising 0 bricks
  discoverability). Tracked upstream as
  [OpenTollGate/tollgate#20](https://github.com/OpenTollGate/tollgate/issues/20)
  with a proposal to de-tentative to 1 — this release matches the field
  evidence that proposal cites.
- **Wallets cannot initialize on jffs2-overlay devices** (squashfs NOR
  targets — ipq40xx, small-NOR ath79, mt76x8 class): bbolt requires a
  shared mmap jffs2 has never supported, so the wallet stays in degraded
  mode while everything else appears healthy
  ([#583](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/583),
  open). Devices with NAND/UBI overlays (the tested representatives) are
  unaffected; the fix is wallet-backend work, tracked post-0.6.
- **The `.apk` lane cannot install on stock OpenWrt 25.12**: nodogsplash's
  `iptables-*` dependencies are absent from every 25.12.5 feed, so an
  `apk add` fails unless the operator stages them manually
  ([#552](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/552),
  open — an upstream feed gap, not fixable in this tree). OpenWrt 24.10
  (`.ipk`) is the installable line for this RC.
- A module restart leaves clients NoDogSplash still holds as
  Authenticated outside any session (free access until their NDS
  sessiontimeout); the inverse-drift reconciliation that closes this is
  written
  ([#619](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/619))
  and deliberately deferred past the freeze.
- nodogsplash on OpenWrt 22.03 has the pre-auth DNAT translation issue
  (#398); 24.10+ is the tested line.

## Hardware support

- **TESTED (this RC's acceptance targets)**: `aarch64_cortex-a53`
  (mediatek-filogic class — Cudy WR3000 v1 with the `upx-ultra-brute`
  variant on 16 MB flash, COMFAST CF-WR632AX), and `ramips-mt7621`
  (`mipsel_24kc`) representatives. Acceptance runs use the release
  artifact by hash through the router-happy-path harness.
- **BUILDABLE, not yet verified on hardware for this RC**: the remaining
  matrix legs (`aarch64_cortex-a72`, `arm_cortex-a7`, `mips_24kc`,
  `x86_64`, apk formats). They are published but must not be called
  supported until their acceptance passes.
- Distinguish: **BUILDABLE** = CI produced the package; **TESTED** =
  the happy-path suite ran on a real representative; **SUPPORTED** =
  TESTED plus the full acceptance list (install, upgrade, same-version
  reinstall, downgrade/upgrade round trip, reboot, WAN/mint outage at
  startup, real payment, second payment, concurrent duplicate,
  usage exhaustion, forced gate-open failure/recovery).

## Upgrade notes

- **Back up the wallet directory and `/etc/tollgate/config.json` before
  upgrading.**
- Upgrade from `v0.6.0-alpha4` (or any `v0.6.0-alpha*`) is a plain
  package upgrade: `opkg install` / `apk add` the new artifact; existing
  configuration, identities and wallet are preserved; expect a setup
  re-run (`/tmp/tollgate-setup.log`, root-only).
- Same-version reinstall and downgrade/upgrade round trips are exercised
  by the acceptance harness (setup-marker ordering protects the
  operator's SSID on rollback).
- Do not install artifacts whose kind-1063 event you cannot verify:
  download from a `url` tag and check the file's sha256 against the `x`
  tag, for BOTH publisher keys.

## Getting v0.6.0-rc1

- Packages are announced as NIP-94 kind-`1063` events (`n=tollgate-wrt`,
  `v=v0.6.0-rc1`, `c=rc` — filter by both publisher keys; see
  [AGENTS.md](AGENTS.md) for the exact `nak` queries).

  ```bash
  nak req -k 1063 \
      -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
      -a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d \
      --tag n=tollgate-wrt --tag v=v0.6.0-rc1 --limit 50 \
      wss://relay.damus.io wss://nos.lol wss://nostr.mom
  ```

- No events for this version means the release is not published — a
  failed shard suppresses all announcements by design.
- **Reporting problems**:
  [docs/tester-intake.md](docs/tester-intake.md) names the single intake
  channel and the report template;
  [docs/rc-tester-guide.md](docs/rc-tester-guide.md) documents
  install/upgrade/rollback. Any wallet/funds symptom is **S1 =
  stop-ship**.

## Verification status

- Unit/race across all 16 modules, contract, packaging and pipeline
  suites: green on the release commit (`make release-check`).
- The three fund-safety invariants: pinned by dedicated regression tests
  (each reproduced red before its fix) and by the conformance fast
  subset lane.
- Hardware acceptance: pending for this RC — that is what the RC cycle
  is for; see "Hardware support" above for what is claimed and what is
  not.

The per-PR engineering record is [CHANGELOG.md](CHANGELOG.md).
