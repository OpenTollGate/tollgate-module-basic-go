# Reproducible builds

> For the step-by-step source/release build (including the cold-cache gate
> and the #1036 troubleshooting), see
> [build-from-source.md](build-from-source.md).

## Two paths, one truth (the parallel-build doctrine)

This repo deliberately maintains TWO build paths and gates them against
each other, so neither can drift in versions or pinnings:

| | Path A — SDK (canonical) | Path B — shortcut |
|---|---|---|
| What | digest-pinned OpenWrt SDK package build (`.github/workflows/build-package.yml`, `scripts/build-sdk-package.sh`) | cloud-lab Docker images + host prebuild (`tests/cloud-lab/Dockerfile.*`, `packaging/local-build-ipk.sh`) |
| Toolchain | `packaging/build-inputs.json` → `.go.version`, re-derived from the OpenWrt packages feed and enforced by `scripts/sdk-go-version.sh check` | the SAME manifest — `lab.sh` exports it into compose build args; Dockerfile `ARG GO_VERSION` defaults are lockstep fallbacks |
| Flags | canonical: `-trimpath -buildvcs=false -ldflags=…` | the SAME canonical flags (Dockerfile.tollgate) |

**The manifest is the single source of truth.** Every other reference is
either derived from it at runtime (CI's `go_pin` step, `lab.sh` exports)
or a literal kept in lockstep — and every literal is drift-gated by
`tests/contract/check-toolchain-parity.py` (CI job + pre-commit hook).
Bumping the toolchain is an intentional release decision: change the
manifest, run `scripts/sdk-go-version.sh update` to refresh the feed map,
and the parity gates will point at anything left behind.

**Proof, not vibes:** `repro-check.yml`'s fast lane rebuilds the binaries
in independent clean roots (identical SHA-256s required); the
`path-parity` job (workflow_dispatch / scheduled) builds the same commit
through BOTH paths and requires **byte-identical** binaries. If the
shortcut path diverges, it fails loudly instead of shipping a different
artifact than the one the SDK publishes.

# Reproducible builds

Given the same immutable input tuple, two clean builds of a `tollgate-wrt`
artifact produce **byte-for-byte identical output** (same SHA-256). This
document explains what "the same inputs" means here, how each one is pinned,
and how to verify a build yourself.

## The immutable input tuple

| Input | Where it is pinned |
|---|---|
| TollGate source commit | the git commit being built (`SOURCE_DATE_EPOCH` derives from it) |
| Captive portal commit | `packaging/build-inputs.json` → `.portal.commit` (an immutable SHA; floating refs are rejected) |
| `SOURCE_DATE_EPOCH` | default = TollGate HEAD commit timestamp (`git log -1 --format=%ct HEAD`); export to override (e.g. to reproduce a historical release) |
| Go toolchain | `.go.version` (exact patch, e.g. `1.26.8`) + verified tarball sha256. Must equal the official golang of the pinned SDK release's packages feed — see "SDK Go alignment" below |
| Node | `.node.version` (the portal's declared engine, e.g. `22.17.0`) + sha256 |
| npm | `.npm.version` (bundled with the pinned Node; verified at build time) |
| UPX (compressed builds) | `.upx.version` + tarball sha256, fetched by `scripts/fetch-upx.sh` |
| OpenWrt SDK image | `.openwrt_sdk.releases[<era>].targets[<target>].digest` — `openwrt/sdk:<tag>@sha256:…` |
| Captive-portal daemon | `.nodogsplash.per_era[<era>]` — upstream version + **packaging release** per era, fenced by `tests/contract/check-nodogsplash-pin.py`. We ship no byte of it; the pin NAMES what the feed resolves (see "The daemon we ship") |
| Target architecture | caller-chosen (`x86_64`, `aarch64_cortex-a53`, …) |
| Package format | `ipk` (opkg-build style tar.gz) or `apk` (OpenWrt SDK) |
| Compression mode | none, or UPX flags (e.g. `--ultra-brute`) |
| Build flags | `CGO_ENABLED=0 -trimpath -buildvcs=false` + the deterministic ldflags below |

`packaging/build-inputs.json` is the manifest a third party needs (plus the
two source repos at the recorded commits) to rebuild a release later.

## How `SOURCE_DATE_EPOCH` is chosen

Default: the **TollGate HEAD commit timestamp**. Every timestamp that ends
up in an artifact derives from it:

- `BuildTime` embedded in the Go binaries is rendered as
  `date -u -d @$SOURCE_DATE_EPOCH` — never the wall clock. Rebuilding the
  same commit next month produces the same string.
- All tar members in the `.ipk` use `--mtime=@$SOURCE_DATE_EPOCH`.
- Files staged for the OpenWrt SDK (apk path) are `touch`ed to the epoch,
  because apk package metadata reads staged mtimes.
- Portal output files are normalized to the epoch after `npm run build`.

Override by exporting `SOURCE_DATE_EPOCH` before any build script; that
also enables strict-inputs mode (`TG_STRICT_INPUTS=1`).

A build with no git metadata — an ngit-ci `act` job checkout, a clean-root copy
— gets the same commit timestamp through `scripts/ngit-commit-epoch.sh`, which
fetches *exactly that commit* (depth 1) from the git transport the build came
from. That script is the only supported CI derivation, and it **fails** rather
than substituting the runner clock: a wall-clock epoch makes two builds of one
commit differ, which is the one thing this document claims cannot happen. The
ngit release lane (`.ngit/act/workflows/build-package-binaries.yml`,
`determine-versioning` → `build-portal`, and the shards' `resolve-inputs`) calls
it for exactly that reason.

## Where everything lives

- `packaging/build-inputs.json` — the manifest of pinned inputs.
- `packaging/build-env.sh` — the single loader every build script sources;
  derives the epoch, verifies pins, and provides `go_ldflags` /
  `cli_ldflags` / `sdk_image_ref` / `normalize_mtime`. Never recompute these
  values independently in another script.
- `packaging/local-build-ipk.sh` — deterministic ipk build (per-arch).
- `packaging/build-ipk.sh` — deterministic packer: sorted members, pinned
  mtimes, `gzip -n` (no gzip-header timestamps), owner 0:0.
- `packaging/portal-build.sh` — portal build pinned to the manifest SHA;
  verifies Node/npm; records the resolved SHA + tool versions into
  `packaging/portal-build-inputs.json`.
- `scripts/build-sdk-package.sh` — apk path: SDK image pinned by digest,
  staged mtimes normalized, `SOURCE_DATE_EPOCH` propagated into the
  container.
- `scripts/sdk-go-version.sh` — reads the official golang of an OpenWrt
  release from the SDK's own sources (feed branch + released feed) and
  audits the manifest's SDK Go pins against them.
- `scripts/fetch-upx.sh` — pinned UPX download with sha256 verification.
- `tests/contract/check-nodogsplash-pin.py` — the captive-portal daemon pin:
  carrier identity (glob-discovered), the live feed recipe per era, and a
  built artifact's own dependency metadata.
- `scripts/repro-test.sh` + `make reproducibility-test` — the test below.

## Building an artifact

```sh
# prerequisites: pinned toolchain on PATH (see build-inputs.json), jq
bash packaging/portal-build.sh                  # refresh portal assets
ARCH=x86_64 bash packaging/local-build-ipk.sh   # → packaging/tollgate-wrt_*.ipk
# apk:
SDK_TAG=x86-64-25.12.0 PACKAGE_FORMAT=apk ARTIFACT_DIR=/tmp/art \
  bash scripts/build-sdk-package.sh             # → /tmp/art/apk-x86-64-25.12.0/
```

## Running the reproducibility test

```sh
make reproducibility-test T=binaries ARCH=x86_64   # or run scripts/repro-test.sh directly
# targets: binaries | portal | ipk | ipk-upx | apk
# arch:    x86_64 (default) | aarch64_cortex-a53 | arm_cortex-a7 | mips_24kc | mipsel_24kc | aarch64_cortex-a72
```

The test creates **two genuinely independent clean roots** (fresh tree
copies, separate `HOME`, separate Go/npm caches — cache state cannot make
hashes match), builds the selected artifact in each, and prints:

```
BUILD 1: <file>  <sha256>
BUILD 2: <file>  <sha256>
REPRODUCIBLE: YES
```

On mismatch it prints `REPRODUCIBLE: NO`, runs `cmp` + `diffoscope` (when
installed) on the pair, and keeps both roots (`KEEP=1` also keeps them) so
the difference can be inspected. Heavy targets (apk especially) belong on a
beefy build host.

The check fails closed. A target that produces no artifact is a **failed**
build, not a pass: an artifact glob with no matches (or an empty portal
output directory) would otherwise reach `REPRODUCIBLE: YES` without a single
comparison. Each build root must yield at least one artifact, both roots must
yield the *same number* of artifacts, the portal tree must be non-empty
before it is hashed, and at least one real comparison must have happened
before the all-clear is printed (the run reports how many artifact pairs it
compared).

## Variance testing (reprotest)

The clean-roots harness builds twice in environments that are, by
construction, identical in everything the copies share — same host, same
user, same umask, same locale. That is its strength (it isolates the build
from the cache) and its blind spot: nothing *varies*. The umask leak fixed
in `0acd0bf` shipped precisely because of that blind spot — two roots on
one machine could not see it; a second host could.

`make reproducibility-variance` (or `scripts/repro-variance.sh`) runs
[reprotest](https://reprotest.readthedocs.io/) — the reproducible-builds.org
variance engine — over the same ipk build. It rebuilds under hostile
environment variations (default `+umask,+timezone`, which need no extra
setup; `VARIATIONS=+locales,+fileordering` or `+all` add more but need
Debian-class tooling — `locales-all` and `disorderfs` with FUSE — or the
varied build dies with exit 127) and diffs the artifacts with diffoscope.
Install it once, outside the tree:

```sh
python3 -m venv .reprotest-venv && .reprotest-venv/bin/pip install reprotest
REPROTEST=.reprotest-venv/bin/reprotest make reproducibility-variance
```

reprotest is a test dependency only — it never touches artifact bytes, so
it is not pinned in `build-inputs.json`.

## Updating pinned versions intentionally

Edit `packaging/build-inputs.json` — one value, one PR:

- **Go**: the pin must equal the official golang of the pinned OpenWrt SDK
  release's packages feed (see "SDK Go alignment" below). Bump `.go.version`
  + the tarball sha256 from `https://go.dev/dl/?mode=json`. CI lanes read
  the version from the manifest at run time — nothing else to keep in sync.
- **Node/npm**: pick what the portal's `package.json` `engines` declares;
  take the sha256 from the nodejs.org `SHASUMS256.txt` for that version.
  Update the workflow's `node-version:` to match.
- **Portal SHA**: paste the full commit SHA of the portal revision to ship.
  Never a branch name.
- **UPX**: version + release-tarball sha256.
- **Captive-portal daemon (nodogsplash)**: `.nodogsplash.per_era[<era>].version`
  + `.release` (the feed's `PKG_VERSION` / `PKG_RELEASE`) + `.source_hash`
  (`PKG_HASH`). This one is **not** a refresh: it has no `update` mode and no
  script rewrites it. Raise the feed's `PKG_RELEASE` upstream, or carry a
  deliberate decision (changelog fragment + ADR) — every gate, session and
  pre-auth allow-list in this repo was bench-verified against the daemon we
  run, so a version change is a re-verification event. See "The daemon we
  ship" below.
- **OpenWrt SDK digests**: `docker manifest inspect openwrt/sdk:<tag>` —
  paste the digest for each target into `packaging/build-inputs.json`.
  The CI package matrix injects every row's digest from the manifest at
  generation time, so the two lists cannot drift.
- **OpenWrt SDK release**: set `.openwrt_sdk.release`, refresh the target
  digests, and run `scripts/sdk-go-version.sh update` so
  `.openwrt_sdk.go_per_release` follows the release lines; then treat the
  Go bullet above as the constraint it names.

Bumping a pin is a reproducibility event: expect artifact hashes to change
once, then be stable again.

## The daemon we ship (nodogsplash)

The captive-portal daemon is the one shipped dependency this repo does not
compile. `packaging/Makefile` declares it as a runtime dependency
(`DEPENDS:=+nodogsplash …`); the OpenWrt feed decides which build of it lands
on the router. Until `.nodogsplash` existed in the manifest, **nothing in this
repository named a version** — a feed bump changed what an operator runs with
no diff here, and the daemon is what every gate, session and pre-auth
allow-list in this tree was bench-verified against.

`packaging/build-inputs.json` → `.nodogsplash.per_era` names it, per era:

| Era | Lane | Pinned | Why it differs |
|---|---|---|---|
| `apk` | 25.x — the only **feature** target | `5.0.2-r2` | `openwrt/routing@openwrt-25.12` carries `PKG_RELEASE:=2`; the released 25.12.0 feed publishes `nodogsplash-5.0.2-r2.apk` |
| `ipk` | ≤24.10 — **frozen** compatibility lane (security / stop-ship only, #796) | `5.0.2-r1` | `openwrt/routing@openwrt-24.10` never took the `-r2` bump; the released 24.10.8 feed publishes `nodogsplash_5.0.2-r1_<arch>.ipk` |

**The era difference is real, so the pin is per-era.** The upstream version is
the same (5.0.2) and the source tarball hash is the same; only the feed's
packaging release differs. A single-value pin would therefore be a **false
equality** — correct for one lane, naming a package the other lane does not
publish, and passing anyway. Instead the manifest carries both and the fence
attributes each claim to an era: a carrier that says `5.0.2` (bare) matches
both; a carrier that says `5.0.2-r2` matches the apk lane and is **refused**
if it is talking about the ipk lane. `.nodogsplash.era_divergence` records the
divergence in prose so a future reader does not "simplify" the map back into
one string.

If the feed ever converges (both branches on one release), the two entries
simply carry the same value — the shape stays, because the lanes can diverge
again at any point-release.

**Three legs, three different failures** (`tests/contract/check-nodogsplash-pin.py`):

```sh
python3 tests/contract/check-nodogsplash-pin.py                     # identity (offline)
python3 tests/contract/check-nodogsplash-pin.py --feed              # live feed + artifact
python3 tests/contract/check-nodogsplash-pin.py --artifact out.apk  # built package's own metadata
```

- **identity** — every file in the tree that names a nodogsplash version
  (discovered by glob, never a hand list — AGENTS.md rule 2) must name one the
  manifest pins, packaging release included.
- **`--feed`** — the manifest must match the *live* feed recipe of each era on
  `PKG_VERSION`, `PKG_RELEASE` and `PKG_HASH`. A transport failure is a
  **failure**, not a skip: a fence that passes because it could not look is
  decoration (rule 4). This leg runs in CI and `release-check.sh`, never in
  the pre-commit hook.
- **`--artifact`** — a built `.apk`/`.ipk` that constrains the daemon's version
  must constrain it to one the pin names. This reads the metadata apk/opkg
  actually consumes, so no recipe edit can hide a wrong resolution.

`tests/contract/nodogsplash-pin_test.sh` is the verdicts harness: it plants
every drift shape and requires the refusal by name, including the case a
single-value pin cannot catch (the apk era's `-r2` written for the ipk lane).

**What this pin does NOT do — pinning is not testing.** This makes the version
**NAMED**. It proves nothing about behaviour: that the daemon we ship is the
one the gates were measured against, that its `ndsctl` surface answers the way
the module expects, that the `-r2` bump changed nothing we rely on. That is
PRTA's `nds_dependency_identity` (separate card, separate repo), which makes
the resolved identity **PROVEN** on a real package. A green fence here means
the name is honest, not that the daemon is verified.

## SDK Go alignment

The OpenWrt SDK does not pin a Go version itself — its packages feed does.
Each release line's feed (the `openwrt/packages` branch `openwrt-<series>`,
e.g. `openwrt-25.12`) defines one official golang, and each point release
publishes it to `downloads.openwrt.org/releases/<release>/packages/`, the
feed the digest-pinned SDK image resolves against. `build-inputs.json`
records that mapping:

- `.openwrt_sdk.go_per_release` — official golang per release line
  (23.05 → 1.21.13, 24.10 → 1.23.12, 25.12 → 1.26.8 at the time of
  writing), maintained by `scripts/sdk-go-version.sh update` from the live
  feed branches.
- `.go.version` — the toolchain this repository builds with; it must equal
  the released feed's golang of the pinned `.openwrt_sdk.release`.

`scripts/sdk-go-version.sh check` verifies both against the live OpenWrt
sources and exits non-zero on drift:

```sh
scripts/sdk-go-version.sh print 24.10    # official golang on a feed branch
scripts/sdk-go-version.sh released       # golang in the pinned release's feed
scripts/sdk-go-version.sh check          # audit the manifest (CI-able)
scripts/sdk-go-version.sh update         # refresh go_per_release only
```

Why it matters for the two build paths: the SDK lane cross-compiles the
binaries on the host with the pinned Go and stages them into the SDK for
packaging (`packaging/Makefile` `PREBUILT_BIN`), so today the SDK's own
golang never touches our bytes. The alignment is the contract that keeps
that true in both directions — if these two paths ever disagree (a full
in-SDK Go build through the feed's `golang/host`, or a toolchain bump the
SDK has not shipped), the binaries the two paths produce would differ, and
`check` is what turns that from a silent divergence into a red light.

Doctrine for the two ways: the fast/reproducible lane **is** the release
pipeline — it is pinned end-to-end (toolchain tarball sha256,
`SOURCE_DATE_EPOCH`), so it reproduces months later, and every shipped
byte comes from it. The OpenWrt-SDK way is the **compatibility canary**:
it exists to notice when OpenWrt's own toolchain would build something
different, not to produce artifacts. Because the feeds move under a
static tree (the released 25.12.0 feed moved 1.26.8 → 1.26.9 with no
release tag), the two are required to agree **at the time of commit**:
`hooks/pre-commit` runs this same `check` on every commit (drift blocks,
an unreachable feed only warns — a hook never blocks on network; CI's
`repro-check.yml` re-runs the fence where network is guaranteed), and
the full path-parity comparison stays a spot check for when in doubt,
not a release gate. Drift is cured by re-tracking the pin intentionally
— never by unpinning the fast way to follow the feed.

One caveat for older lines: a full in-SDK Go build also needs the source
tree's language minimum (`src/go.mod` `go` directive, currently 1.25.0) to
be ≤ the line's official golang. 23.05 (1.21.x) and 24.10 (1.23.x) do not
qualify; their artifacts must come from the host-compiled staging path the
release lanes use today.

## The build contract (one declaration, N consumers)

`packaging/build-contract.json` is the machine-readable face of everything
above: the binary set (authority: `packaging/Makefile` `PREBUILT_*`), the
module map and ldflags profiles per binary, the **arch table** (authority:
`scripts/repro-test.sh`'s arch case, cross-checked both directions — the
feed's build matrix and the fast lane's cross-compile consume it instead of
re-listing arches), the language minimum (max `go` directive across every
`src/**/go.mod`, glob-discovered), the toolchain pin and the portal commit. `scripts/gen-build-contract.sh` is its single
writer — the committed file is generated, never hand-edited — and
`tests/contract/check-build-contract.py` fences it two ways: the
generator must reproduce the committed bytes, and every carrier that
builds Go binaries must ship the whole set (content-discovered carriers,
explicit `# contract-exempt:` markers with reasons for harnesses).

Why a generated file at all: the binary set used to live in three carrier
scripts plus two downstream feed repos, and the fifth copy is where
reality diverged — upstream's apk lane broke on a missing CVM daemon
(#1020) while the Gen-1 feed still ships two binaries with a broken CLI
version injection. One generated declaration plus fences turns that class
from "detected eventually, per repo" into "impossible, everywhere".

Downstream feeds (e.g. FreedomTechFeed) consume the contract straight out
of the release tarball — codeload tarballs are repo snapshots, so every
tag and every SHA pin carries it for free — instead of re-deriving the
facts by hand. Keeping it IN the release lane (not a side artifact) is
what makes it the single source of truth: a contract that can lag a
release is a second source of truth waiting to happen.

## Reproducing a historical release

1. Check out the TollGate commit recorded in the release manifest.
2. Ensure the toolchain versions match the manifest (install the pinned
   Go/Node tarballs).
3. `SOURCE_DATE_EPOCH=<epoch from the release notes> make reproducibility-test T=ipk ARCH=<arch>`
   — or build directly as above with the same export.
4. Compare your SHA-256 to the published one.

## Known residual nondeterminism surface

- The GitHub-hosted ipk job runs on `ubuntu-latest`; its `tar`/`gzip`
  versions are whatever the image ships. The packer normalizes ordering,
  mtimes, ownership, and gzip headers explicitly, so the bytes should not
  depend on those versions — but the authoritative check runs on a
  controlled host. Migrating the job to a pinned container is a possible
  follow-up.
- `npm ci` depends on the portal's `package-lock.json` at the pinned commit;
  the lockfile, not the registry, fixes dependency bytes.
- **Upstream epoch policy**: OpenWrt is moving its own package builds
  toward a fixed `SOURCE_DATE_EPOCH` (see openwrt/openwrt#21579 and the
  "set SOURCE_DATE_EPOCH to 0" direction) because per-package git
  derivation breaks in shallow feed clones and the SDK. When tollgate is
  built inside openwrt/packages, their epoch policy will govern buildbot
  artifacts, so a feed artifact (epoch = our commit timestamp) and an
  upstream buildbot artifact (epoch = their fixed value) will differ by
  design even for identical sources. That is per-channel identity, not
  nondeterminism — but do not expect the hashes to cross-check between
  the two channels.
