#!/usr/bin/env bash
# Contract test: tests/contract/check-toolchain-parity.py verdicts.
#
# The parity checker pins ONE toolchain truth (packaging/build-inputs.json's
# .go.version) across every build path. This harness plants each drift shape
# in a fixture tree and requires a refusal that NAMES it — a fence that
# cannot fail is decoration — then requires an aligned tree to pass.
#
# Run from anywhere:  bash tests/contract/toolchain-parity_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-toolchain-parity.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# A minimal but complete build-path fixture: manifest, a cloud-lab
# Dockerfile, a workflow with an expression pin and a container job, and a
# go.mod. Aligned, this must pass every check the real tree passes.
seed_tree() { # seed_tree <dir>
    mkdir -p "$1/packaging" "$1/tests/cloud-lab" "$1/.github/workflows" "$1/src/thing"
    printf '{"go": {"version": "1.26.8"}}\n' >"$1/packaging/build-inputs.json"
    printf 'ARG GO_VERSION=1.26.8\nFROM golang:${GO_VERSION}-bookworm AS builder\nRUN go build -trimpath -buildvcs=false -ldflags="-s" ./...\n' \
        >"$1/tests/cloud-lab/Dockerfile.tollgate"
    printf 'jobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo pin is ${{ steps.pins.outputs.go }} via expression\n  c:\n    runs-on: ubuntu-latest\n    container: golang:1.26.8-bookworm\n    steps:\n      - run: go test ./...\n' \
        >"$1/.github/workflows/test.yml"
    printf 'module example.com/thing\n\ngo 1.26.0\n' >"$1/src/thing/go.mod"
}

# require <dir> <want:accept|refuse> <label> [needle]
require() {
    local d="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$CHECK" --root "$d" 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle')"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
}

# --- aligned tree -----------------------------------------------------------

d="$TMP/aligned"; seed_tree "$d"
require "$d" accept "an aligned tree (pins, flags, directive, container) passes"

# --- check 4: go.mod directives vs the manifest ------------------------------

d="$TMP/above"; seed_tree "$d"
mkdir -p "$d/src/newer"
printf 'module example.com/newer\n\ngo 1.27.0\n' >"$d/src/newer/go.mod"
require "$d" refuse "a go.mod directive ABOVE the pin" "go 1.27.0 > manifest 1.26.8"

d="$TMP/at-pin"; seed_tree "$d"
printf 'module example.com/thing\n\ngo 1.26.8\n' >"$d/src/thing/go.mod"
require "$d" accept "a go.mod directive AT the pin is fine"

d="$TMP/below"; seed_tree "$d"
printf 'module example.com/old\n\ngo 1.24.2\n' >"$d/src/thing/go.mod"
require "$d" accept "a go.mod directive BELOW the pin is fine (it is a floor)"

d="$TMP/no-directive"; seed_tree "$d"
printf 'module example.com/thing\n' >"$d/src/thing/go.mod"
require "$d" refuse "a go.mod without a go directive" "no 'go' directive"

# --- check 5: workflow container image pins ----------------------------------

d="$TMP/container-drift"; seed_tree "$d"
printf 'jobs:\n  c:\n    runs-on: ubuntu-latest\n    container: golang:1.25.0-bookworm\n    steps:\n      - run: go test ./...\n' \
        >"$d/.github/workflows/test.yml"
require "$d" refuse "a drifted container golang: image" "container golang:1.25.0-bookworm != manifest 1.26.8"

d="$TMP/container-image-block"; seed_tree "$d"
printf 'jobs:\n  c:\n    runs-on: ubuntu-latest\n    container:\n      image: golang:1.24.4-alpine\n    steps:\n      - run: go test ./...\n' \
        >"$d/.github/workflows/test.yml"
require "$d" refuse "a drifted container image: block" "golang:1.24.4-alpine != manifest"

# --- checks 1-2 still hold (regression guard for the extension) ---------------

d="$TMP/arg-drift"; seed_tree "$d"
printf 'ARG GO_VERSION=1.25.0\nFROM golang:${GO_VERSION}-bookworm\nRUN go build -trimpath -buildvcs=false ./...\n' \
    >"$d/tests/cloud-lab/Dockerfile.tollgate"
require "$d" refuse "a drifted cloud-lab ARG fallback (pre-existing check)" "ARG GO_VERSION=1.25.0"

d="$TMP/literal-pin-drift"; seed_tree "$d"
printf 'jobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-go@v6\n        with:\n          go-version: 1.25.0\n' \
    >"$d/.github/workflows/test.yml"
require "$d" refuse "a drifted literal go-version pin (pre-existing check)" "go-version: 1.25.0 != manifest 1.26.8"

# --- verdict -----------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
