# Contributing to TollGate

<!-- markdownlint-disable MD013 -->

TollGate turns an OpenWrt router into a Cashu-powered payment gateway
for internet access. Customers pay in sats (time- or data-based); the
router gates network access behind a captive portal and sweeps balances
to configured Lightning addresses. The same binary also acts as a
*client* to upstream TollGates, so a router can buy internet from
another TollGate and resell it automatically.

The code is organized as Go modules under [src/](src/), split along
that merchant/client line:

- **Selling side** — [merchant](src/merchant/) prices advertisements,
  validates incoming Cashu payments, and drives Lightning payouts;
  [upstream_session_manager](src/upstream_session_manager/) owns the
  customer session lifecycle (time or bytes) and instructs the valve;
  [valve](src/valve/) is a thin wrapper over `ndsctl` that opens and
  closes the NoDogSplash gate per MAC.
- **Buying side (reseller mode)** —
  [upstream_detector](src/upstream_detector/) probes WAN interfaces for
  an upstream TollGate and decides whether to buy;
  [wireless_gateway_manager](src/wireless_gateway_manager/) handles
  Wi-Fi gateway selection, connection, and reseller-mode network
  orchestration.
- **Shared plumbing** — [config_manager](src/config_manager/) (schema,
  migrations, validation of `/etc/tollgate/config.json`),
  [tollwallet](src/tollwallet/) (Cashu wallet operations),
  [lightning](src/lightning/) (LNURL-p / Lightning address resolution),
  [cli](src/cli/) (the `tollgate` CLI), and
  [tollgate_protocol](src/tollgate_protocol/) (wire types).

Most non-trivial changes affect behavior that only shows up on a real
router — captive-portal gating, firewall and UCI state, Wi-Fi
handoffs, two-router autopay. A `go test` run is necessary but not
sufficient for that class of change; the physical-router test suite in
[physical-router-test-automation](https://github.com/OpenTollGate/physical-router-test-automation)
is where those regressions actually surface. This
document covers the workflow assuming that context. Wire-protocol depth
lives in the canonical spec repo
[OpenTollGate/tollgate](https://github.com/OpenTollGate/tollgate), module internals in
[docs/](docs/).

## Quick start

```bash
git clone https://github.com/OpenTollGate/tollgate-module-basic-go.git
cd tollgate-module-basic-go/src
go build ./...
go test -tags testenv ./...
```

Go tooling runs from [src/](src/), not the repo root. The Go version is
pinned in [src/go.mod](src/go.mod); CI installs exactly that version
via `go-version-file`. The `testenv` build tag provisions a hermetic
temp config dir so the main package's `init()` does not depend on
`/etc/tollgate/config.json` — letting the suite run off-router. The tag
is a no-op for subpackages.

To build an installable package (`.ipk` or `.apk`) locally, use
[scripts/build-sdk-package.sh](scripts/build-sdk-package.sh). It
cross-compiles the binaries, stages the canonical
[packaging/](packaging/) recipe into the OpenWrt SDK, and produces the
same artifact shape as CI.

For end-to-end runs against real routers, see
[physical-router-test-automation](https://github.com/OpenTollGate/physical-router-test-automation)
for the hardware test fleet and cloud lab.

## Choosing a branch to target

All development happens on **`main`**; every PR targets it. Releases
are cut as tags from `main` (`v0.5.0-alpha1` … `v0.5.0`), so there are
no long-lived release branches to choose between. If a fix needs to
land in a released version, say so in the PR and the maintainers will
handle the tagging.

## Reporting bugs

Search [open issues](https://github.com/OpenTollGate/tollgate-module-basic-go/issues)
before filing a new one.

When you open a bug report, please include:

- **Package version** (`tollgate version`, or `opkg info tollgate-wrt`
  / `apk info tollgate-wrt`)
- **OpenWrt release and hardware** (router model, architecture, and
  whether you installed the `.ipk` or `.apk`)
- **Topology** — single router selling access, or a two-router
  reseller setup (which is its own class of bug)
- **What you expected to happen** — your mental model of the behavior,
  ideally referencing the relevant docs or config field.
- **What actually happened** — the observed behavior, including the
  surprise.
- **Reproduction steps** — minimal and deterministic if you can.
  Two-router bugs should describe both routers' roles and config.
- **Evidence** — relevant log excerpts (`logread -e tollgate` or
  `tollgate logs`), `tollgate status` output, and config excerpts from
  `/etc/tollgate/config.json`. **Redact secrets before posting**:
  `identities.json` holds Nostr private keys, and logs can contain
  Cashu tokens, which are bearer instruments.

One issue per bug. Don't bundle unrelated symptoms even if you suspect
they share a root cause — the maintainers will link them if they turn
out to be related.

## Submitting pull requests

### Scope discipline

Every PR should make one logical change. The reviewer should be able
to read the whole diff and trace every line back to the PR's stated
purpose.

- No drive-by reformatting of unrelated files.
- No unrelated refactors folded into a bug fix or a feature PR.
- No "while I was in there" cleanups in files outside the change's
  natural footprint. Send them as separate PRs; they'll usually land
  faster on their own.
- Pre-existing warnings in files you didn't touch are not yours to fix
  in this PR.
- Do not commit planning documents (`*-plan.md`, `TODO-*.md`, scratch
  notes, agent working files). Only production documentation belongs in
  the tree: `README.md`, `CHANGELOG.md`, protocol specs, module docs.

### Required before opening any PR

Run these locally, from [src/](src/), and confirm they all pass:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test -race -count=1 -tags testenv ./...
```

CI runs the test suite per-module with `-race`, so a data race in any
touched package will fail the build.

If your change touches the config schema or the captive-portal
contract, also run the contract checks from the repo root (these are
CI gates):

```bash
node tests/contract/js-schema-lint.mjs
bash tests/contract/build-purity.sh
```

**Recommended for router-visible changes**: build a real package with
[scripts/build-sdk-package.sh](scripts/build-sdk-package.sh), install
it on a test router, and exercise the affected flow — at minimum the
purchase path (the
[physical-router-test-automation](https://github.com/OpenTollGate/physical-router-test-automation)
suite automates it if you have a fleet configured). Captive-portal,
firewall, Wi-Fi, and session-lifecycle changes cannot be validated any
other way.

### Self-review against the project review checklist

The 13-criteria checklist the maintainers run on every incoming PR is
published at [PR-REVIEW.md](PR-REVIEW.md). Run your own change through
it before opening — or hand the document to your coding agent with
"review my branch against this checklist" and let it do the pass. The
checklist covers PR hygiene (body, commit shape, base freshness), diff
content (does the change do what the description says, does it fit the
codebase as a natural extension), and cross-cutting concerns (tests,
docs, dependencies, security, contributor-conventional Go patterns).

This is the first thing a maintainer does on any submission, so
running it yourself saves a review round trip.

### Additional requirements for feature PRs

- **Test coverage.** Features added without a test that exercises them
  won't be reviewed. Unit tests live next to the code under `src/`;
  hardware flows get a pytest case under `tests/`. Coverage of just the
  happy path is fine for an initial PR; edge cases can land as
  follow-ups.
- **Documentation updated alongside the code.** Wire-protocol changes
  update the relevant spec in the canonical spec repo
  [OpenTollGate/tollgate](https://github.com/OpenTollGate/tollgate).
  Config changes update the schema in
  [config_manager](src/config_manager/) *with a migration* and the
  example in [README.md](README.md). Behavior visible to operators
  updates [README.md](README.md) and the module doc it touches, plus a
  [CHANGELOG.md](CHANGELOG.md) entry.

### Additional requirements for bug-fix PRs

- **A regression test** where practical. If a regression test isn't
  tractable (some bugs only surface on real hardware, under Wi-Fi
  timing, or in two-router topologies), say so in the PR description
  with a one-paragraph explanation.
- **Commit message references the bug**: the symptom, the root cause
  in one sentence, and the fix shape.

### Merge mechanics

PRs are merged via **squash-merge**. One logical change per PR becomes
one commit on `main`, which keeps `git bisect` useful. Your in-PR
commit history doesn't matter for the final landed history — the
maintainer rewrites the commit message at merge time.

## Security: never derive secrets from public keys

A Nostr public key (npub) is broadcast to relays and is world-readable.
Anyone who learns a router's npub can compute anything derived from it.
**Passwords, API tokens, and any other secret material must be derived
from the private key (`hexPrivKey`), never from the public key or npub.**

This bug was introduced and fixed in PR #193 (see issue #209): the
original `DeriveRootPassword` and `DeriveWiFiPassword` took `pubKeyHex`,
making root and WiFi passwords computable from the npub alone. The fix
changed both to take `hexPrivKey`. Public attributes (IPv4, MAC) remain
npub-derived — only secrets moved to the private key.

**Rule of thumb**: if the function name contains "password", "token",
"secret", or "key" (as output, not input), its input must be the private
key, not the public key. The same applies to Spilman channel keypairs in
tollgate-rs and any other Nostr-based identity scheme.

## AI coding assistant policy

Use of AI coding assistants (Claude Code, Copilot, Cursor, Aider, and
similar) in preparing a contribution is welcome. These tools are force
multipliers and we have no objection in principle to their use in
writing code, tests, documentation, or PR descriptions.

What we require is that the contributor does a thorough manual review
and editorial pass over the output before submission. Concretely:

- Verify that the code does what it claims, not just that it compiles.
- Verify that any tests the agent wrote actually test something
  useful, not just that they pass.
- Verify that any documentation matches the behavior.
- Spot-check the diff for nothing-surprising: no unrelated files
  modified, no fabricated APIs, no references to symbols that don't
  exist, no version bumps you didn't intend, no planning documents or
  agent scratch files, no churn outside the change's natural footprint.
- Be ready to discuss the design choices in the PR as if you wrote
  every line, because for the purposes of accountability you did.

The coding agent is a tool. The contributor is the author of record
and is accountable for whatever they submit. PRs are reviewed on what
they contain, not on who or what wrote them.

**Review effort scales with submission effort.** A submission that
shows signs of being unreviewed agent output — irrelevant edits
scattered across the tree, hallucinated function names, mismatched
test/behavior pairs, fabricated API references, ChatGPT-style summary
prose in comments — will receive an AI-coding-agent reply in turn,
without human review. If you want a human reviewer's attention, do the
editorial pass yourself first.

Repeated submissions of unreviewed AI output will result in the
contributor being asked to step back and may result in account
restrictions.

## Where the conversation happens

- **GitHub issues** — bugs, feature requests, design discussions that
  don't fit on a specific PR.
- **GitHub PRs** — design discussion specific to a change in flight.
  Comment threads on the diff are the right place to push back on a
  decision.
- **[tollgate.me](https://tollgate.me)** — the project site; broader
  project conversation and announcements happen there and on the
  project's Nostr presence.

For implementation questions specific to your PR, ask in the PR
itself. For design or roadmap questions that don't have a clear PR
home yet, file a GitHub issue.

## Further reading

- [PR-REVIEW.md](PR-REVIEW.md) — the 13-criteria PR review checklist
  maintainers run on every incoming PR; run it yourself before opening
  to save a round trip.
- [OpenTollGate/tollgate](https://github.com/OpenTollGate/tollgate) — the
  canonical TollGate wire-protocol specs (TIPs, HTTP, NOSTR, WIFI).
- [docs/](docs/) — module internals: merchant, upstream session
  manager, data-session management, wireless gateway manager.
- [physical-router-test-automation](https://github.com/OpenTollGate/physical-router-test-automation) —
  the hardware test fleet and end-to-end suites.
- [README.md](README.md) — module map, installation, configuration
  reference.
