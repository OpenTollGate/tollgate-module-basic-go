#!/usr/bin/env bash
# Spec-quote drift check (greatspectations vs cashubtc/nuts).
# NOTE: --comment-start is "// " (trailing space) — the marker must
# directly follow the comment start; "//" alone matches nothing and the
# check passes vacuously (the bug this script version fixes).
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v greatspectate >/dev/null 2>&1; then
    echo "Installing greatspectations..."
    pip install --user --break-system-packages git+https://github.com/rustyrussell/greatspectations.git >/dev/null 2>&1 || pip install git+https://github.com/rustyrussell/greatspectations.git >/dev/null 2>&1
    export PATH="$PATH:$HOME/.local/bin"
fi
if [ ! -d nuts ]; then
    echo "Cloning NUT specs..."
    git clone --depth=1 https://github.com/cashubtc/nuts.git nuts
fi

echo "Checking spec quote drift..."
# Drift is a report, never a build failure here: exit 0 either way.
FILES=$(find src -name "*.go" -not -name "*_test.go")
greatspectate check --config specquotes.toml --comment-start "// " --comment-continue "//" $FILES || true
exit 0
