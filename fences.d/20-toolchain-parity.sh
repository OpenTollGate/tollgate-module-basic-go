# Toolchain parity: the manifest is the single source of truth for every
# build-path toolchain version.
if git diff --cached --name-only | grep -qE 'build-inputs\.json|tests/cloud-lab/Dockerfile|\.github/workflows/.*\.yml'; then
    echo "🔍 Checking toolchain parity across build paths..."
    python3 tests/contract/check-toolchain-parity.py
fi
