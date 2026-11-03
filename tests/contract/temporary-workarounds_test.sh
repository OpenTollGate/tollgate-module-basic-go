#!/usr/bin/env bash
# Contract test: the temporary-workaround registry (docs/temporary-workarounds.md).
#
# "TEMPORARY WORKAROUND" that nobody can see is permanent. The registry is
# the single source of truth for every marker shipped in src/, and this test
# fails the build when the code and the registry disagree — fail fast on
# workaround debt, at PR time rather than at a bench take two releases later
# (#768: the portal-poke workaround shipped ungated and untracked through an
# entire release train before anyone read its log line).
#
# Rules:
#   1. every "TEMPORARY WORKAROUND" marker in src/ (non-test Go) has a
#      registry row in docs/temporary-workarounds.md;
#   2. every registry row's marker file exists and still carries its marker;
#   3. every row carries all columns (id TW-n, issue link, added-in,
#      invocation contract, review-by);
#   4. TW-1's invocation contract's static half: TriggerCaptivePortalSession
#      has exactly one call site in non-test src/, and it is NOT in the
#      probe path (#768).
#
# Run from anywhere:  bash tests/contract/temporary-workarounds_test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
REGISTRY="$ROOT/docs/temporary-workarounds.md"
MARKER_RE='TEMPORARY WORKAROUND'

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1"; }
ok() { if [ "$1" = 0 ]; then pass "$2"; else fail "$2"; fi; }

[ -f "$REGISTRY" ]
ok $? "registry exists"

# --- rule 1: every marker is registered ------------------------------------

marker_files() {
    grep -rl --include='*.go' "$MARKER_RE" "$ROOT/src" 2>/dev/null |
        grep -v '_test\.go$' |
        sed "s|^$ROOT/||" | sort -u
}

unregistered=0
while IFS= read -r f; do
    [ -z "$f" ] && continue
    if ! grep -q "$f" "$REGISTRY"; then
        echo "  unregistered marker: $f"
        unregistered=1
    fi
done < <(marker_files)
ok $unregistered "every TEMPORARY WORKAROUND marker has a registry row"

# --- rule 2: every row's marker still exists -------------------------------

stale=0
while IFS= read -r row; do
    id=$(echo "$row" | awk -F'|' '{print $2}' | tr -d ' ')
    marker_cell=$(echo "$row" | awk -F'|' '{print $3}')
    m=$(echo "$marker_cell" | grep -o '`[^`]*`' | head -1 | tr -d '`')
    [ -n "$m" ] || { echo "  registry row $id: no backticked marker path: $row"; stale=1; continue; }
    [ -f "$ROOT/$m" ] || { echo "  registry row $id: file missing: $m"; stale=1; continue; }
    grep -q "$MARKER_RE" "$ROOT/$m" || { echo "  registry row $id: no marker in $m"; stale=1; }
done < <(grep '^| TW-' "$REGISTRY")
ok $stale "every registry row points at a live marker"

# --- rule 3: rows are complete ---------------------------------------------

incomplete=0
row_count=0
while IFS= read -r row; do
    row_count=$((row_count + 1))
    # 6 content cells: id | marker | issue | added-in | contract | review-by
    cells=$(echo "$row" | awk -F'|' 'NF>=8 {print "ok"}')
    [ "$cells" = "ok" ] || { echo "  row has missing columns: $row"; incomplete=1; }
    echo "$row" | grep -qE '\| TW-[0-9]+ \|' || { echo "  row id malformed: $row"; incomplete=1; }
    echo "$row" | grep -q 'issues/[0-9]' || { echo "  row issue link missing: $row"; incomplete=1; }
    for cell in 5 6 7; do
        echo "$row" | awk -F'|' -v c=$cell 'length($c)<3 {exit 1}' || { echo "  row column empty: $row"; incomplete=1; }
    done
done < <(grep '^| TW-' "$REGISTRY")
[ "$row_count" -gt 0 ]
ok $? "registry has at least one row (it is the single source of truth)"
ok $incomplete "every registry row is complete"

# --- rule 4: TW-1 call-site contract ---------------------------------------
# Call sites only: a receiver-qualified call ".TriggerCaptivePortalSession("
# never matches the interface declaration or the method definition.

callsites=$(grep -rn --include='*.go' '\.TriggerCaptivePortalSession(' "$ROOT/src" 2>/dev/null |
    grep -v '_test\.go:' || true)
n=$(printf '%s\n' "$callsites" | grep -c . || true)

one_call=0; [ "$n" = 1 ] || one_call=1
ok $one_call "TriggerCaptivePortalSession has exactly one non-test call site, found $n (#768)"

in_manager=0
printf '%s\n' "$callsites" | grep -q 'upstream_session_manager/upstream_session_manager\.go' || in_manager=1
ok $in_manager "the one call site is the manager's adoption path, not the probe path (#768)"

# --- verdict ----------------------------------------------------------------

echo
if [ "$fails" = 0 ]; then
    echo "temporary-workarounds registry: PASS ($cases checks)"
    exit 0
else
    echo "temporary-workarounds registry: FAIL ($fails of $cases checks)"
    echo "Register the marker in docs/temporary-workarounds.md (id, issue, added-in, invocation contract, review-by) or remove the workaround."
    exit 1
fi
