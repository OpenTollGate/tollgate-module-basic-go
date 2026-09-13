# Metrics we might be overlooking — final ranked list

> **Consultant C, task T4 (final).** This replaces the seeded placeholder list.
> The seed items are kept and re-ranked; the "genuinely missing" additions are
> marked **[NEW]**. Each entry states why it matters *for this specific
> decision*, how it is measured, and what a bad value looks like. Metric IDs line
> up with `measurement-protocol.md`; where an entry needs a procedure, the
> protocol is authoritative.
>
> **A caution the ranking is built around:** the operator's question was framed
> as *"how do these two wallets perform on the router (memory, storage, threads)
> and how do they compare on reliability?"* — but the two candidates are not the
> same kind of thing (one is ESP32 firmware with no licence file, the other is a
> Rust ALPHA library requiring an integration architecture), so a
> resource-consumption comparison is close to meaningless until §"Cost of being
> wrong" is settled. The ranking below deliberately puts the metrics that can
> *disqualify* ahead of the metrics that only *differentiate*.

---

## Ranked summary

| # | Metric | Class | Measured by |
|---|---|---|---|
| 1 | Licence and grant of rights | **DECISION-DRIVING / disqualifier** | E2 |
| 2 | Migration + rollback + seed custody of existing value | **DECISION-DRIVING / disqualifier** | M1, M2, E11 |
| 3 | Failure-mode symmetry under the mandatory fault set | **DECISION-DRIVING / disqualifier** | R1–R8, S4 |
| 4 | Integration-architecture economics (build + CI + cross-compile) | **DECISION-DRIVING / disqualifier** | E4, E5, F10 |
| 5 | User-visible feature/NUT parity, including the known adapter holes | **DECISION-DRIVING** | E10 |
| 6 | Seed/key material at rest **[NEW, promoted]** | disqualifier (funds) | E11 |
| 7 | Flash wear: bytes written per payment + erase budget | differentiator | S2, S5 |
| 8 | Runtime model and resources on the target (RSS/PSS/threads/startup/FDs/CPU) **[NEW: tokio runtime]** | differentiator | F5–F8, P1, P4 |
| 9 | FFI/language-boundary hazard behaviour **[NEW]** | disqualifier | E12 |
| 10 | Concurrency and lock contention on the target CPU **[NEW]** | differentiator | C1–C3 |
| 11 | Upgrade/rollback safety and per-release binary growth **[NEW: split from #2]** | disqualifier | M3, F3, F9 |
| 12 | Debuggability and support telemetry on device | differentiator | E9 |
| 13 | Build reproducibility, dependency graph, and supply chain | differentiator | E1, E3 |
| 14 | Test story: hermetic fake-mint harness we can run in CI | differentiator | E8 |
| 15 | Bus factor, governance, churn — the "will we fork again" test | disqualifier in the long run | E6, E7 |
| 16 | Deterministic builds and signed artifact provenance **[NEW]** | differentiator | E3, E5 |
| 17 | Thermal and sustained-load behaviour **[NEW]** | differentiator | P5 |
| 18 | Cost of being wrong in either direction **[NEW: framing metric]** | the weighting itself | §Cost of being wrong |

---

## 1. Licence and grant of rights — DECISION-DRIVING

Pick this first. Everything else is meaningless if the answer here is "no".

- **Why it matters for THIS decision.** The two candidates sit at opposite
  extremes. CDK lives under a real org with a `LICENSE.md` (GitHub reports
  `NOASSERTION`, so the *actual terms* must be read, and compatibility with the
  project's GPL-3.0 verified). **nucula has no licence file at all** (verified via
  the GitHub contents API) — absent a licence there is no grant of rights, so it
  cannot be vendored, linked, or derived from without the author's explicit
  permission, and a permissive-looking README is not a licence. Also remember the
  third option: staying on gonuts means *no new licence surface at all*, which is
  a genuine advantage of option (c) in the architecture comparison.
- **How to measure.** Read the licence file text (hash it, quote it). For
  `NOASSERTION`, walk the actual file; check for MIT/Apache-2.0 vs copyleft, check
  whether the Rust crates are uniformly licensed (`cargo metadata` / `cargo tree`
  per-package licence field — a wallet crates graph with a lone GPL or
  unlicensed transitive dependency is a shipping blocker), then do the
  GPL-3.0 compatibility analysis for the *combined work* being distributed as
  `tollgate-wrt` (statically linking a Rust lib into a GPL-3.0 Go binary is a
  distribution of a combined work; the FFI/cgo boundary does not change that).
  For nucula, the "measurement" is a written grant from the author, stored in the
  branch.
- **What a bad value looks like.** No licence file, or a licence that does not
  permit GPL-3.0-compatible distribution; a dependency with no licence at all;
  a "licence" that is only a README statement; or a licence whose attribution
  obligations the product cannot meet (no `--licenses` output, no docs page).

## 2. Migration + rollback + seed custody of existing value — DECISION-DRIVING

Real routers already hold proofs, and real users hold the value in them.

- **Why it matters for THIS decision.** This is not a greenfield install. The
  current wallet is a gonuts `WalletPath` store plus a merchant `quotes.json`
  (`src/merchant/merchant.go`), and the questions are: can the candidate read or
  import that, can the *existing* balance be spent after migration, what happens
  if the operator rolls back the package, and who holds the seed. The CDK adapter
  answers part of this already — it keeps a per-mint sqlite store and a
  `cdk-wallet-mnemonic.txt` whose own comment says *"losing it forfeits every
  derived balance"* — so a migration is a change of key custodian, not just a
  change of file format.
- **How to measure.** M1: migrate a captured (redacted) production wallet into the
  candidate and verify independently — proof-by-proof mint agreement via NUT-07,
  `quotes.json` still parses through the dual-format `MintQuoteState` unmarshaller,
  and the migrated balance can be spent end-to-end. M2: run the previous binary
  against the new data and confirm it either works or fails cleanly;
  an irreversible migration must be documented as one-way. E11: `stat` the seed
  file, check it is inside the `keep.d/tollgate` preserved set (`/etc/tollgate`),
  and test restore from backup in a clean environment.
- **What a bad value looks like.** Any satoshi unaccounted for; a proof whose mint
  state contradicts the local record; a migration that requires the user to
  re-enter a recovery phrase; a downgrade that corrupts or deletes data; a seed
  that a normal `sysupgrade` or config reset silently destroys; a seed that is
  plaintext and group/world-readable; no documented rollback at all.

## 3. Failure-mode symmetry under the mandatory fault set — DECISION-DRIVING

A wallet that loses proofs is worse than one that is 2 MB larger. This is the
metric the operator's word *"reliability"* actually means.

- **Why it matters for THIS decision.** The candidates fail differently, and the
  failures that matter are value-destroying, not performance-degrading. The
  in-tree adapter already shows the seams where failure lands: `Melt` explicitly
  notes that *"Paid-without-Confirm is a funds-loss bug (proofs reported spent
  while merely staged)"*; `getOrCreateWallet` re-creates a wallet handle lazily;
  `Shutdown()` destroys wallets under the same mutex that `GetBalanceByMint`
  holds through a CGO call (the comment names the use-after-free it is guarding
  against). nucula's failure surface is a battery-less ESP32 losing power
  mid-redeem. gonuts's failure surface is the offline-receive behaviour the fork
  exists for.
- **How to measure.** The eight mandatory scenarios in
  `measurement-protocol.md` §5, each ending in the three-way reconciliation
  check (local consistency, mint agreement via an independent oracle, restore
  from seed). Record pass/fail per scenario, not a narrative.
- **What a bad value looks like.** Any scenario where the wallet and the mint
  disagree about whether a proof is spent and no scripted recovery exists; a
  proof deleted locally after a network error; a 5xx treated as a terminal
  outcome; a malformed response causing a panic; a crash-loop on boot after a
  hard kill; a store that fails its own integrity check after power loss.

## 4. Integration-architecture economics — DECISION-DRIVING

The seam costs more than the footprint difference, and it is the thing that
decides whether the migration is a month or a year.

- **Why it matters for THIS decision.** CDK is Rust; the router binary is Go.
  The in-tree `cdk_wallet.go` already commits to option (a) (cgo + `cdk-go`
  FFI), but **the current build sets `CGO_ENABLED=0`** — in
  `.github/workflows/build-package.yml` and in
  `packaging/local-build-ipk.sh` — across all five CI targets (`arm64`, `armv7`,
  `mipsle-softfloat`, `mips-softfloat`, `amd64`). So the existing adapter has
  never been through the real build. Choosing CDK means changing the build
  contract for five architectures, or adding a second artifact and a supervisor
  (sidecar). Option (c) scores best here by construction: no new toolchain.
- **How to measure.** E4 (do the `aarch64`-musl and `mipsel`-musl artifacts
  *run* on-device, and how many patches), E5 (can one documented CI job build all
  five arches), F10 (added cold-build minutes per arch), P6 (seam overhead), and
  the §7 trade-off table.
- **What a bad value looks like.** A cgo path that links for `arm64` but cannot
  be built for `mipsel`; a build that needs hand-maintained per-arch toolchain
  images; a patch set so large it becomes a second fork; a sidecar with no
  `procd` supervision and no protocol-version handshake; a cross-compiled binary
  that links but segfaults on the target (wrong FP ABI on softfloat, wrong TLS
  model).

## 5. User-visible feature/NUT parity, including the known adapter holes — DECISION-DRIVING

A substitution that silently drops a feature is not a substitution.

- **Why it matters for THIS decision.** The in-tree CDK adapter is *partial*, and
  the gaps are user-visible, not cosmetic: `MeltToLightning` returns
  *"not yet wired"*; `SendWithOverpayment` **refuses** non-zero overpayment caps
  rather than honouring them; untrusted-mint swap-to-trusted is not implemented
  (per its own comment, untrusted tokens are accepted as-is); and the NUT-04
  wire format needed a deliberate dual-format unmarshaller to stay compatible
  with legacy gonuts data (`port.go`, `WIREFORMAT.md`). Meanwhile gonuts covers
  the paths the product actually runs today, plus the offline-receive behaviour
  the fork was made for.
- **How to measure.** E10: NUT number → supported by gonuts at the pinned
  revision → supported by the candidate → does the merchant/lightning code path
  depend on it. Cite file:line on both sides.
- **What a bad value looks like.** A gap on a path the product exercises today
  (melting to Lightning, receiving from an untrusted mint, overpayment caps),
  with no written acceptance of the behaviour change; or a spec-compliance
  "improvement" that changes the JSON written to disk and breaks a downgrade (the
  reverse of the dual-format compatibility work already done).

## 6. Seed/key material at rest — disqualifier (funds) **[promoted from a footnote]**

- **Why it matters for THIS decision.** The mnemonic **is** the money. The in-tree
  adapter writes it as a plaintext file at mode `0600` in the wallet directory
  and comments that losing it forfeits every derived balance. On a router the
  seed must survive `sysupgrade` (which keeps `/etc/tollgate`) but must *not*
  survive a factory reset into the hands of someone else, and it must never
  reach a log or a crash dump. Neither candidate ships a router-grade answer;
  gonuts has the same class of problem, so this is mostly a *new-risk-vs-old-risk*
  comparison.
- **How to measure.** E11: where the seed lives, `stat -c %a`, encrypted-at-rest
  or not, whether it is in `keep.d`, what a config wipe does, whether a backup
  and restore procedure exists and has been tested, and whether the seed appears
  in `logread` output or a core dump.
- **What a bad value looks like.** Plaintext seed readable by anything other than
  root; seed lost on a routine upgrade; seed regenerated silently when the file
  is missing (a new seed = all existing proofs orphaned, which is funds loss
  dressed as a fresh start); seed printed in logs; no restore test anywhere.

## 7. Flash wear: bytes written per payment + erase budget

- **Why it matters for THIS decision.** A router writes to JFFS2/UBIFS with
  limited erase cycles, and a router wallet does *thousands* of small writes over
  years. This is where a "better" wallet can be catastrophically worse: a sqlite
  file per mint (`<sha256(mintURL)[:8]>.sqlite` in the current CDK adapter) that
  commits on every operation, plus JSON quote persistence, can multiply
  small-write cost versus a design that batches. Likewise a sidecar that logs
  verbosely can wear the flash out through logging alone.
- **How to measure.** S2 (delta of `/proc/diskstats` sector writes across ≥ 20
  payment cycles, logical vs kernel amplification separated); S3 (store growth
  per 1 000 payments, fsync/commit frequency, compaction); S5 (the erase-budget
  formula: `erase_cycles × usable_flash / bytes_per_payment`, printed with the
  geometry used).
- **What a bad value looks like.** A whole-file rewrite per payment; unbounded
  log growth in the default configuration; no compaction/vacuum path; a wear
  projection that runs out inside the device's expected service life. Note: the
  flash geometry must come from the operator/datasheet — **ASSUMPTION A2** in the
  protocol (16–32 MB, ~10⁵ cycles/block) is a placeholder, and the wear number is
  only as good as that input.

## 8. Runtime model and resources on the target — differentiator **[tokio is the hidden one]**

- **Why it matters for THIS decision.** The operator asked directly about
  "memory, persistent storage, threads etc.". On a 64–128 MB device that also
  runs hostapd/uhttpd/nftables, the wallet's RSS and thread count can decide
  whether the *network* stays up under memory pressure. The overlooked part is
  not "how much" but "**what shape**": whether the wallet needs an async runtime.
  A Rust library that spins up a tokio multi-thread runtime inside a cgo call
  creates N worker threads whose N may be derived from the host's CPU count, and
  whose stacks land inside the Go process's memory — a cost that does not appear
  in any "library size" figure.
- **How to measure.** F5/F6 (RSS and PSS idle and at peak, per phase), F7 threads
  at idle and during each operation **with the runtime explicitly identified**,
  F8 FDs/sockets, P1 startup-to-wallet-ready with the WAN down (the existing code
  carries a TODO saying the wallet DB is not unlocked without a network
  connection — the new wallet must not inherit that), P4 CPU seconds per payment,
  and P6 seam overhead.
- **What a bad value looks like.** Idle threads > 32 or thread count scaling with
  the core count; peak RSS > ~25 % of device RAM; wallet-ready gated on mint
  reachability; FDs that never return to baseline; a payment that measurably
  steals forwarding throughput.

## 9. FFI/language-boundary hazard behaviour **[NEW]** — disqualifier

- **Why it matters for THIS decision.** Option (a) puts Rust and Go in one
  address space on a router. A Rust panic that is not caught at the boundary
  aborts the process — which is `tollgate-wrt`, i.e. the router's whole service,
  not just the wallet. The reverse direction matters too: the adapter's own
  comments show the authors already reasoning about use-after-free between
  `Token.Close()`/`Destroy()` and concurrent `Shutdown`. Neither concern exists
  at all in option (c), and it is *contained* in option (b) (a sidecar crash is a
  socket error, not a router outage).
- **How to measure.** E12, empirically: malformed input into the FFI surface, a
  forced Rust-side panic path, double-`Close()`, `Destroy` concurrent with an
  in-flight call; record whether the process aborts, unwinds, returns a typed
  error, or corrupts memory. Check the Rust build's `panic=abort`/`unwind`
  setting and whether the FFI layer installs a panic hook.
- **What a bad value looks like.** **Any** input that aborts or double-frees the
  router process; a panic message that gives a Rust symbol with no code location
  and no way to reproduce; a crash that takes the router's forwarding down;
  `Destroy` that is not idempotent; a token usable after `Close()`.

## 10. Concurrency and lock contention on the target CPU **[NEW]** — differentiator

- **Why it matters for THIS decision.** The router calls the wallet from several
  directions at once: per-quote monitor goroutines (`merchant/lightning.go`), a
  2-second data-usage ticker and per-mint health tickers (`merchant.go`), and
  user-facing portal actions. The in-tree CDK adapter serialises **everything**
  behind one `sync.Mutex`, and holds it *across* FFI calls in `GetBalanceByMint`,
  `GetAllMintBalances` and `getOrCreateWallet` — on a low-core-count device that
  means a local balance read can wait for a network round-trip to the mint.
- **How to measure.** C1 (two concurrent operations: serialise, deadlock, or
  corrupt — with `-race`), C2 (p95 latency of a cheap local read while a remote
  operation is in flight, **with the core count recorded**), C3 (monitor vs user
  action, and whether monitors outlive their quotes).
- **What a bad value looks like.** A deadlock or race-detector hit; a local
  read stalling for the duration of a remote operation; monitors that keep
  polling after their quote settles; any lost or double-credited payment under
  concurrency.

## 11. Upgrade/rollback safety and per-release binary growth **[split out of #2]**

- **Why it matters for THIS decision.** The product ships as a signed package to
  devices in the field, and the upgrade path is where migrations go wrong.
  `keep.d/tollgate` defines exactly what survives `sysupgrade`
  (`/etc/config/network`, `/etc/config/wireless`, `/etc/tollgate`) — a wallet
  store outside that set is wiped by design. Separately, the binary has to keep
  fitting: a wallet that grows the package monotonically will eventually make
  upgrades fail for lack of flash headroom, or force removing other features.
- **How to measure.** M3 (interrupt `sysupgrade`/`apk upgrade` mid-write on QEMU
  then hardware; verify recovery and data survival), M2 (previous binary against
  new data), F3 (`.apk` size delta), F9 (size trend across the last ~6 releases),
  and a CI-run `apk`/`.ipk` upgrade test rather than a fresh install
  (`scripts/build-sdk-package.sh` defaults to `PACKAGE_FORMAT=apk`; the product
  is OpenWrt 25.12.x/apk).
- **What a bad value looks like.** A normal upgrade that wipes wallet data; a
  half-upgrade with no recovery; an undocumented one-way data migration; a
  package that no longer fits the target's flash with upgrade headroom.

## 12. Debuggability and support telemetry on device

- **Why it matters for THIS decision.** Wallet failures happen on a customer's
  router, and the only channel back is `logread`. The genuinely overlooked
  question is: **can the on-device log distinguish "the mint rejected this" from
  "our code panicked"?** For option (a) the answer is not obvious (see #9), for
  option (b) you have two logs and a socket protocol to correlate, and for
  option (c) nothing changes. The related constraint: logs go to a flash-backed
  ring buffer, so verbosity is a wear and space question too (ties to #7).
- **How to measure.** E9: a level-controlled log line per wallet transition with
  the source identified; a symbolised crash path; a committed reproduction script;
  an on-device state/balance inspection command that does not require a debug
  build; and a measured bound on bytes logged per payment.
- **What a bad value looks like.** Opaque aborts; logs that cannot attribute a
  failure to a source; the only way to see wallet state being a full debug build
  with symbols; log volume with no level control; a support procedure that
  requires the device to be shipped back.

## 13. Build reproducibility, dependency graph, and supply chain

- **Why it matters for THIS decision.** The whole motivation is to stop owning a
  fork; a candidate that cannot be rebuilt deterministically, or that drags in a
  large unreviewable graph, recreates the same kind of liability. Note the
  router already carries a heavy transitive Lightning graph through gonuts, so
  the honest comparison is *delta*, not absolute (`go list -m all` vs
  `cargo tree -e normal,no-dev`, counting only what actually links).
- **How to measure.** E1 (dependency count + licence per dependency, for the
  feature flags actually used), E3 (two clean builds of the same revision
  byte-compared; attempt to reproduce a published upstream binary).
- **What a bad value looks like.** Non-reproducible builds with no documented
  method; absolute paths or build timestamps embedded in the shipped binary; a
  linked-dependency graph too large to review, or with an unlicensed / GPL-only
  transitive crate buried in it.

## 14. Test story: hermetic fake-mint harness we can run in CI

- **Why it matters for THIS decision.** Fault injection (§3) is only repeatable if
  a mock mint exists. If we have to build it ourselves, that is real work we are
  taking on — and it is the difference between "we can test this before shipping
  it to a router" and "we find out on a router". Two in-tree signals to start
  from rather than assume: `tests/cloud-lab/` already runs a mint container with
  failure scenarios (`Dockerfile.mint`, `test_mint_failure.py`,
  `test_smoke_payment.py`), and `tests/setup_cdk_testing.yml` implies CDK testing
  scaffolding was already contemplated. CDK's workspace also contains
  `cdk-fake-wallet` and `cdk-integration-tests`. Whether any of these is usable
  as *our* hermetic fault-injection harness is a measurement, not an assumption.
  Existing in-tree unit tests (`cdk_wallet_test.go` and the
  compatibility/vector/bench suites) are unit-level and appear not to exercise a
  real mint or FFI failure paths.
- **How to measure.** E8: run the candidate's own suite *and* one of our routing
  tests against its fake mint on the build host; record which wallet-visible
  paths are covered (mint quote, mint, swap, melt, restore, checkstate); check
  whether the suite is hermetic (no live public mints).
- **What a bad value looks like.** Tests that only run against live mints;
  a "fake wallet" that fakes the *wallet* instead of the *mint* (so it cannot
  test our storage or network behaviour); no restore/checkstate coverage; a suite
  that cannot run in CI at all.

## 15. Bus factor, governance, churn — the "will we fork again" test

- **Why it matters for THIS decision.** This is the *actual* objective of the
  whole exercise: escape a fork we maintain. A candidate that is present but
  unstable just relocates the problem. Objectively: CDK is maintained under the
  reference Cashu org (the "maintained by a proper Cashu developer" criterion) but
  self-describes as **ALPHA** — *"the api will change and should be used with
  caution"* — and nucula is a personal project (6 stars, bus factor 1, no
  licence). Relocating from a fork of a slow-moving library to an ALPHA API with
  monthly breaking changes may be **worse** than the fork we have; that
  possibility must be stated, not hidden.
- **How to measure.** E6 (breaking changes on the wallet API surface across the
  last ~10 releases — would our adapter have needed a code change more than once
  a month?), E7 (`git shortlog -sne` over 12 months, merge rights, release
  cadence, issue latency, funding).
- **What a bad value looks like.** A breaking change in most releases; one author
  with no succession and no licence grant; a cadence that has already stalled
  relative to the fork; no release process we can pin to.

## 16. Deterministic builds and signed artifact provenance **[NEW]**

- **Why it matters for THIS decision.** CI already signs release events with a
  publisher key and publishes per-arch artifacts to Blossom (see AGENTS.md), and
  it cross-compiles five arches. Any new integration must fit that pipeline: a
  Rust object produced outside it breaks the provenance chain ("what exactly is
  in this binary we just shipped to a thousand routers?"). Two artifacts
  (sidecar) doubles the signing and version-skew surface.
- **How to measure.** E3 (deterministic rebuild + hash compare) and E5 (is the
  artifact produced by the documented CI job, from a pinned revision, with a hash
  that matches the published metadata?).
- **What a bad value looks like.** A wallet artifact built outside CI, from a
  floating dependency, with no hash published; per-arch binaries that differ
  between rebuilds with no explanation; a sidecar whose version is not bound to
  the package version.

## 17. Thermal and sustained-load behaviour **[NEW]**

- **Why it matters for THIS decision.** A payment gateway does continuous small
  crypto work on a passively-cooled device that is simultaneously forwarding
  traffic and running a Wi-Fi AP. Sustained-load testing (not idle sampling)
  is the only way to see CPU contention, throttling and forwarding loss. It also
  exposes the difference between a wallet that is idle-cheap and one that polls
  the mint continuously (per-quote monitors, health tickers).
- **How to measure.** P5: 200 consecutive payments while sampling thermal zones,
  load average, per-core idle, and forwarding throughput.
- **What a bad value looks like.** Any throttling event attributable to the
  wallet; sustained load above the core count; measurable forwarding/throughput
  loss during payment bursts; a wallet whose idle polling keeps the CPU awake.

## 18. Cost of being wrong in either direction **[NEW — the weighting metric]**

- **Why it matters for THIS decision.** The failure modes are asymmetric, and
  pretending otherwise is how bad architecture decisions get made. Choosing a
  wallet that loses proofs loses **customer money**, irreversibly, and destroys
  trust in a payments product. Choosing a worse architecture costs **engineering
  time**, which is recoverable — and staying on gonuts costs *nothing new today*
  while leaving the fork's maintenance burden in place.
- **How to measure.** It is not measured; it is *stated*. The recommendation must
  declare an explicit weighting (e.g. "value-safety and migration outrank
  footprint and maintainability by a factor of N") and then show which option
  survives that weighting. Every metric above should be read through it.
- **What a bad value looks like.** A recommendation that ranks a candidate on
  bytes of RSS while its migration path or fault behaviour is unmeasured; a
  "faster/lighter wins" conclusion reached before §2/§3 are answered; a
  comparison whose axes differ between the two candidates (e.g. ESP32 firmware
  footprint vs router RSS) presented as though it were a like-for-like result.

---

## Re-ranked, downgraded, or rejected from the seed list

| Seed item | Disposition |
|---|---|
| Licence compatibility | **Kept, rank 1.** Unchanged in substance: it precedes performance. |
| Architecture cost, not benchmark cost | **Kept, rank 4**, and sharpened with evidence: the existing CI runs `CGO_ENABLED=0` across all five arches, so the in-tree cgo adapter has never actually been built by CI. |
| Flash wear | **Kept, rank 7.** Promoted to include the erase-budget formula and log-writing wear. |
| Failure-mode symmetry | **Kept, rank 3**, and turned into the mandatory 8-scenario suite. |
| Upgrade path for existing users | **Split:** migration/rollback/seed custody at rank 2, upgrade safety and binary growth at rank 11. |
| Debuggability on the device | **Kept, rank 12**, sharpened to "can the log tell us *who* failed". |
| Build reproducibility + supply chain | **Kept, rank 13**, and split off deterministic/signed provenance at rank 16. |
| Test story | **Kept, rank 14**, with the hermetic-fake-mint criterion made explicit. |
| Bus factor honesty | **Kept, rank 15**, with the ALPHA warning quoted against the "maintained by a proper Cashu developer" criterion. |
| **Raw performance benchmarks** | **Downgraded.** Latency and CPU per payment (P2–P4) are kept as *regression gates* against the gonuts baseline on the same device, but they are explicitly **not** decision-driving: both candidates' performance differences are likely to be dwarfed by the architecture choice (#4) and by fault behaviour (#3). |
| **"Which language / how many stars / last commit date"** | **Rejected as metrics.** These may inform *questions* (E6/E7) but GitHub metadata is not a measurement. Two facts do survive as evidence: nucula has no licence file, and CDK self-describes as ALPHA. |
| **Nucula footprint (IRAM/DRAM)** | **Rejected for this decision.** It is ESP32-C3 firmware that does not run on a router; an ESP32 footprint number cannot be compared with router RSS. Relevant only if the operator redefines the goal as a device-side wallet product. |

---

## The one metric most likely to be overlooked, in one line

**Failure-mode symmetry measured as "does the router wallet's idea of a spent
proof ever disagree with the mint's, and can it recover by itself"** — because it
is invisible until it costs a customer money, it is invisible to any benchmark,
and it is the only axis on which every option (CDK-cgo, CDK-sidecar, un-forked
gonuts, nucula) fails in a different way.
