# One logical change, one CHANGELOG entry: a staged CHANGELOG edit must
# not leave the same bold lead-in in two sections (the duplicate a
# section-level conflict resolution can resurrect).
if git diff --cached --name-only | grep -q '^CHANGELOG\.md$'; then
    echo "🔍 Checking CHANGELOG for duplicate entries..."
    python3 tests/contract/check-changelog-duplicates.py
fi
