# Rebase & CI Alignment Plan

**Date:** 2026-06-04
**Status:** Complete

## Tasks

### Task 1: Rebase configurationwizzard PR #12
- [x] Clone fresh copy of `net4sats/configurationwizzard`
- [x] Checkout `feature/upstream-wifi-scan`
- [x] `git rebase main` — resolved 2 conflicts in `src/routes/captive-portal.tsx` and `docs/issue-9-fix-plan.md`
- [x] Verify `npm install && npm run build` — passed
- [x] Force-push to GitHub
- [x] Verify PR #12 shows mergeable on GitHub — MERGEABLE (UNSTABLE = CI running)

### Task 2: Rebase `develop` onto `main` in `tollgate-module-basic-go`
- [x] Checkout `develop` (pruned stale worktree first)
- [x] `git rebase github/main` — resolved 5 conflicts in `config_manager/`, `cli/server.go`, `main.go`, `test.yml`, `.gitignore`
- [x] Verify `go build ./...` — passed
- [x] Verify `go test ./...` — config_manager + tollgate-cli pass (root module fails on dev machine, expected)
- [x] Push to GitHub (new branch — `develop` was deleted from GitHub)
- [ ] Push to ngit remotes — relay.ngit.dev and ngit.orangesync.tech unreachable (deferred)

### Task 3: Update portal + configwizzard CI to match `tollgate-module-basic-go`
- [x] Update portal `.github/workflows/build-package.yml` on `feat/admin-spa-packaging`
  - [x] 4 Blossom servers + min-success=2 + redundant upload pattern
  - [x] Expanded relays (public + orangesync + ngit = 8 relays)
  - [x] Multi-url NIP-94 events
- [x] Update configwizzard `.github/workflows/build-package.yml` on `main`
  - [x] Same Blossom/relay changes
  - [x] Redundant upload + multi-url NIP-94
- [x] Push both to GitHub
- [x] Verify CI passes — portal: 6 ipks published, 6 NIP-94 events across 8 relays

### Task 4: Nudge @amperstrand on portal PR #11
- [x] Post comment on PR #11 — https://github.com/OpenTollGate/tollgate-captive-portal-site/pull/11#issuecomment-4621022896

## Infrastructure Reference

```
BLOSSOM_SERVERS: blossom.tollgate.me blossom.primal.net blossom1.orangesync.tech blossom2.orangesync.tech
RELAYS: relay.damus.io nos.lol nostr.mom relay.tollgate.me relay1.orangesync.tech relay2.orangesync.tech ngit1.orangesync.tech ngit2.orangesync.tech
BLOSSOM_MIN_SUCCESS: 2
```

## Outstanding

- ngit remays for `tollgate-module-basic-go develop` branch — relay.ngit.dev and ngit.orangesync.tech are unreachable. Retry when relays are back.
