# Release process (tollgate-wrt)

Maintainer runbook for cutting and publishing a `tollgate-wrt` release:
the version rules, the pre-flight gates, the exact tag commands, and the
publish sequence.

Two gates are **operator actions** — an agent prepares the release and
stops there:

- pushing the **release tag**;
- **publishing** artifacts (feed files, signed indices, NIP-94 `1063`
  events) anywhere publicly retrievable.

Everything before those gates can be prepared and verified by any
contributor.

## Version single source of truth

The release version is written down in exactly one place: the
[VERSION](../VERSION) file at the repository root. Everything else
derives from it.

- **CI** ([.github/workflows/build-package.yml](../.github/workflows/build-package.yml))
  takes the version from the pushed tag (`GITHUB_REF_NAME`, so the tag
  name *is* the packaged version) and passes it to
  `-ldflags -X .../src/cli.Version=…`, to the `.ipk` control file and to
  the OpenWrt SDK Makefile. A guard step **refuses** a tag whose name is
  not byte-identical to `VERSION`, which is what stops a release from
  being published under a version nobody wrote down.
- **`scripts/build-sdk-package.sh`** takes `PACKAGE_VERSION` from its
  caller (CI passes the tag) and otherwise reads `VERSION` itself.
- **`packaging/files/etc/uci-defaults/99-tollgate-setup`** ships the
  placeholder `SETUP_VERSION="__TOLLGATE_VERSION__"`. The CI `.ipk`
  staging, the SDK `packaging/Makefile` (`.apk`) and
  `packaging/local-build-ipk.sh` each substitute the real version;
  running the script straight from a checkout falls back to the version
  the package manager recorded, so a bogus marker is never written.
- **`src/cli/version.go`** must keep the non-release sentinel
  `Version = "dev"`. A version literal there is a bug, not a
  convenience: it silently disagrees with the tag, and it is how a
  `v0.0.0` binary shipped in an earlier build.

Bump `VERSION` in the release commit, then verify from the repo root:

```bash
sh scripts/check-version-sync.sh                 # internal consistency
sh scripts/check-version-sync.sh v0.6.0-alpha2   # ... and tag == VERSION
```

`hooks/pre-commit` runs the first form whenever a version-carrying file
is staged. `scripts/check-version-sync.sh` also fails the tree when the
CHANGELOG section and the `RELEASE-NOTES.md` title do not carry the
version in `VERSION` — a stale release-notes document shipped once
already (the v0.5.0 document was still in the tree two minors later).

### Allowed version strings

The suffix has to be a **single** `-alphaN`, `-betaN`, `-rcN` or
`-preN`; `vMAJOR.MINOR.PATCH` with no suffix is the stable channel.
Anything else publishes into the wrong channel or breaks the package
version:

- CI picks the release channel
  (`c=` tag on the NIP-94 events) by regex on the tag name:
  `^v[0-9]+\.[0-9]+\.[0-9]+-alpha` → `alpha`, `-beta` → `beta`, bare
  `vMAJOR.MINOR.PATCH` → `stable`, **everything else** → `dev`.
- `packaging/normalize-apk-version.sh` accepts exactly
  `^[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc|pre)[0-9]*)?$` and maps
  `-alpha` to `_alpha` for apk; anything else falls through to the
  branch/PR fallback `0.0.0_git<height>-r0`, which is flatly wrong for
  a release.

So a two-part suffix such as `v0.6.0-rc-alpha1` is silently wrong twice
over: it publishes the first real alpha into the `dev` channel (already
polluted with per-commit builds) and produces an apk version of
`0.0.0_git<height>-r0`. If a literal `rc` in the name is ever required,
extend the channel regex to map `^v[0-9]+\.[0-9]+\.[0-9]+-(alpha|rc)`
to `alpha` **and** relax the apk normaliser to accept a single `-rcN`
suffix first.

## Release checklist

### 0. Operator go/no-go

- Confirm the target version string, and that it does not already
  exist: `git ls-remote --tags upstream | grep <version>` must be
  empty and `gh api repos/OpenTollGate/tollgate-module-basic-go/tags`
  must not list it. (Never reuse a tag: `v0.6.0-alpha1` exists and is
  bound to an older commit plus a 0-asset GitHub prerelease.)
- Confirm every blocking fix for this release is merged to upstream
  `main`, and that no open issue labelled blocking is waiting.
- Confirm the known-issue list in `RELEASE-NOTES.md` is accurate.

### 1. Pre-flight gates

From `src/` (each standalone module too), on the commit to be tagged:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test -race -count=1 -tags testenv ./...
```

From the repo root, when the change touches the config schema or the
captive-portal contract (these are CI gates):

```bash
node tests/contract/js-schema-lint.mjs
bash tests/contract/build-purity.sh
```

From the repo root, always:

```bash
sh scripts/check-version-sync.sh <version>   # includes tag == VERSION
```

If `go test` fails with `creating work dir: stat .../.gotmp-*`, a stale
`GOTMPDIR` is in `~/.config/go/env`; run with
`GOTMPDIR=/tmp/gotmp-cross TMPDIR=/tmp`.

### 2. Prepare the release commit

On a branch based on the **upstream** `main` tip:

1. `VERSION` holds the target version (e.g. `v0.6.0-alpha2`).
2. `CHANGELOG.md`: the accumulated `[Unreleased]` section becomes
   `## [<version>] - <date>`, and a fresh empty `## [Unreleased]`
   heading is left at the top for the next cycle. Update the compare
   links at the bottom of the file.
3. `RELEASE-NOTES.md`: rewritten for this release. Its title line must
   contain the version in `VERSION` (checked by
   `scripts/check-version-sync.sh`).
4. Re-run `sh scripts/check-version-sync.sh <version>` — every check
   must pass before the tag exists, because CI will run the same
   consistency from the other direction.

### 3. Tag on UPSTREAM main — never the fork

The fork is for working branches only. Every `v0.6.0*` and
`v0.7.0-alpha*` tag in existence lives on the fork and points at
branches that were never merged; tags pushed there are invisible on
`github.com/OpenTollGate/tollgate-module-basic-go` and cannot be
re-tagged, which is how a release gets orphaned.

```bash
# 1. Make sure the release commit is on upstream main.
git -C ~/repos/tollgate-module-basic-go fetch upstream --tags
git -C ~/repos/tollgate-module-basic-go log --oneline -1 upstream/main

# 2. Annotated tag on that exact commit (attach it to upstream/main, not HEAD).
git -C ~/repos/tollgate-module-basic-go tag -a v0.6.0-alpha2 \
    -m "tollgate-wrt v0.6.0-alpha2" upstream/main

# 3. Push the tag to upstream. Requires maintainer credentials: the
#    felixfelix-bot token gets "403 Permission denied" on OpenTollGate.
git -C ~/repos/tollgate-module-basic-go push upstream refs/tags/v0.6.0-alpha2

# 4. Prove where the tag lives and what it points at.
git ls-remote --tags upstream | grep v0.6.0-alpha2
git -C ~/repos/tollgate-module-basic-go rev-parse v0.6.0-alpha2^{commit}
git -C ~/repos/tollgate-module-basic-go rev-parse upstream/main
```

The last two must match before anything is announced.

### 4. Verify the tag build

The tag push is what triggers CI — but with GitHub Actions down since
2026-08-27 that means the **ngit lane**, not the GitHub workflow (see "The ngit
release lane" below for the stage sequence and how to read a run). A tag push
enqueues **both stages at once**, and stage 2's `resolve-inputs` can out-wait
stage 1's measured 11.8 min warm / 20.8 min cold — the poll window is ~20.6 min
worst case, bounded by the coordinator's enforced 1800 s job timeout, so warm
pushes resolve unattended while a cold push can still exhaust the window; if it
gives up, re-run stage 2 by hand — do not re-push the tag. Check, in order:

```bash
# 1. The runs exist and the VERSION guard passed: workflow results are kind 9842.
nak req -k 9842 -a 765cd47badcbbc4a38c7d0c57d5607663b484c20cd59773f9f7064487f9431e8 \
    -l 10 wss://relay.ngit.dev

# 2. Watch stage 1 finish at the tagged commit — the kind-30078 records:
nak req -k 30078 --tag "d=tollgate-build/<short-sha>/binaries" -l 1 \
    wss://relay.damus.io wss://nos.lol wss://nostr.mom

# 3. If stage 2 timed out waiting, trigger it by hand (maintainer key).
#    NOTE: bash, not sh — dash rejects the script's bash arrays and dies on it:
bash scripts/ngit-ci-trigger.sh .ngit/act/workflows/build-package.yml \
    <tagged-commit> refs/tags/<version>

# 4. The announced NIP-94 events carry the right version and channel. Filter by
#    BOTH release publisher keys, or the ngit-era releases are invisible.
nak req -k 1063 \
    -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
    -a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d \
    --tag n=tollgate-wrt --tag v=v0.6.0-alpha2 --limit 50 \
    wss://relay.damus.io wss://nos.lol wss://nostr.mom

# 5. The publication gate: every (arch, format) announced, every artifact on >= 2
#    mirrors with the sha256 from the x tag. Non-zero exit = the version did not
#    reach the channel; the output names the missing pair or the failing mirror.
VERIFY_EXPECT="$(scripts/ngit-matrix-expectations.sh .ngit/act/workflows/build-package.yml)" \
    scripts/verify_publication.sh v0.6.0-alpha2 alpha -
```

Every artifact must be present for **every** architecture in the
declared matrix — a partial set (say 7 of 8) reads as a broken feed, and
`v` must be the tag name verbatim, `c` the intended channel. Download
at least one artifact from its `url` tag and check the sha256 against
the event's `x` tag before announcing anything (the gate above does the whole
matrix for you).

### 5. Publish and announce

- Feed publishing (package index + rollback path) is its own runbook
  and its own gate: nothing goes public until the feed index and the
  Nostr artifacts are the same bytes for the same version and arch.
- Announce only the architectures whose acceptance test actually
  passed. Everything else is published as "built, untested" — never as
  supported.
- `RELEASE-NOTES.md` and the CHANGELOG section are the announcement
  text; the CHANGELOG is the per-PR record.
- **Open the tester intake channel as part of the announcement.** Create the
  pinned issue described in [`docs/tester-intake.md`](../docs/tester-intake.md)
  §1 from the paste-ready title and body in its Appendix A, pin it, and link
  it in the announcement next to the tester guide. Create it when the feed is
  live, not earlier — a channel that points at a release nobody can install
  yet is worse than no channel. This step needs a maintainer account: the bot
  token is read-only on upstream.
- A partially-published release is worse than a late one. If the
  publish is interrupted, say so; do not let an index advertise
  artifacts that are not retrievable.

### 6. Post-release

- Close the release-request issue (currently #339) with a link to the
  tag and the release page.
- Watch the intake channel. Every qualified report becomes exactly one
  kanban card on the release board, titled with its severity label and the
  reporter's architecture — `S1`/`S2`/`S3` plus the arch, e.g.
  `S2 aarch64_cortex-a53` — and worked in severity order, S1 first
  ([`docs/tester-intake.md`](../docs/tester-intake.md) §4.3). An S1
  wallet/funds report is a stop-ship: pull the feed index *before*
  investigating (§4.2) — a step for the **feed operator** named in the feed
  runbook (§5), executed through that runbook, not for whoever is triaging the
  report. Do not open a second tracker for the same reports;
  the card is the tracker, the intake thread is where the tester sees the
  tag echoed back.
- Leave a fresh empty `## [Unreleased]` heading in `CHANGELOG.md` for
  the next cycle (step 2.2 assumes it exists).
- Record anything that could not be verified on hardware, in the
  release notes, rather than in private notes.

## The ngit release lane (the lane that actually runs)

GitHub Actions is dead for this repository: runs have sat `queued` since
2026-08-27, so `.github/workflows/build-package.yml` — and the
`verify-publication` job #406 added to it — never executes. Since PR #410 the
lane that does publish is **ngit-ci**, reading `.ngit/act/workflows/` from the
ngit mirror at the pushed ref. Everything above still applies through it: the
version single source of truth, the tag/VERSION guard, "publish only what you
verified", and honest limitation notes.

| step | GitHub lane (dead) | ngit lane (live) |
| --- | --- | --- |
| build + announce | `build-package.yml` on the tag push | stage 1 (`build-package-binaries.yml`) then stage 2 (`build-package.yml`), at the same commit |
| start stage 2 on a ref its `on:` does not cover | `workflow_dispatch` | `scripts/ngit-ci-trigger.sh .ngit/act/workflows/build-package.yml <commit> <ref>` |
| publication gate | `verify-publication` job in `build-package.yml` | the same job in `.ngit/act/workflows/build-package.yml`, plus the standalone `.ngit/act/workflows/verify-publication.yml` |
| read the result | `gh run list` | `nak req -k 9842 -a <coordinator-hex> wss://relay.ngit.dev` |

Two properties of the port decide how a release is actually driven, and both are
measured, not assumed (`.ngit/README.md`):

- one `act` invocation — one workflow file — is bounded by the coordinator's
  1800 s job ceiling, and the full 14 `.ipk` + 3 `.apk` matrix does not fit it;
- a push to `main` or a `v*` tag enqueues **both** stage files at once while
  `resolve-inputs` polls for only 10 minutes, against a stage 1 that takes
  11.8 min warm / 20.8 min cold.

So the order is: stage 1, wait for its workflow result to read `success`, start
stage 2, then run the gate (it is also a job inside stage 2, but a stage 2 that
is cut off before it finishes the matrix never reaches that job):

```bash
# NOTE: bash, not sh — the trigger script uses bash arrays and
# `sh scripts/ngit-ci-trigger.sh` dies instantly with a syntax error on
# Ubuntu (dash).
# stage 1 — or just let the push trigger it
scripts/ngit-ci-trigger.sh .ngit/act/workflows/build-package-binaries.yml "$(git rev-parse <commit>)" refs/heads/<ref>
# stage 2, once stage 1 reported success
scripts/ngit-ci-trigger.sh .ngit/act/workflows/build-package.yml "$(git rev-parse <commit>)" refs/heads/<ref>
# the gate — the ref names the published version to verify (ngit-ci delivers no
# workflow inputs, so the ref *is* the parameter)
git push ngit HEAD:refs/heads/verify/<version>/<channel>/ipk    # ipk | apk | all
```

The gate is the same check #406 specified: every `(arch, format)` the release
matrix declares must have a kind-1063 announcement for the published
version+channel, and each artifact must be fetchable from >= 2 Blossom mirrors
with the sha256 in its `x` tag. A failure names the missing pair or the failing
mirror and exits non-zero, so the run is red — and it is tested against a version
that does not exist so that a vacuous pass is impossible. The expectations come
from the matrix in `build-package.yml` itself
(`scripts/ngit-matrix-expectations.sh`), never from what happened to be
published. Full detail, including the format scope and the negative controls:
`.ngit/README.md` → "Publication verification".

### The publish key era — what a consumer sees

The release history is signed by **two** keys, and only one of them can still
sign:

| era | key (hex) | state |
| --- | --- | --- |
| GitHub Actions, up to 2026-08-27 | `5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a` | the Actions secret is write-only over every API and exists nowhere on this fleet, so **nothing new will ever be published under it** |
| ngit / Nostr CI, from PR #410 | `6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d` | the dedicated CI release key; deliberately not the repository maintainer key (`36bdeb…`), which also signs the kind-30617 announcement of this repository |

What follows from that for anyone fetching a release:

- **Filter by both keys, or by no key at all.** A query pinned to
  `-a 5075e61f…` returns nothing published after 2026-08-27, so a consumer using
  the old example sees an empty release history even for a version that was
  published. The kind-1063 tags (`n`, `v`, `c`, `A`, `format`, `compression`)
  are publisher-independent and are the durable way to find artifacts. `AGENTS.md`
  documents both keys with copy-pasteable `nak` queries.
- **A gate pass on the new key is a pass.** `scripts/verify_publication.sh`
  accepts either release publisher on purpose and prints which key signed each
  announcement it verified, plus an explicit note when an announcement did *not*
  come from the historical key — so a release that only the new key announced
  cannot be mistaken for one the old key announced.
- **Nothing published before the switch is invalidated.** Those events remain
  valid and fetchable; they simply carry the old signature.

## Traps

- **Tagging on the fork.** Orphaned tags, releases nobody can install.
  The fork is write-accessible to the bot token, upstream is not — that
  asymmetry is exactly why this happens.
- **Reusing an existing tag name.** `git tag -f` + force-push rewrites a
  published tag; consumers that already fetched it see a different
  commit. Cut a new version instead.
- **Two-part suffixes** (`v0.6.0-rc-alpha1`): wrong channel, wrong apk
  version (see above).
- **A version literal in `src/cli/version.go`** or in the setup script:
  the built package then disagrees with the tag, and
  `scripts/check-version-sync.sh` fails the tree by design.
- **Publishing before verifying the tag build.** The `1063` events are
  permanent records; an event announcing an artifact that 404s is worse
  than no event.
