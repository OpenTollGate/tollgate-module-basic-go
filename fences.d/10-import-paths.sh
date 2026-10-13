# Stale import paths: modules moved under src/ must not be imported by
# their pre-move paths.
if git diff --cached --name-only | grep -q '\.go$'; then
    echo "🔍 Checking for stale import paths..."
    python3 tests/contract/check-import-paths.py
fi
