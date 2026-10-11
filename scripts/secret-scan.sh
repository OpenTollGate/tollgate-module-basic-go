#!/usr/bin/env bash
#
# Shared secret-scanning gate for pre-commit hooks (gitleaks on the staged diff).
# Sourced/executed by the repo pre-commit hook. Fails CLOSED when gitleaks is
# present and finds a secret in staged changes; degrades to a loud warning
# only when gitleaks is absent — fail-closed with the tool, loud-skip without
# it (a missing tool never silently disables scanning forever; the warning is
# visible in every commit until installed, and GitHub push protection is the
# server-side net either way).
#
# Mirrors the PRTA#199 pattern (OpenTollGate/physical-router-test-automation):
# the credential-leak class ships exactly this way — an env backup or TLS key
# copied into the tree on a machine without hooks installed.
#
# Install gitleaks: https://github.com/gitleaks/gitleaks (brew install gitleaks,
# go install github.com/zricethezav/gitleaks/v8@latest, or the release binary).

set -uo pipefail

if ! command -v gitleaks &>/dev/null; then
    echo "⚠️  gitleaks not found — secret scanning SKIPPED for this commit." >&2
    echo "    Install it: brew install gitleaks   # or: go install github.com/zricethezav/gitleaks/v8@latest" >&2
    echo "    (baseline history scan: gitleaks git --redact)" >&2
    exit 0
fi

# 'gitleaks protect --staged' scans exactly the staged diff (fast; no history walk).
if ! gitleaks protect --staged --redact -v >&2; then
    echo "" >&2
    echo "❌ gitleaks found a secret pattern in your STAGED changes — commit refused." >&2
    echo "   If this is a false positive: add a scoped allowlist entry to .gitleaks.toml" >&2
    echo "   with a comment naming WHY it is safe — never weaken the ruleset." >&2
    echo "   If it is real: rotate, then remove the secret from the staged files." >&2
    echo "   (bypass for one commit you have personally reviewed: git commit --no-verify)" >&2
    exit 1
fi
