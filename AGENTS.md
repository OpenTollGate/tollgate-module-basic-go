# AGENTS.md

<!-- markdownlint-disable MD013 -->

Instructions for AI coding agents working in this repository.

> **This file is committed and shared.** Every contributor's agent
> reads it. Do not edit it for personal, machine-local, or
> session-specific preferences — only change it when the guidance is
> meant to apply to everyone working on this repo. (`CLAUDE.md` is a
> local, gitignored symlink to this file.)

## Orientation

TollGate turns an OpenWrt router into a Cashu-powered payment gateway
for internet access; the same binary also buys access from upstream
TollGates (reseller mode). Read [README.md](README.md) for the module
map and configuration reference.

- Go code lives under [src/](src/); run all Go tooling from there,
  not the repo root.
- Much of the behavior only exists on a real router (captive portal,
  firewall, Wi-Fi, `ndsctl`). Unit tests passing does not mean a
  router-visible change works — say so honestly in PR descriptions.

## Contributing process

Follow [CONTRIBUTING.md](CONTRIBUTING.md). The parts agents most often
get wrong:

- **One logical change per PR**, targeting `main`. No drive-by
  reformatting, no unrelated cleanups, no scope creep.
- **Do NOT commit planning documents** (`*-plan.md`, `PLAN-*.md`,
  `TODO-*.md`, `MOCK-*.md`, scratch notes, agent working files). Add
  them to `.gitignore` instead. Only production documentation is
  committed: `README.md`, `CHANGELOG.md`, protocol specs, module docs.
- **No coding-assistant attribution** in commits or PR bodies — no
  `Co-Authored-By: Claude`, no `Generated with ...` footers.
- Before opening a PR, run from [src/](src/):

  ```bash
  gofmt -l .          # must print nothing
  go vet ./...
  go build ./...
  go test -race -count=1 -tags testenv ./...
  ```

  If the change touches the config schema or captive-portal contract,
  also run `node tests/contract/js-schema-lint.mjs` and
  `bash tests/contract/build-purity.sh` from the repo root.
- PRs are squash-merged; the maintainer rewrites the final commit
  message.

## PR review requirements

The 13-criteria checklist maintainers run on every incoming PR is
[PR-REVIEW.md](PR-REVIEW.md). Before opening a PR (or after pushing a
substantial revision), review the branch against that checklist and
fix or pre-empt what it surfaces. When asked to "review a PR" in this
repo, use PR-REVIEW.md as the rubric — it specifies the context to
gather, the criteria, the report shape, and the citation format.

## Changelog requirements

Every user-visible change lands with an entry in
[CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`:

- Categories: `Added`, `Fixed`, `Changed / Internal` (CI, tests,
  refactors, and docs go in the last one).
- One bullet per change, bold lead-in phrase, linking the PR as
  `([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N))`.
  Match the existing entries' wrapped style.
- Doc-only or purely internal one-liners may be batched, but don't
  skip the entry — the changelog is finalized into release notes at
  release time (`[Unreleased]` becomes `[vX.Y.Z] - date`, and
  [RELEASE-NOTES.md](RELEASE-NOTES.md) is rewritten per release).

## Builds and releases on Nostr

CI ([.github/workflows/build-package.yml](.github/workflows/build-package.yml))
cross-compiles every push, packages `.ipk`/`.apk` per architecture,
uploads each artifact to multiple Blossom servers, and announces it as
a Nostr event. Agents can fetch builds without GitHub access using
`nak`.

**Publisher pubkey** (all release events are signed by CI with this
key):

- hex: `5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a`
- npub: `npub12p67v8ctqjq53dspqhqa6u4matse2uek4evzgzr72th6xa8cg94qxks7ks`

**Relays**: `wss://relay.damus.io`, `wss://nos.lol`,
`wss://nostr.mom`, `wss://relay1.orangesync.tech`,
`wss://relay2.orangesync.tech`

**Event kinds**:

- **`1063`** (NIP-94 file metadata) — one per published package.
  Tags: `url` (one per Blossom mirror holding the file), `x`/`ox`
  (sha256), `filename`, `n` (package name, `tollgate-wrt`), `v`
  (version: git tag like `v0.5.0`, or `<branch>.<height>.<sha>` for
  branch builds), `c` (release channel: `stable`, `beta`, `alpha`,
  `dev`), `A` (architecture, e.g. `aarch64_cortex-a53`, `mips_24kc`,
  `x86_64`), `format` (`ipk` or `apk`), `compression` (`none` or a
  `upx-*` variant).
- **`30078`** — transient per-arch build coordination between CI jobs;
  deleted with a kind `5` after the release events publish. Not useful
  to consumers.

**Fetching with nak** — single-letter tags (`n`, `v`, `c`, `A`) are
relay-filterable; multi-letter tags (`format`, `compression`) must be
filtered client-side with `jq`:

```bash
# Latest stable builds for one architecture
nak req -k 1063 -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
  --tag n=tollgate-wrt --tag c=stable --tag A=aarch64_cortex-a53 --limit 10 \
  wss://relay.damus.io wss://nos.lol

# All artifacts for a specific version
nak req -k 1063 -a 5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a \
  --tag v=v0.5.0 --limit 50 wss://relay.damus.io wss://nos.lol
```

Download from any `url` tag (they're mirrors of the same blob) and
verify the file's sha256 against the `x` tag before using it.
