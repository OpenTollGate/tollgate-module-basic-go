# Changelog fragments carry a PR's changelog entry (changelog.d/README.md). A
# malformed one — wrong filename, unknown type, a body that is not the bullet —
# is refused here rather than when a release tries to fold it. The fold tool is
# what writes them into CHANGELOG.md; PRs do not edit the changelog directly.
if git diff --cached --name-only | grep -qE '^changelog\.d/.*\.md$'; then
    echo "🔍 Checking changelog fragments..."
    python3 scripts/changelog-assemble.py check
fi
