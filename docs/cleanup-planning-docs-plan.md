# Cleanup: Delete Stale Develop Branch + AGENTS.md + Remove Planning Docs

**Date:** 2026-06-04
**Status:** In Progress

## Context

The `develop` branch in `tollgate-module-basic-go` was correctly deleted from GitHub
after its content was merged into `main` via PRs #124/#147. It was incorrectly
re-pushed during the rebase task. All develop content (config schema, CLI --json,
test workflow) already exists on `main`.

## Tasks

### Task A: Delete stale `develop` branch from GitHub
- [ ] `git push github --delete develop` in `tollgate-module-basic-go`

### Task B: Add AGENTS.md to all 3 repos
- [ ] `OpenTollGate/tollgate-module-basic-go` — create `AGENTS.md` on `main`
- [ ] `OpenTollGate/tollgate-captive-portal-site` — create `AGENTS.md` on PR #11 branch
- [ ] `net4sats/configurationwizzard` — create `AGENTS.md` on cleanup branch

### Task C: Update .gitignore in all 3 repos
- [ ] `tollgate-module-basic-go` — ensure planning doc patterns cover all untracked docs
- [ ] `tollgate-captive-portal-site` — add planning doc patterns
- [ ] `configurationwizzard` — add planning doc patterns

### Task D: Remove tracked planning docs from configurationwizzard
- [ ] Delete `docs/issue-9-fix-plan.md`
- [ ] Delete `docs/part-b-integration-plan.md`
- [ ] Create branch `chore/cleanup-planning-docs`, push, open PR to `main`

### Task E: Push all changes
- [ ] Push tollgate-module-basic-go `main` to GitHub
- [ ] Push portal PR #11 branch to GitHub
- [ ] Push configwizzard cleanup branch to GitHub
- [ ] Open PR for configwizzard

## AGENTS.md Content (same for all repos)

```markdown
# AGENTS.md

## Rules for LLM Sessions

- Do NOT commit markdown planning documents (e.g., `*-plan.md`, `PLAN-*.md`,
  `TODO-*.md`, `MOCK-*.md`, `TEST-COVERAGE-*.md`, `PR*-DECOMPOSITION-*.md`,
  `docs/*-plan.md`, `docs/tmp/`).
- Planning documents should be added to `.gitignore` instead.
- Only commit production documentation (`README.md`, `CHANGELOG.md`,
  protocol specs, API docs).
```

## .gitignore Addition (same for all repos)

```
# LLM planning documents — not for release
*-plan.md
PLAN-*.md
TODO-*.md
MOCK-*.md
TEST-COVERAGE-*.md
PR*-DECOMPOSITION-*.md
docs/tmp/
```
