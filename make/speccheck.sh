#!/usr/bin/env bash
# Spec-quote drift check (greatspectations vs cashubtc/nuts).
# NOTE: --comment-start is "// " (trailing space) — the marker must
# directly follow the comment start; "//" alone matches nothing and the
# check passes vacuously (the bug this script fixes).
# CLI NOTE: the greatspectations wheel installs `greatspectate` as its
# only console script (there is no `spectate`) — use the right name.
set -euo pipefail
cd "$(dirname "$0")/.."

# Pinned greatspectations ref (scheduled bump: update when releasing).
GS_REF=0f226495657e5febebd270b6a07b8562cca003d3

if ! command -v greatspectate >/dev/null 2>&1; then
    echo "Installing greatspectations (pinned $GS_REF)..."
    pip install --user --break-system-packages "git+https://github.com/rustyrussell/greatspectations.git@$GS_REF"
    export PATH="$PATH:$HOME/.local/bin"
fi
if [ ! -d nuts ]; then
    echo "Cloning NUT specs (HEAD)..."
    git clone --depth=1 https://github.com/cashubtc/nuts.git nuts
fi

echo "Checking spec quote drift against nuts HEAD..."
# Tool/config/usage errors exit non-zero (set -e is active here):
#   exit 0  = clean run, no drift
#   exit 1  = spec drift found (report only locally, loud banner)
#   exit 2+ = tool/config/usage error -> propagate and abort loudly
set +e
greatspectate check --config specquotes.toml --comment-start "// " --comment-continue "//" \
    --coverage=.greatspectate.cov \
    $(find src -name "*.go" -not -name "*_test.go")
RC=$?
set -e
if [ "$RC" -eq 0 ]; then
    echo ""
    echo "Spec coverage check (uncovered spec sections):"
    greatspectate coverage --config specquotes.toml --coverage .greatspectate.cov --format json 2>/dev/null | \
        python3 -c "
import json,sys
d=json.load(sys.stdin)
docs=d.get('documents',[])
total_uncovered=sum(len(doc.get('lines',[])) for doc in docs)
nuts_ids = ', '.join(f'NUT-{doc[\"id\"]:02d}' for doc in docs)
print(f'  {total_uncovered} spec lines across {len(docs)} NUT documents not directly quoted')
print(f'  Reviewed: {nuts_ids}')
" || true
    echo ""
    echo "All spec quotes match nuts HEAD — no drift."
    exit 0
elif [ "$RC" -eq 1 ]; then
    echo ""
    echo "########################################"
    echo "#      SPEC DRIFT DETECTED             #"
    echo "#  Update comments to match nuts HEAD  #"
    echo "########################################"
    exit 0
fi
echo "ERROR: greatspectate failed with exit code $RC (tool/config error)" >&2
exit "$RC"