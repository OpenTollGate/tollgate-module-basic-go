#!/usr/bin/env bash
# go-battery.sh — the canonical pre-PR Go gate across ALL modules.
#
# WHY A SCRIPT: src/ is a multi-module tree (16 nested go.mod files with
# replace directives); `go <cmd> ./...` from src/ covers ONLY the root
# module. A battery invoked that way silently skips every subpackage while
# reporting success. AGENTS.md and CONTRIBUTING.md point here, and the
# go-test CI lane mirrors this set — one implementation of the gate.
#
# Per module: gofmt (must print nothing), go vet, go build, and
# `go test -race -count=1 -tags testenv`. Fast-fail on the first module
# that breaks, like `go test` does within a module.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Pre-flight (#550): a replace directive whose target directory is missing
# (a half-set-up local fork) makes every module below fail with a
# module-resolution error indistinguishable from a code regression. Fail
# fast, naming the go.mod, before any module work runs.
bash "$ROOT/scripts/check-dangling-replace.sh" "$ROOT" || {
    echo "go-battery: refusing to run over dangling replace directives (see above)" >&2
    exit 1
}

mapfile -t mods < <(cd "$ROOT" && find src -name go.mod | xargs -n1 dirname | sort)

for m in "${mods[@]}"; do
    echo "=== module: $m"
    (
        cd "$ROOT/$m" || exit 1
        unformatted="$(gofmt -l .)"
        if [ -n "$unformatted" ]; then
            echo "gofmt: unformatted files (fix with: gofmt -w .):" >&2
            echo "$unformatted" >&2
            exit 1
        fi
        go vet ./...
        go build ./...
        go test -race -count=1 -tags testenv ./...
    )
done

echo "go-battery: ${#mods[@]} modules green"
