# .ngit — Nostr CI (ngit-ci)

ngit-ci reads workflows from `.ngit/act/workflows/` **only** — files under
`.github/workflows/` are detected but never executed. GitHub Actions is
untouched; the two systems run side by side.

## What runs, and why

| File | Checks |
| --- | --- |
| `act/workflows/test.yml` | The port of `.github/workflows/test.yml`: per-module Go tests over the module matrix, the main-package `testenv` test, `js-schema-lint`, the `Spec-quote drift check`, `build-purity`, and the dependency/import-path checks. |
| `act/workflows/go-test.yml` | The pre-PR sequence documented in [AGENTS.md](../AGENTS.md), run from `src/`: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test -race -count=1 -tags testenv ./...`. |
| `act/workflows/build-package-binaries.yml` | Stage 1 of the release pipeline: cross-compile the five GOARCH/GOARM/GOMIPS targets, build the captive-portal assets, mirror both to Blossom, and publish the build-id records stage 2 consumes. |
| `act/workflows/build-package.yml` | Stage 2 of the release pipeline: the full `.ipk` (14) and `.apk` (3) matrix, Blossom mirroring, kind-1063 NIP-94 announcements, and the tollgate-os handoff. |
| `act/workflows/ci-probe.yml` | Temporary diagnostic. Reports the runner ceiling, daemon-socket access and secret release from inside a real job. Deleted once `build-package.yml` is verified. |

Two Go files because they cover different things: `test.yml` tests the nested
modules (which `./...` from `src/` does not reach — they are separate modules)
and the `tests/contract` checks, while `go-test.yml` runs the documented gate
that `test.yml` omits (`gofmt`, `go vet`, `go build`, and a race-enabled run of
the root module).

### `test.yml` was NOT a faithful copy until 2026-09-12

The first port claimed to be "a faithful, verbatim port" but **silently dropped
the `Spec-quote drift check` step** that `.github/workflows/test.yml` runs (the
two files agreed everywhere else, so a diff was the only way to see it). The
step is restored, so the two files are now byte-identical in content. Verify
with:

```bash
diff -u .github/workflows/test.yml .ngit/act/workflows/test.yml   # empty
```

Local evidence for the restored step (run in a worktree at `1a3cbb4`):

```
pip install --user "git+https://github.com/rustyrussell/greatspectations.git@0f22649"
git init nuts && git -C nuts fetch --depth=1 origin 49a909ce && checkout FETCH_HEAD
greatspectate check --config specquotes.toml \
  --comment-start "// " --comment-continue "//" <56 non-test src/**/*.go>
-> exit 0 (56 files, no drift)
```

## Triggers

`test.yml` and `go-test.yml` run on **push to `main`** and on **pull requests**.
`build-package.yml` runs on **push to `main`** and on **`v*` tag** pushes, with
the GitHub twin's `paths-ignore` (`**.md`, `docs/**`), plus pull requests.

What ngit-ci does **not** honour, and what the port does about it:

| GitHub feature | ngit-ci | Port decision |
| --- | --- | --- |
| `schedule:` | not supported | not used |
| `workflow_dispatch` | replay only; `inputs` are never delivered | the twin's `full_compression` boolean is replaced by a ref test (see below) |
| `concurrency:` | not honoured | documented; the build id is the commit, so coordination records are addressable and a re-run replaces rather than duplicates |
| dynamic `strategy.matrix` from `needs.<job>.outputs` | **not supported** ("matrix values built from expressions do not [work]") | the matrices are written out as static YAML — the same 14 `.ipk` and 3 `.apk` entries |
| `matrix.*` in a **job-level** `if:` | rejected: `Failed to match job-factory: Unknown Variable Access matrix`, which invalidates the whole file | the variant rule is dropped (all variants always build) and `if:` at job level is used only as `always()` |
| `container:` / `services:` | **refused** with `startup_failure` when the operator sets container options (this deployment does) | the SDK jobs run `docker run openwrt/sdk:<sdk>-25.12.0` from a plain job (see below) |
| `actions/upload-artifact` / `download-artifact` | **fails**: `Unable to get the ACTIONS_RUNTIME_TOKEN env variable` | nothing crosses a job boundary in an artifact; the portal assets travel over Blossom, like the compiled binaries already did |
| `github.token` / `GITHUB_TOKEN` | empty | nothing depends on it |
| `peter-evans/repository-dispatch@v4` | impossible | replaced by the `os-handoff` job (a kind-30078 Nostr record) plus a printed manual instruction |
| secrets | only to maintainer-authored triggers | `secrets.NSEC_HEX` is provisioned operator-side (see below) |
| 30-minute budget | the whole `act` invocation is bounded by `--job-timeout-secs` (1800 s) | see "Does the matrix fit?" |

The GitHub twin dropped the UPX compression variants on a non-release ref by
filtering the matrix it generated with `jq`. Neither half of that is available
here: a dynamic matrix is unsupported, and a job-level `if:` that reads
`matrix.*` makes act's schema validator reject the *entire file* —

```
Failed to match job-factory: Unknown Variable Access matrix
Actions YAML Schema Validation Error detected
```

— so the ngit port builds **every** variant on **every** run: 14 `.ipk` entries
and 3 `.apk` entries. That is *more* coverage than the twin, not less, which is
the shape the operator asked for ("build the matrix so we can publish as many
architectures as we like"). The cost is eight extra jobs on a side-branch or PR
run where the twin built only the base variants; on `main` and on `v*` tags the
two are identical. A job-level `if:` is therefore only used with `always()`, and
the one bit of ref-dependent logic (the tollgate-os handoff) is a `bash` check
inside the step.

## What the port changes, and why — measured, not assumed

Everything below was measured on this deployment (coordinator `765cd47b…` on
DQ05, `embedded-act` runner, `ghcr.io/catthehacker/ubuntu:act-latest`) with
`ci-probe.yml` runs on 2026-09-12.

* **Container daemon is usable from a job.** `NGIT_CI_ACT_CONTAINER_DAEMON_SOCKET`
  is set to `unix:///var/run/docker.sock`, so a job gets
  `/var/run/docker.sock` (mode `srw-rw---- root:2375`) and `docker version`,
  `docker pull` and `docker run --rm --user root` all work. That is what makes
  the SDK jobs possible: the twin's `container: openwrt/sdk:…` block is refused,
  but the image itself is not.
  The dind daemon is a *sibling*, so the job's filesystem is not visible to it:
  the package tree is streamed in with `docker exec -i … tar xzf -` and the
  built `.apk` is streamed out with `docker cp`.
* **Runner ceiling: 4 vCPU, 4 GiB.** `nproc=4`, `cgroup memory.max=4294967296`
  (4 GiB), `cgroup cpu.max=200000 100000` (2 CPUs of quota), host has ~5 GiB
  free. Job containers are capped at `--memory=4g --cpus=2` by
  `NGIT_CI_ACT_CONTAINER_OPTIONS`, and `NGIT_CI_MAX_CONCURRENT_JOBS=2`.
* **`go` is not preinstalled**, `nak` and `upx` are not either; `node`/`npm`,
  `python3`/`pip3`, `jq`, `curl`, `ar`, `tar`, `docker`, `make`, `apt-get` and
  `sudo` are. The port installs Go with `actions/setup-go@v6` pinned to the
  literal `1.25.0` (`go-version-file` is what failed first in this image) and
  installs nak from the pinned `v0.16.2` release.
* **No git metadata in the checkout.** `git rev-parse` inside a job reports
  *not a git repository* and `git rev-list --count HEAD` fails, so the twin's
  `git rev-parse --short HEAD` / `git rev-list --count HEAD` are replaced by
  `GITHUB_SHA`. A branch build is `<branch>.<height>.<sha>`, where the height
  falls back to `0` when history is unavailable; tagged releases are unaffected
  because their version is the tag name. `GOFLAGS=-buildvcs=false` for the same
  reason.
* **Blossom reachability from the runner.** `blossom.primal.net`,
  `blossom.psbt.me`, `blossom2.orangesync.tech` and `drive.cashu.email` answer;
  `blossom1.orangesync.tech` times out (25 s). The server list is left as the
  GitHub twin has it, with `BLOSSOM_MIN_SUCCESS=1`, but every upload pays a
  ~60 s penalty on the dead mirror. Dropping it from `BLOSSOM_SERVERS` at the
  top of the file is a one-line change if that cost matters.
* **`actions/cache@v4` is used**, marked `continue-on-error: true`, so a
  coordinator with caching disabled still runs the job. `setup-go` is called
  with `cache: false` for the same reason (a cache failure inside the action is
  not survivable).
* **Per-job publishing of the coordination record.** Each package job publishes
  its own kind-30078 record keyed by `d=tollgate-build/<build_id>/<arch>/<fmt>/<compression>`
  instead of relying on a single job to collect them. `publish-metadata` then
  turns those records into kind-1063 NIP-94 events with `if: always()`, so a red
  SDK job cannot suppress the artifacts that did build.
* **The 30078 records are kept**, not deleted. The GitHub twin published a
  NIP-09 deletion for them at the end of the run; here they are the ngit-native
  rendezvous point — a second workflow file, or a downstream consumer such as
  tollgate-os, can resolve them by build id.

## Does the matrix fit the 30-minute budget?

The budget is per `act` invocation (one workflow file), not per job
(`--job-timeout-secs`, default 1800). With `NGIT_CI_MAX_CONCURRENT_JOBS=2`, two
files can run at once, and inside a file act parallelises jobs up to the host's
capacity — 4 vCPU and ~5 GiB free, against a 4 GiB / 2 CPU cap *per job*, means
roughly one to two package jobs are actually resident at a time.

Measured on this deployment: a job that installs nak, uploads to Blossom,
fetches the blob back and publishes + reads back a kind-30078 event takes
**13.6 s** wall (`ci-probe.yml`, commit `8e79b43`). The `.ipk` jobs are that
plus a ~10–40 MB binary download, an optional `apt-get install upx-ucl`, and the
`ar`/`tar` packaging — call it 40–90 s each. Fourteen of them, mostly serialised,
is 10–20 minutes: it fits, but with little headroom once the Go cross-compile
(`compile-binaries`) and the portal build are included.

The `.apk` jobs are the real risk: each must pull an OpenWrt SDK image, run
`make defconfig` over the SDK, and compile `nodogsplash`, `luci` and `jq` as
dependencies. That is very unlikely to finish inside 30 minutes from a cold SDK
image, and there is no way to raise the ceiling from inside the workflow — the
timeout belongs to the coordinator operator.

**The split is implemented — two files, two budgets.** The first manual
replay of the single-file pipeline (commit `1312f03`) was rejected by act's
schema validator in 675 ms; after that was fixed, the replay at `63eb38f` was
still inside `compile-binaries` eleven minutes in, with the Go module cache at
243 MB and the five Blossom uploads still retrying. A 17-job package stage
behind a 10–20 minute compile stage cannot fit one 30-minute budget, so the
pipeline is split by stage:

1. `build-package-binaries.yml` — `determine-versioning`, `compile-binaries`,
   `build-portal`. Its own 30 minutes.
2. `build-package.yml` — `resolve-inputs`, `package-ipk` (14), `package-apk`
   (3), `publish-metadata`, `os-handoff`. Its own 30 minutes.

They are tied together by the build id (the commit's short SHA) and by two
addressable kind-30078 records that stage 1 publishes:

```
d=tollgate-build/<build_id>/binaries   {"arm64":"<sha256>", "armv7":"…", …}
d=tollgate-build/<build_id>/portal     {"sha256":"…","filename":"portal-assets.tar.gz","urls":[…]}
```

`resolve-inputs` polls the coordination relays for those records for up to 10
minutes, so the two stages can be started back to back at the **same commit**
— and on a push to `main` both files are triggered by the same push anyway.
This is the ngit-native replacement for the twin's `needs.<job>.outputs`
(ngit-ci has no cross-file `needs`) and for `actions/upload-artifact` (broken
here). A stage 2 run against a commit whose stage 1 never ran fails loudly in
`resolve-inputs` with that explanation.

Each package job also publishes its **own** kind-1063 announcement, immediately
after its Blossom upload, instead of one `publish-metadata` job announcing
everything at the end. `publish-metadata` now verifies the announcements
against the build records, republishes any that are missing, and prints the
summary. The reason is the 30-minute ceiling: a run that is cut off mid-matrix
must still have announced everything it did build.

If three SDK architectures still do not fit one budget once their wall time is
measured, the same file is copied once per architecture
(`build-package-apk-mediatek-filogic.yml`, `build-package-apk-x86-64.yml`) —
they are independent runs that share the build id.

## Release signing: the historical key is unrecoverable

`build-package.yml` signs the Blossom uploads and the kind-1063 announcements
with `secrets.NSEC_HEX`, which on GitHub was the release key
`5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a`.

That key **cannot be recovered from GitHub**: Actions secret values are
write-only over every API, so not even an org admin can read one back. It is
also not present on this host or on DQ05 (every `nsec1…` token in the profile
stores, key directories and git configs was resolved to a pubkey and compared
against it — no match). Its only surviving copy is wherever the human who set
the secret kept it.

Until that key is supplied out of band, this port signs with a **new, dedicated
CI release key** provisioned by the operator:

```
pubkey 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d
```

It is deliberately **not** the repository maintainer key (`36bdeb…`), which also
signs this repository's kind-30617 announcement and must not be handed to
workflow-executed code. Announcements made by the new key are attributable and
separable; a consumer filtering releases by the historical publisher pubkey will
not see them, which is why publishing a real alpha/beta/stable release is
blocked on the operator either importing the old key or blessing the new one.

### Provisioning it (operator, on DQ05)

Three things are needed, and the first one is easy to miss:

```bash
# 1. give the watched entry an alias, so per-repo secrets can be addressed.
#    A bare-pubkey entry with #ALIAS keeps exactly the same watch coverage.
#    .env ->  NGIT_CI_REPOS=npub1x677…#TMBG,<other entry>
# 2. declare the secret.  .env ->  NGIT_CI_SECRET_TMBG__NSEC_HEX=<64 hex chars>
# 3. PASS IT INTO THE CONTAINER.  docker-compose interpolates .env but only
#    injects the variables named under a service's `environment:` block, so
#    without this line the secret is silently absent:
#      NGIT_CI_SECRET_TMBG__NSEC_HEX: "${NGIT_CI_SECRET_TMBG__NSEC_HEX:-}"
cd ~/ngit-ci-deploy && docker compose up -d coordinator
```

Verify — the coordinator must log `Loaded per-repo secrets count=1`, and a
maintainer-authored push run must report a 64-character key (never print the
value):

```bash
docker logs --since 60s ngit-ci-deploy-coordinator-1 | grep -i "per-repo secrets"
```

Secrets are released only to runs whose *trigger event* was authored by a
confirmed maintainer, so a third-party PR gets an empty `NSEC_HEX` and the
upload step fails there by design. The secret never appears in git, in an
argument list or in a published event.

## Pushing to the ngit mirror

`git remote -v` in a normal clone shows only GitHub HTTPS remotes, and the
obvious approaches do not work. Verified 2026-09-12:

* the remote must be a `nostr://` URL —
  `nostr://npub1x677…/relay.ngit.dev/tollgate-module-basic-go`. Pushing the
  `https://relay.ngit.dev/…` grasp URL fails with
  `send-pack: protocol error: bad band #69`; the grasp serves fetch only.
* `git-remote-nostr` resolves its signer with libgit2 from the repository's own
  config file, so `git -c nostr.nsec=…` and `extensions.worktreeConfig` are both
  invisible to it; a linked worktree silently falls back to the machine-global
  key (`npub1xtzgnzz…`), which is not a maintainer and is rejected with
  `your nostr account … isn't listed as a maintainer of the repo`. Push from a
  plain clone whose own `.git/config` carries the maintainer `nostr.nsec`.
* the exit status is untrustworthy in both directions, so confirm success with
  `git ls-remote https://relay.ngit.dev/<npub>/tollgate-module-basic-go.git refs/heads/<branch>`.
* the push also mirrors to `gitnostr.com` under co-maintainer
  `npub1xh6njjx…`; that transport fails with
  `ERR authorisation failed: No state events in purgatory`. It is the fallback
  copy, not the push: the ref lands on `relay.ngit.dev`.

A helper that does all of this (and refuses to run with the wrong key) is at
`/home/c03rad0r/push-ngit-tmbg.sh` on CobradorWave.

### Known relay gap

The coordinator log shows `Relay did not accept published event relay=wss://gitnostr.com`,
so CI results land only on `relay.ngit.dev`. Reading results therefore means
querying that relay explicitly, which is why the commands below name it.

## Reading results

- `ngit ci status <commit|pr>` — job and workflow state for a commit or PR.
  (The installed CLI is v2.6.1, which has no `ci` and no `status` subcommand;
  gitworkshop.dev shows the same results against the commit, and `nak` reads
  them directly.)
- Published kinds: **39842** workflow progress, **9841** job result (carries the
  job's log tail), **9842** workflow result/conclusion. Each names the commit,
  the workflow path and the SHA-256 of the workflow file's content.

```bash
COORD_HEX=765cd47badcbbc4a38c7d0c57d5607663b484c20cd59773f9f7064487f9431e8
nak req -k 9842 -a "$COORD_HEX" -l 5  wss://relay.ngit.dev   # conclusions
nak req -k 9841 -a "$COORD_HEX" -l 20 wss://relay.ngit.dev   # per-job + log tail
```

Only the **tail** of a job log survives in the 9841 `content`, which is why the
port's reporting steps print their summary last.

## Authorization

**This deployment runs the `request-required` policy.** Ordinary push and PR
runs do not start until a maintainer publishes a standing **Service Request
(kind 9843)** naming the coordinator and this repository. One is already
outstanding for this repo (observed in the coordinator log as
`Recorded Service control event … action="Request"`), so push triggers fire.
Manual triggers (kind 9840) are one-shot and bypass the gate; a 9840 that pins
`w` = `.ngit/act/workflows/<file>` and its content SHA-256 replays any file
regardless of its `on:` clause.

## State of the checks right now

`gofmt -l .` reports three files on `main`
(`config_manager/config_schema.go`, `merchant/lightning_state_test.go`,
`merchant/quotes_wireformat_test.go`), so the `gofmt` step in `go-test.yml`
fails until they are formatted — `cd src && gofmt -w .`. The other three
commands pass (verified locally, `go1.25`/`go1.26`).

## Not run here (deliberately)

- **Router-visible behaviour.** Nothing exercises a real router — captive
  portal, firewall, Wi-Fi, `ndsctl`.
- **`trigger-build-os`.** The GitHub twin dispatched into
  `OpenTollGate/tollgate-os` with a cross-repo token. There is no token and no
  Actions there, so `build-package.yml`'s `os-handoff` job publishes a
  kind-30078 record (`d=tollgate-build-os-handoff/<build_id>`) and prints the
  `override_tollgate_wrt_version` value; **starting the build-os workflow is a
  manual step**.
- **act-image caveat.** ngit-ci runs jobs in `catthehacker/ubuntu:act-*` images,
  which are lighter than GitHub runner VMs. A red run there is an environment
  gap until the job's log tail says otherwise.
