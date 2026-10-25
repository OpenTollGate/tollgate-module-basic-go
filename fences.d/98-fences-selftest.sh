# The fence fence: hooks/pre-commit dispatches fences.d/*.sh, and the
# dispatcher contract (complete if/fi blocks, parseable standalone, stable
# order) is what keeps a lane's added file from wedging every later commit.
# The verdicts harness plants the shape that broke main — a fragment missing
# its `fi` — and requires the hook to refuse the commit.
if git diff --cached --name-only | grep -qE '^hooks/pre-commit$|^fences\.d/'; then
    echo "🔍 Checking the fences dispatcher (fences.d verdicts)..."
    bash tests/contract/fences-dispatch_test.sh
fi
