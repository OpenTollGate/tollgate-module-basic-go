# Building TollGate from source

This is the user-facing build manual. It covers **which ref to build**, how to
build the portal and the package, and how to prove the tree builds from a cold
module cache. For the pinning/reproducibility design, see
[reproducible-builds.md](reproducible-builds.md).

## Just want a package for a router?

You do not need to build from source. Use the release artifacts:

- **Feed releases (signed `.apk`/`.ipk` + `SHA256SUMS`):**
  https://github.com/FreedomTechFeed/packages/releases
- **Feed recipe / supported targets:** https://github.com/FreedomTechFeed/packages
  (see `TARGETS.md` for the per-device matrix)
- **Installer wizard (recommended install path):**
  https://github.com/OpenTollGate/tollgate-installer

This manual is for reproducing or customizing a build, not for a normal
install.

| Goal | Where |
|---|---|
| Install on a router | feed releases + installer |
| Check supported devices/targets | feed `TARGETS.md` |
| Build or verify from source | this manual (`tollgate-module-basic-go`) |
| Rebuild a published artifact | the feed release tag + this manual |
| Read the OpenWrt recipe / release workflow | feed `net/tollgate-wrt/` |

## Release build vs `main`

**For a release, build the tag, not `main`.**

```sh
git clone --branch v0.6.0 --depth 1 \
  https://github.com/OpenTollGate/tollgate-module-basic-go.git
cd tollgate-module-basic-go
```

`main` is development. It can carry dependency changes that have not been
through the release path yet, and it is the default branch a fresh clone
checks out. If a build fails on `main`, first confirm you are not on a tag:

```sh
git describe --tags --exact-match 2>/dev/null || echo "not on a tag"
```

## CI status and reproducible builds

The current CI needs an overhaul. Warm module caches masked the
[#1036](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/1036)
`go.sum` gap: cached lanes never re-resolved a module that was already
downloaded, while a cold user checkout failed immediately. The cold-cache
check below is the interim gate until the CI lanes are rebuilt around a clean
module cache.

Reproducible builds are an active workstream. The pinned inputs and the
current guarantees are in [reproducible-builds.md](reproducible-builds.md);
this manual documents what is tested today, not a claim that every CI lane is
already cold-cache clean.

## Prerequisites

The pinned versions live in
[`packaging/build-inputs.json`](../packaging/build-inputs.json) and are loaded
by `packaging/build-env.sh`; do not guess them.

| Tool | Source |
|---|---|
| Go `1.26.8` | `packaging/build-inputs.json` `.go.version` |
| Node `22.17.0` / npm `10.9.2` | `.node.version` / `.npm.version` (portal build) |
| jq | required by `build-env.sh` |
| OpenWrt SDK + Docker | `.apk` package path (`scripts/build-sdk-package.sh`) |

If you keep the pinned toolchains outside `PATH`, point `TG_TOOLS` at a tree
containing `go/bin` and `node/bin`; `build-env.sh` prepends them.

```sh
export TG_TOOLS=$HOME/tollgate-tools   # optional
```

## Targets: what builds what

| Target | What it does | Produces a package? |
|---|---|---|
| `make portal-build` | builds/stages the captive-portal + admin SPA assets | no |
| `make go-battery` | `gofmt`/`vet`/`build`/`test -race` across every module | no |
| `make cold-battery` | the same battery with a fresh module cache | no |
| `make manual-check` | portal build + cold battery | only with `MANUAL_CHECK_PACKAGE=1` (then `.ipk`) |
| `make release-check` | release gates (battery, contracts, packaging tests, repro) | no |
| `make package-ipk` | full `.ipk` via `packaging/local-build-ipk.sh` | **yes** |
| `make package-apk` | `.apk` via the pinned OpenWrt SDK | **yes** |

`make portal-build` is **not** a package build. It only stages the portal and
admin assets that the package embeds; `packaging/local-build-ipk.sh` refuses to
run without them. The two package targets depend on `portal-build` (and
therefore on the pinned Node/npm). If you already have current portal assets
and do not want to rebuild them, the underlying scripts are the no-rebuild
path.

`packaging/build-ipk.sh` is the low-level packer (payload + `PKG_*` metadata);
it is not the release builder. Use `package-ipk` for a release.

## The quick check

On `main`, the one command that runs the sequence below is:

```sh
make manual-check
```

It checks the pinned Go, builds the portal with the pinned Node/npm, then runs
the Go battery in a **fresh** `GOMODCACHE`/`GOPATH`. To include the `.ipk`:

```sh
MANUAL_CHECK_PACKAGE=1 ARCH=x86_64 make manual-check
```

To skip the portal (for example when the pinned Node is not installed):

```sh
make cold-battery
```

On older tags the `manual-check`/`cold-battery` targets may not exist. Use the
explicit commands below; they are the same steps.

## Step 1 — portal assets

```sh
make portal-build
```

`packaging/portal-build.sh` refuses a Node/npm that does not match the pins.
It records the resolved portal SHA and tool versions in
`packaging/portal-build-inputs.json`.

## Step 2 — the all-module Go battery

`src/` is a multi-module Go tree (16 modules on `v0.6.0`, 18 on current
`main`); `cd src && go build ./...` covers only the root module. The canonical
gate is:

```sh
make go-battery
```

That runs `gofmt` (must print nothing), `go vet`, `go build`, and
`go test -race -count=1 -tags testenv` in **every** nested module.

## Step 3 — prove it from a cold cache

A warm module cache can hide a missing `go.sum` entry: a module that is already
downloaded is not re-resolved. Run the battery with fresh caches:

```sh
GOMODCACHE=$(mktemp -d) GOPATH=$(mktemp -d) make go-battery
```

On `main` this is `make cold-battery`. This is the check that would have caught
[#1036](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/1036).

## Step 4 — build a package

The package targets are the supported way:

```sh
# .ipk (OpenWrt 24.10 and earlier); default ARCH is x86_64
make package-ipk ARCH=x86_64

# .apk (OpenWrt 25.12+) through the pinned SDK
make package-apk SDK_TAG=x86-64-25.12.0 ARTIFACT_DIR=/tmp/tollgate-artifacts
```

Underlying commands, for reference:

```sh
ARCH=x86_64 bash packaging/local-build-ipk.sh
# → packaging/tollgate-wrt_<version>_x86_64.ipk

SDK_TAG=x86-64-25.12.0 PACKAGE_FORMAT=apk ARTIFACT_DIR=/tmp/tollgate-artifacts \
  bash scripts/build-sdk-package.sh
```

`SDK_TAG` encodes the target/OS line; the script infers `EXPECTED_ARCH` and
`GOARCH` from it. Supported values are in
[reproducible-builds.md](reproducible-builds.md) and
`packaging/build-inputs.json`.

## Step 5 — verify what you built

- The package version in the filename/control must match the release tag's
  `VERSION`.
- On a router, `tollgate version` reports the module build (it is not the feed
  tag); `apk list -I` / `opkg list-installed` reports the package version.
- For a **published** release asset, verify against the feed release's signed
  `SHA256SUMS`:

```sh
sha256sum -c SHA256SUMS
```

Do not invent a digest or skip the check because a warm build passed locally.

## Troubleshooting

### `missing go.sum entry for module providing package … btcutil/bech32`

This is the #1036 failure. It is a **`main` cold-build gap**, not a broken
`v0.6.0` release:

- `go-nostr/nip19` imports `btcsuite/btcd/btcutil/bech32`;
- two nested modules (`upstream_detector`, `upstream_session_manager`) were
  missing the transitive `go.sum` entries on `main`;
- #1013 fixed `merchant_types`; #1032 fixes the remaining two;
- the `v0.6.0` tag predates the import and cold-builds clean.

Workaround on a `main` checkout before #1032:

```sh
cd src/upstream_detector && go get github.com/nbd-wtf/go-nostr/nip19@v0.51.12
cd ../upstream_session_manager && go get github.com/nbd-wtf/go-nostr/nip19@v0.51.12
```

Or build the release tag. Do **not** use `go mod tidy` as a shortcut; it
cascades unrelated dependency drift (#1032 deliberately avoids it).

### `node … != pinned v22.17.0` / `npm … != pinned 10.9.2`

Install the pinned Node/npm and put them under `TG_TOOLS/node/bin`, or run
`make cold-battery` if you are only validating the Go tree.

### `go … != pinned 1.26.8`

Install the pinned toolchain from `packaging/build-inputs.json`. A newer local
Go is not a substitute for a release build.

## Tested at

| Ref | Commit | Command | Result |
|---|---|---|---|
| `v0.6.0` | `9364f2e` | cold `make go-battery` | PASS — 16 modules |
| `main` post-#1032 | `4e55543` | cold `make cold-battery` | battery PASS — 18 modules; cleanup trap fixed in this PR |
| `main` pre-#1032 | `9ed8687` | cold build of the two modules | expected #1036 failure |

When you change the pins or the build path, update this table and the
[reproducible-builds.md](reproducible-builds.md) manifest together.

## Optional Ansible wrapper

The lab uses
`tollgate-infrastructure-kit/ansible/playbooks/53-tollgate-build.yml` to run the
same checks on a clean builder. It calls `make manual-check` /
`make cold-battery`; the Make targets remain the contract. Ansible is not
required for a contributor build.
