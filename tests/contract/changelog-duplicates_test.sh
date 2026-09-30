#!/usr/bin/env bash
# changelog-duplicates_test.sh — pins all three verdicts of
# tests/contract/check-changelog-duplicates.py: a clean changelog passes,
# a duplicate involving [Unreleased] fails and is named, and the same
# lead-in inside two frozen released sections passes (history is not
# rewritten).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK_PY="$REPO_ROOT/tests/contract/check-changelog-duplicates.py"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/tests/contract"
cp "$CHECK_PY" "$TMP/tests/contract/"

cat > "$TMP/CHANGELOG.md" <<'EOF'
## [Unreleased]

### Fixed

- **A gate close that fails is no longer treated as a close.** Body one.

### Changed / Internal

- **Dropped a stray tracked `.pyc`.** Body two.

## [v0.1.0]

### Fixed

- **An early fix.** Body three.
EOF

python3 "$TMP/tests/contract/check-changelog-duplicates.py" >/dev/null 2>&1 \
    || { echo "FAIL: unique lead-ins must pass"; exit 1; }

# The resurrect pattern: [Unreleased] holds the moved entry, a released
# section still holds the stale copy.
cat >> "$TMP/CHANGELOG.md" <<'EOF'

## [v0.2.0]

### Fixed

- **A gate close that fails is no longer treated as a close.** The stale,
  duplicate copy a section-level conflict resolution resurrected.
EOF

OUT="$(python3 "$TMP/tests/contract/check-changelog-duplicates.py" 2>&1)" \
    && { echo "FAIL: a duplicate involving [Unreleased] must fail"; exit 1; }
echo "$OUT" | grep -q "A gate close that fails" \
    || { echo "FAIL: failure output must name the duplicate: $OUT"; exit 1; }
echo "$OUT" | grep -q "Unreleased" \
    || { echo "FAIL: failure output must name the sections: $OUT"; exit 1; }

# Frozen history: the same lead-in twice inside released sections is
# tolerated — release notes are not rewritten — while a live [Unreleased]
# section is present (renaming it away would pin a different property).
python3 - "$TMP/CHANGELOG.md" <<'EOF'
import sys
p = sys.argv[1]
s = open(p).read()
s = s.replace("## [Unreleased]", "## [v0.3.0]", 1)
open(p, "w").write(s)
EOF
cat >> "$TMP/CHANGELOG.md" <<'EOF'

## [Unreleased]

### Fixed

- **A unique live entry.** Body four.
EOF
python3 "$TMP/tests/contract/check-changelog-duplicates.py" >/dev/null 2>&1 \
    || { echo "FAIL: duplicates wholly inside frozen released sections must pass while [Unreleased] is present"; exit 1; }

# An unclosed bold span must not swallow the next bullet's attribution:
# a bad conflict resolution that mangles a lead-in is exactly when the
# resurrect pattern must still be caught. The mangled bullet below has
# no closing `**`; with a bare lazy dot-all span its match would consume
# the next bullet's opener, leaving the verbatim duplicate undetected.
cat >> "$TMP/CHANGELOG.md" <<'EOF'
- **A gate close that fails
- **A second entry.** Body five.

## [v0.4.0]

### Fixed

- **A second entry.** The stale, resurrected copy.
EOF
OUT="$(python3 "$TMP/tests/contract/check-changelog-duplicates.py" 2>&1)" \
    && { echo "FAIL: an unclosed bold span must not hide the resurrected duplicate"; exit 1; }
echo "$OUT" | grep -q "A second entry." \
    || { echo "FAIL: failure output must name the duplicate hidden by the unclosed span: $OUT"; exit 1; }

echo "passed=4 failed=0 (unique passes; Unreleased duplicate fails, named; frozen history passes with Unreleased present; unclosed bold span cannot hide a duplicate)"
