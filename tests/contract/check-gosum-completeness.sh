#!/usr/bin/env bash
# check-gosum-completeness.sh — the go.sum fence for the multi-module tree
# (#976; the #975 class).
#
# WHY
#   A merge can change one module's import surface (e.g. #938 adding
#   nip19 to package config_manager) and leave every IMPORTER's go.sum
#   incomplete. `go build` then fails with "missing go.sum entry" — but
#   only when THAT module is built, and no gate owned that beat:
#   check-deps-sync.py checks go.mod Require sync only, and per-PR
#   verification runs the PR's touched modules, not every importer. The
#   2026-10-16 night battery caught #975's three broken modules hours
#   after merge; this fence makes that check cheap, tree-wide and loud.
#
# WHAT
#   For every module directory under src/ (discovered by glob, never
#   enumerated — a new module must not need a fence edit), run the exact
#   probe that fails on the class: `go build ./...` from INSIDE the
#   module dir (#961 discipline), with GOWORK=off and -mod=readonly so
#   each module resolves against its own go.sum with no workspace or
#   re-tidy rescue. Build failures are categorized: the "missing go.sum
#   entry" class is the fence's namesake; any other build failure is
#   reported separately (still red — the battery gates builds generally,
#   but a fence that hides a red behind a category is decoration).
#
#   Test files are NOT compiled (test-only import gaps stay with the
#   battery's `go test` legs); this keeps the fence fast enough for
#   per-push lanes and local runs.
#
# Usage:
#   bash tests/contract/check-gosum-completeness.sh            # check the tree
#   bash tests/contract/check-gosum-completeness.sh --selftest # hermetic negative control
#
# Exit: 0 = every module builds against its own go.sum; 1 = red (modules
# named); 2 = usage/environment error.
set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"

# The probe: one module, its own sums, no rescue. Output goes to the
# caller; the exit code is the verdict.
probe_module() { # probe_module <module-dir>
    local mod="$1"
    ( cd "$mod" && GOWORK=off GOFLAGS=-mod=readonly go build ./... )
}

check_tree() {
    local mods=()
    local shopt_state
    shopt_state="$(shopt -p nullglob)" || true
    shopt -s nullglob
    local m
    for m in "$ROOT"/src/go.mod "$ROOT"/src/*/go.mod "$ROOT"/src/*/*/go.mod; do
        mods+=("$(dirname "$m")")
    done
    eval "$shopt_state"
    if [ "${#mods[@]}" -eq 0 ]; then
        echo "gosum-fence: no go.mod found under src/ — discovery is broken" >&2
        exit 2
    fi

    local gosum_red=() build_red=() mod rel
    for mod in "${mods[@]}"; do
        rel="${mod#"$ROOT"/}"
        local log
        log="$(mktemp)"
        if ! probe_module "$mod" >"$log" 2>&1; then
            if grep -q 'missing go.sum entry' "$log"; then
                gosum_red+=("$rel")
                echo "RED  $rel — missing go.sum entries:"
                grep 'missing go.sum entry' "$log" | sed 's/^/       /' | head -3
            else
                build_red+=("$rel")
                echo "RED  $rel — build failure (non-gosum class; see below)"
                tail -3 "$log" | sed 's/^/       /'
            fi
        else
            echo "ok   $rel"
        fi
        rm -f "$log"
    done

    echo
    echo "== gosum fence: ${#mods[@]} modules probed, ${#gosum_red[@]} go.sum-incomplete, ${#build_red[@]} other build failures"
    if [ "${#gosum_red[@]}" -gt 0 ] || [ "${#build_red[@]}" -gt 0 ]; then
        [ "${#gosum_red[@]}" = 0 ] || echo "== fix: run 'go mod tidy' (or 'go get <dep>') inside: ${gosum_red[*]}" >&2
        return 1
    fi
    return 0
}

# Hermetic negative control: in a throwaway copy of a real module, strip
# the go.sum lines of a dependency that module ACTUALLY builds (chosen
# from its own dependency graph, so the fixture cannot rot with dep
# churn), and require the probe to fail. A fence that cannot fail is
# decoration (#976 acceptance criterion).
selftest() {
    local src_mod="$ROOT/src/config_manager"
    [ -f "$src_mod/go.sum" ] || src_mod="$ROOT/src/utils"
    [ -f "$src_mod/go.sum" ] || { echo "selftest: no module with a go.sum found" >&2; exit 2; }

    local tmp
    tmp="$(mktemp -d /tmp/gosum-fence-selftest.XXXXXX)"
    cp -r "$src_mod" "$tmp/module"

    # A dependency this module truly compiles: first non-stdlib package in
    # its own `go list -deps` output. Strip BOTH of its go.sum lines (the
    # go.mod hash and the module zip h1:) — the probe must go red.
    local victim
    victim="$( (cd "$tmp/module" && GOWORK=off go list -deps ./... 2>/dev/null \
        | grep -E '^[a-z0-9.-]+\.[a-z]+/' | head -1) )"
    if [ -z "$victim" ]; then
        echo "selftest: could not pick a victim dependency from $src_mod" >&2
        rm -rf "$tmp"
        return 1
    fi
    local victim_mod="${victim%/*}"
    # Reduce a package path to its module root is unreliable in general;
    # instead strip every go.sum line whose path prefix matches the first
    # two path segments (covers module roots like github.com/nbd-wtf/…).
    local prefix
    prefix="$(printf '%s' "$victim_mod" | cut -d/ -f1-2)"
    if ! grep -q "^${prefix}" "$tmp/module/go.sum"; then
        prefix="$victim_mod"
    fi
    sed -i "\|^${prefix}|d" "$tmp/module/go.sum"

    if probe_module "$tmp/module" >/dev/null 2>&1; then
        echo "selftest FAIL: stripping '$prefix' go.sum lines did not break the build — the probe cannot detect the class" >&2
        rm -rf "$tmp"
        return 1
    fi
    echo "selftest ok: stripping '$prefix' go.sum lines in a temp copy of $(basename "$src_mod") fails the probe (the tree check names modules the same way)"
    rm -rf "$tmp"
    return 0
}

case "${1:-}" in
    --selftest) selftest ;;
    "") check_tree ;;
    *) echo "usage: $0 [--selftest]" >&2; exit 2 ;;
esac
