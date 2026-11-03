#!/usr/bin/env bash
# go-test.sh — the test-only counterpart of go-battery.sh, safe to run
# from ANY directory of ANY checkout (normal clone or git worktree).
#
# WHY THIS EXISTS (#961): the repo root is not a Go workspace. src/ is a
# multi-module tree with its own root module at src/go.mod, and the
# repo-root go.mod's module (same import path, no packages) makes a raw
# `go` invocation at the repo root either fail with "main module ...
# does not contain package ..." or, with ./..., silently match zero
# packages and still exit 0. Both behave identically in fresh clones and
# in `git worktree` checkouts — this is a property of the module layout,
# not of worktrees. go-battery.sh stays the full pre-PR gate
# (fmt+vet+build+test); this script shares its module discovery (glob,
# never an enumeration) and its test flags, and additionally accepts
# module selectors so callers such as the parallel PR runner can drive
# one module at a time from any checkout.
#
# Usage:
#   scripts/go-test.sh                     # every module under src/
#   scripts/go-test.sh src/cli ...         # selected modules (repeatable)
#   make test                              # all modules
#   make test M=src/cli                    # one module via make
#
# GO_TEST_FLAGS overrides the default test flags
# (-race -count=1 -tags testenv — the same set go-battery runs).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

mapfile -t all_mods < <(cd "$ROOT" && find src -name go.mod | xargs -n1 dirname | sort)

if [ "${#all_mods[@]}" -eq 0 ]; then
    echo "go-test: no modules discovered under src/ — run this from a checkout of the repo" >&2
    exit 2
fi

if [ "$#" -gt 0 ]; then
    mods=()
    for arg in "$@"; do
        sel="${arg#./}"
        sel="${sel%/}"
        if [ -f "$ROOT/$sel/go.mod" ]; then
            mods+=("$sel")
        else
            echo "go-test: '$arg' is not a module directory (no go.mod found there)." >&2
            echo "  This repo is a multi-module tree: raw 'go test ./<dir>' from the repo" >&2
            echo "  root cannot work in ANY checkout (#961). Discovered modules:" >&2
            printf '    %s\n' "${all_mods[@]}" >&2
            exit 2
        fi
    done
else
    mods=("${all_mods[@]}")
fi

read -ra test_flags <<< "${GO_TEST_FLAGS:--race -count=1 -tags testenv}"

fail=0
for m in "${mods[@]}"; do
    echo "=== module: $m"
    if ! (cd "$ROOT/$m" && go test "${test_flags[@]}" ./...); then
        fail=1
    fi
done

if [ "$fail" -eq 0 ]; then
    echo "go-test: ${#mods[@]} module(s) green"
else
    echo "go-test: FAIL — see the module transcripts above" >&2
fi
exit "$fail"
