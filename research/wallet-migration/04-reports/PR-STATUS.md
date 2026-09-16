# PR status — wallet-migration / feed-RC work (2026-09-16)

All PRs were opened from `felixfelix-bot/*` forks into `OpenTollGate/*`. The
agent account has **read-only** upstream access, so none can be self-merged; a
maintainer with write access is required. Archived diffs: `../patches/`.

| PR | Repo | Branch (head SHA) | Base | Files | mergeState | Review | Landed by |
|---|---|---|---|---|---|---|---|
| #395 | tollgate-module-basic-go | `pr/wallet-sidecar` (`827b9ac`) | `373770a` | 14 | `BLOCKED` | `REVIEW_REQUIRED` | — |
| #396 | tollgate-module-basic-go | `pr/t16-decouple` (`67adb09`) | `373770a` | 3 | `BLOCKED` | `REVIEW_REQUIRED` | — |
| #116 | physical-router-test-automation | `pr/feed-rc-verify` (`a900a22`) | `37a5e6d` | 3 | `CLEAN` | none required | — |
| #117 | physical-router-test-automation | `pr/wallet-sidecar-tests` (`66e6b95`) | `37a5e6d` | 1 | `CLEAN` | none required | — |
| #12 | tollgate-installer | `pr/force-flash` | — | 4 | — | — | **`c03rad0r`** 2026-09-14 |
| #11 | tollgate-installer | `pr/health-window-firstboot` | — | — | — | — | merged (earlier) |

Notes:
- **No CI checks reported** on any of the open PR branches — fork PRs do not run
  the repos' workflows until a maintainer approves the run.
- Module PRs are `BLOCKED` on a required review (branch protection), not on
  failing checks.

## Merge-readiness actions (this session)

- **#395 / #396** — added the required `CHANGELOG.md` `[Unreleased]` entries and
  ran the repo's documented gate from `src/`: `gofmt -l .`, `go vet ./...`,
  `go build ./...`, `go test -race -count=1 -tags testenv ./...`; self-reviewed
  against `PR-REVIEW.md`.
- **#116 / #117** — self-reviewed against the harness repo conventions.

## What a maintainer needs to do

1. Approve/merge **#395** and **#396** (module) — review required.
2. Merge **#116** and **#117** (harness) — clean, needs a writer.
3. (Optional) Approve the workflow runs so CI executes on the PR branches first.

## Self-review vs the repos' `PR-REVIEW.md` (2026-09-16)

Executed the 13-criteria pass on each branch; fixed what it surfaced.

**#395 (module, `pr/wallet-sidecar`)**
- *Fixed:* missing `CHANGELOG.md` `[Unreleased]` entry (Added) — added.
- *Verified:* no coding-assistant attribution in any commit; **no `go.mod`/
  `go.sum` changes** (stdlib-only client → no new dependency surface); diff is a
  coherent whole (13 files, all under `src/tollwallet/`, no drive-by changes).
- *Gates:* `gofmt` clean; `go vet ./...`, `go build ./...`,
  `go test -race -count=1 -tags testenv ./...` green — run **both** in the root
  module and inside the nested `src/tollwallet` module (the root `./...` does not
  cover nested modules).
- *Surfaced, non-blocking (documented in the PR body):* the sidecar socket has
  no auth/peer-credential check (follow-up: `0600` perms + `SO_PEERCRED`);
  `OpenWallet` assumes the daemon is already running (supervision is init work).

**#396 (module, `pr/t16-decouple`)**
- *Fixed:* missing `CHANGELOG.md` entry (Changed / Internal) — added.
- Test-only, no production/dependency change, no attribution. Gates green in
  `src/merchant` (`-race -tags testenv`, 185s) and in the root module.

**#116 / #117 (harness)**
- No attribution; shells pass `bash -n`; python passes `py_compile`; the feed
  script verifies the published asset `sha256` against GitHub's digest. No
  actionable findings.

## Branch preservation

- The research docs live on `felixfelix-bot/tollgate-module-basic-go` branch
  `research/wallet-migration` and are mirrored by an upstream PR (opened from the
  fork) plus a fork tag, so the branch is not lost if a fork branch is deleted.
- The code PR diffs are archived verbatim in `../patches/`.
