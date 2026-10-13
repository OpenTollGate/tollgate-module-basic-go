# The two-path build surface (manifest, eras, allowlist, agnosticism) —
# there is no third build path.
if git diff --cached --name-only | grep -qE 'build-inputs\.json|scripts/.*\.sh|packaging/.*\.sh|tests/cloud-lab/Dockerfile|\.github/workflows/.*\.yml'; then
    echo "🔍 Checking the two-path build surface (manifest, eras, allowlist, agnosticism)..."
    python3 tests/contract/check-build-surface.py
fi
