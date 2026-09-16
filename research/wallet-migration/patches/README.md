# Patch bundle — the code PRs, archived on the research branch

These are `git format-patch` bundles of every code PR this programme opened, so
the research branch alone reproduces all of the work even if a fork branch is
deleted or an upstream PR is closed.

| File | Repo | PR | Head | Base | Commits |
|---|---|---|---|---|---|
| `395-wallet-sidecar.patch` | `OpenTollGate/tollgate-module-basic-go` | #395 | `827b9ac` | `373770a` | 7 |
| `396-t16-decouple.patch` | `OpenTollGate/tollgate-module-basic-go` | #396 | `67adb09` | `373770a` | 2 |
| `116-feed-rc-verify.patch` | `OpenTollGate/physical-router-test-automation` | #116 | `a900a22` | `37a5e6d` | 1 |
| `117-wallet-sidecar-tests.patch` | `OpenTollGate/physical-router-test-automation` | #117 | `66e6b95` | `37a5e6d` | 6 |

Heads are the fork branch heads at 2026-09-16; bases are the upstream merge-bases
the PRs target.

## Apply

```bash
# in a checkout of the matching repo, on top of the recorded base
git am /path/to/research/wallet-migration/patches/395-wallet-sidecar.patch

# or inspect without applying
git apply --stat 395-wallet-sidecar.patch
```

The module patches apply to `tollgate-module-basic-go`; the harness patches apply
to `physical-router-test-automation`. Do not mix them across repos.

## Source of truth

The live branches (and their open PRs) remain the source of truth:

- `felixfelix-bot/tollgate-module-basic-go`: `pr/wallet-sidecar`, `pr/t16-decouple`
- `felixfelix-bot/physical-router-test-automation`: `pr/wallet-sidecar-tests`, `pr/feed-rc-verify`

Regenerate any patch with:

```bash
git -C <worktree> format-patch <base>..<branch> --stdout > <file>.patch
```
