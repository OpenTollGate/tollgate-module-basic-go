# .ngit — Nostr CI (ngit-ci) configuration

This directory holds the Nostr CI configuration for this repository, mirroring
the GitHub Actions test pipeline so that the same test suite runs on Nostr
git (ngit) infrastructure.

## Layout

- `.ngit/act/workflows/test.yml` — a faithful, verbatim port of
  `.github/workflows/test.yml`. It runs the identical test suite (Go unit
  tests across the module matrix, main-package `testenv` tests, contract
  lint, build-purity, and dependency/import-path checks) under ngit-ci.

## Relationship to GitHub Actions

- `.github/workflows/` is **intentionally untouched**. The GitHub test CI
  remains the source of truth and continues to run on every push/PR to
  `main`, `master`, and `develop`.
- `.ngit/act/workflows/test.yml` is a mirror of that pipeline for Nostr CI.
  It is invisible to GitHub Actions (the `.ngit/` directory is not a GitHub
  workflow path), so adding it has zero effect on GitHub CI — this is the
  backwards-compatibility guarantee.

## Keeping the two in sync

When you change the GitHub test workflow (`.github/workflows/test.yml`),
port the same change to `.ngit/act/workflows/test.yml` so Nostr CI keeps
running the same suite. The port must stay a faithful copy.
