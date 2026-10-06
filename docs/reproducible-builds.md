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
| OpenWrt SDK image | `.openwrt_sdk.targets[<target>].digest` — `openwrt/sdk:<tag>@sha256:…` |
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

One caveat for older lines: a full in-SDK Go build also needs the source
tree's language minimum (`src/go.mod` `go` directive, currently 1.25.0) to
be ≤ the line's official golang. 23.05 (1.21.x) and 24.10 (1.23.x) do not
qualify; their artifacts must come from the host-compiled staging path the
release lanes use today.

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
