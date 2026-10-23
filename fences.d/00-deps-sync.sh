# Dependency sync: every go.mod that carries a fork require must agree with
# the others. Triggered by any staged module or sum edit.
if git diff --cached --name-only | grep -q '\.mod$\|go\.sum$'; then
    echo "🔍 Checking dependency sync across go.mod files..."
    python3 tests/contract/check-deps-sync.py
fi
