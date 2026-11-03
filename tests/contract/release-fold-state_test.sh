#!/usr/bin/env bash
# release-fold-state_test.sh — planted-drift verdicts for the release-fold
# fence (tests/contract/check-release-fold-state.py).
#
# Offline, fixture-based: each case builds a minimal tree in a mktemp dir,
# plants the drift the fence exists to catch, and asserts the gate's exit.
# A fence that cannot fail is decoration — every case here must be able to
# go red against a broken gate.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$SCRIPT_DIR/check-release-fold-state.py"
PASS=0; FAIL=0

ok()   { echo "  ok   $1"; PASS=$((PASS+1)); }
bad()  { echo "  BAD  $1"; FAIL=$((FAIL+1)); }

mkfixture() { # <dir>
    local d="$1"
    mkdir -p "$d/changelog.d"
    printf '# TollGate changelog\n\n## [Unreleased]\n### Fixed\n\n- old entry\n\n## [v0.5.0] - 2026-07-03\n' > "$d/CHANGELOG.md"
    printf '# TollGate v0.5.0 notes\n' > "$d/RELEASE-NOTES.md"
}

echo "== final version, everything folded (the green path) =="
d=$(mktemp -d); mkfixture "$d"
printf '## [v0.6.0] - 2026-10-14\n' >> /dev/null # (section added below instead)
python3 - "$d" <<'EOF'
import sys, pathlib
d = pathlib.Path(sys.argv[1])
t = d.joinpath('CHANGELOG.md').read_text().replace('## [Unreleased]', '## [v0.6.0] - 2026-10-14\n## [Unreleased]')
d.joinpath('CHANGELOG.md').write_text(t)
d.joinpath('RELEASE-NOTES.md').write_text('# TollGate v0.6.0 notes\n')
EOF
if python3 "$GATE" v0.6.0 "$d" >/dev/null 2>&1; then ok "folded final passes"; else bad "folded final refused"; fi

echo "== final version, unfolded fragments (the rc2-rc4 drift) =="
d=$(mktemp -d); mkfixture "$d"
python3 - "$d" <<'EOF'
import sys, pathlib
d = pathlib.Path(sys.argv[1])
t = d.joinpath('CHANGELOG.md').read_text().replace('## [Unreleased]', '## [v0.6.0] - 2026-10-14\n## [Unreleased]')
d.joinpath('CHANGELOG.md').write_text(t)
d.joinpath('RELEASE-NOTES.md').write_text('# TollGate v0.6.0 notes\n')
EOF
printf -- '- **A fix.** ([#999](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/999)).\n' > "$d/changelog.d/999-a-fix.fixed.md"
if python3 "$GATE" v0.6.0 "$d" >/dev/null 2>&1; then bad "unfolded fragments accepted"; else ok "unfolded fragments refused"; fi

echo "== final version, fragments folded but section missing =="
d=$(mktemp -d); mkfixture "$d"
if python3 "$GATE" v0.6.0 "$d" >/dev/null 2>&1; then bad "missing section accepted"; else ok "missing [vX.Y.Z] section refused"; fi

echo "== final version, notes not rewritten =="
d=$(mktemp -d); mkfixture "$d"
python3 - "$d" <<'EOF'
import sys, pathlib
d = pathlib.Path(sys.argv[1])
t = d.joinpath('CHANGELOG.md').read_text().replace('## [Unreleased]', '## [v0.6.0] - 2026-10-14\n## [Unreleased]')
d.joinpath('CHANGELOG.md').write_text(t)  # notes still say v0.5.0
EOF
if python3 "$GATE" v0.6.0 "$d" >/dev/null 2>&1; then bad "stale notes accepted"; else ok "stale RELEASE-NOTES refused"; fi

echo "== pre-release, unfolded (the documented batch shape — must NOT block) =="
d=$(mktemp -d); mkfixture "$d"
printf -- '- **A fix.** ([#999](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/999)).\n' > "$d/changelog.d/999-a-fix.fixed.md"
if python3 "$GATE" v0.6.0-rc5 "$d" >/dev/null 2>&1; then ok "pre-release cuts unfolded, warning only"; else bad "pre-release refused (must warn, not block)"; fi

echo "== malformed version =="
d=$(mktemp -d); mkfixture "$d"
if python3 "$GATE" 0.6.0 "$d" >/dev/null 2>&1; then bad "unprefixed version accepted"; else ok "malformed version refused"; fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
