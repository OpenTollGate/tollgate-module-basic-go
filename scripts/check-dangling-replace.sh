#!/usr/bin/env bash
# check-dangling-replace.sh — fail fast on replace directives pointing at
# directories that do not exist.
#
# WHY (#550): an uncommitted box-local `replace` whose target directory is
# missing (a gonuts-fork checkout half-set-up) makes every build/test run in
# that module — and anything resolving through it — die with a module-
# resolution error that reads exactly like a code regression. During #549's
# pre-push verification this produced a red `go test` for changes that were
# provably green: a false-negative verification run with nothing to do with
# the branch under test. The failure surfaces at go-battery time (before any
# module work), naming the go.mod and the missing path, instead of masquer-
# ading as a broken test.
#
# The sanctioned mechanism for per-developer overrides is a go.work file
# (gitignored, never committed or stashed), NOT editing go.mod:
#
#     go work edit -replace github.com/OpenTollGate/gonuts-tollgate=../third_party/gonuts-fork
#
# Only directory-path targets (./ ../ / absolute) are checked: a module-path
# target (github.com/foo/bar) is resolved by the module cache, and the Go
# toolchain already reports a missing directory loudly — but as a build error
# indistinguishable from a regression, which is precisely the failure mode
# this pre-flight check exists to disambiguate.
#
# Usage: scripts/check-dangling-replace.sh [root]   (root defaults to the
# repo root; the contract test passes a fixture tree)
set -uo pipefail

ROOT="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$ROOT" || exit 1

status=0

while IFS= read -r mod; do
    dir="$(dirname "$mod")"
    # Emit `target` for every `old => new` whose new value is a directory
    # path, in both shapes go.mod allows: single-line `replace a => b` and
    # grouped `replace ( ... )` blocks. Comments are stripped first; a
    # target with a trailing version (module-path shape) is skipped by the
    # path-anchored match.
    while IFS= read -r target; do
        [ -n "$target" ] || continue
        case "$target" in
            /*) path="$target" ;;
            *)  path="$dir/$target" ;;
        esac
        if [ ! -d "$path" ]; then
            printf '%s: replace target %s does not exist (%s)\n' "$mod" "$target" "$path" >&2
            status=1
        fi
    done < <(
        awk '
            # strip comments so a prose `#` cannot fake or hide a directive
            { line = $0; sub(/\/\/.*/, "", line) }
            /^replace[ \t]*\(/ { ingroup = 1; next }
            ingroup && /^\)/ { ingroup = 0; next }
            ingroup {
                if (line ~ /=>/) {
                    split(line, sides, /=>/)
                    tgt = sides[2]
                    sub(/^[ \t]+/, "", tgt)
                    split(tgt, words, /[ \t]+/)
                    if (words[1] ~ /^(\.\/|\.\.|\/)/) print words[1]
                }
                next
            }
            line ~ /^[ \t]*replace[ \t]/ {
                sub(/^[ \t]*replace[ \t]+/, "", line)
                if (line ~ /=>/) {
                    split(line, sides, /=>/)
                    tgt = sides[2]
                    sub(/^[ \t]+/, "", tgt)
                    split(tgt, words, /[ \t]+/)
                    if (words[1] ~ /^(\.\/|\.\.|\/)/) print words[1]
                }
            }
        ' "$mod"
    )
done < <(find src -name go.mod 2>/dev/null | sort)

if [ "$status" -ne 0 ]; then
    {
        echo ""
        echo "dangling replace directive(s) found — a missing target directory makes every"
        echo "go build/test in that module fail with what looks like a code regression (#550)."
        echo "Per-developer overrides belong in a gitignored go.work, never go.mod:"
        echo "  go work edit -replace <module>=<local-path>   # then commit nothing"
    } >&2
fi
exit "$status"
