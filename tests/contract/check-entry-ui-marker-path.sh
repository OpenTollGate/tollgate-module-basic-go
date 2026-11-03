#!/usr/bin/env bash
# Contract: the entry-UI marker file (/etc/tollgate/entry-ui-mapping, D4 of
# docs/architecture/default-ui-and-entry-port-decision.md) is ONE file read by
# every half of the mapping — 99-tollgate-setup's shell repair, the daemon's
# entry_ui default population (src/config_manager, #928), and the tollgate-cli
# resolver (src/cmd/tollgate-cli/ui.go). The readers live in different
# languages and different Go modules, so the path is declared more than once;
# that duplication is only survivable while every copy is the SAME value — a
# reader pointed at a second spelling silently sees "marker absent" and
# repairs/populates the legacy mapping on a fully-vendored router (or vice
# versa), the exact torn-pair class D4 exists to prevent.
#
# Two directions, because either alone can be fooled:
#
#   1. Every path-shaped mention of the marker under packaging/, src/, docs/
#      and tests/ resolves to ONE value (discovered by grep at run time, so a
#      new declaration joins the contract automatically). Catches partial
#      renames that keep the entry-ui-mapping suffix.
#   2. Every direct string assignment to the marker variables carries the
#      canonical value, and the canonical default is still declared on both
#      the shell and the Go side. Catches a whole-site rename that direction 1
#      would silently shrink away.
#
# Env-override redirects (TOLLGATE_ENTRY_UI_MARKER="$dir/…") and test-seam
# redirects (filepath.Join) are exempt: the override seam is meant to point
# elsewhere; the DEFAULTS are the fact. Mutation verdicts: change any single
# copy's default path — either direction — and this check must refuse.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
canonical='/etc/tollgate/entry-ui-mapping'
marker_re='(?<![A-Za-z0-9._/-])(/[A-Za-z0-9._/-]*entry-ui-mapping)(?![A-Za-z0-9_-])'
# The env-override assignment shape: value begins with $ but not with ${
# (99's own "${TOLLGATE_ENTRY_UI_MARKER:-…}" default must NOT be exempt).
override_re='TOLLGATE_ENTRY_UI_MARKER="\$[^{]'
fail=0

# -- direction 1: one distinct path value everywhere -------------------------

lines="$(
    grep -rhP "$marker_re" \
        "$root/packaging" "$root/src" "$root/docs" "$root/tests" 2>/dev/null \
    | grep -vE "$override_re" \
    || true
)"
tokens="$(printf '%s\n' "$lines" | grep -oP "$marker_re" || true)"

if [ -z "$tokens" ]; then
    echo "entry-ui-marker-path: FAIL — no declaration of the marker path found"
    echo "  (the fence greps packaging/ src/ docs/ tests/; if the marker moved,"
    echo "   every reader must move in the same change — and this fence with them)"
    exit 1
fi

distinct="$(printf '%s\n' "$tokens" | sort -u)"
count="$(printf '%s\n' "$distinct" | grep -c .)"

if [ "$count" -ne 1 ] || [ "$distinct" != "$canonical" ]; then
    echo "entry-ui-marker-path: FAIL — the entry-UI marker path is not uniformly $canonical:"
    printf '%s\n' "$distinct" | sed 's/^/    /'
    echo "  Declaration sites:"
    printf '%s\n' "$lines" | grep -nP "$marker_re" | sed 's/^/    /' | head -30
    fail=1
fi

# -- direction 2: direct assignments carry the canonical value ---------------

assign_hits="$(
    grep -rnE '(ENTRY_UI_MARKER|entryUIMarkerPath)[[:space:]]*=[[:space:]]*"' \
        "$root/packaging" "$root/src" "$root/tests" 2>/dev/null \
    | grep -vE "$override_re" \
    | grep -v 'check-entry-ui-marker-path.sh' \
    || true
)"
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    case "$hit" in
    *"$canonical"*) ;;
    *)
        echo "entry-ui-marker-path: FAIL — marker assignment does not use $canonical:"
        echo "    $hit"
        fail=1
        ;;
    esac
done <<EOF
$assign_hits
EOF

# Both halves must still declare the canonical default at all.
if ! grep -rq -- ":-$canonical}" "$root/packaging" "$root/src" 2>/dev/null; then
    echo "entry-ui-marker-path: FAIL — no shell declaration of the marker default ($canonical) remains"
    fail=1
fi
if ! grep -rqF "entryUIMarkerPath = \"$canonical\"" "$root/src" 2>/dev/null; then
    echo "entry-ui-marker-path: FAIL — no Go declaration of the marker path ($canonical) remains"
    fail=1
fi

if [ "$fail" -ne 0 ]; then
    exit 1
fi
echo "entry-ui-marker-path: PASS (every declaration uses $canonical)"
