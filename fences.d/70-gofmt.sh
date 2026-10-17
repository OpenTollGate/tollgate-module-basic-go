# Check gofmt on staged Go files (--diff-filter=d: gofmt cannot read
# deleted files; xargs -I{} keeps one file per line, spaces-safe)
if git diff --cached --name-only | grep -q '\.go$'; then
    unformatted=$(git diff --cached --name-only --diff-filter=d -- '*.go' | xargs -I{} gofmt -l -- {})
    if [ -n "$unformatted" ]; then
        echo "❌ gofmt found unformatted files:"
        echo "$unformatted"
        echo ""
        echo "Run: gofmt -w <files>"
        exit 1
    fi
fi
