# The Test workflow twins are rendered from ci.d/ fragments by ONE generator
# (#920): the committed copies are build products, so a hand-edit — or a
# fragment changed without re-rendering — is refused at commit time, not
# discovered in CI or at merge time.
if git diff --cached --name-only | grep -qE '^ci\.d/.*\.yml$|^ci\.d/README\.md$|^scripts/assemble-ci\.py$|^\.github/workflows/test\.yml$|^\.ngit/act/workflows/test\.yml$'; then
    echo "🔍 Checking the CI twins match their fragments (assemble → diff)..."
    python3 scripts/assemble-ci.py --check
fi
