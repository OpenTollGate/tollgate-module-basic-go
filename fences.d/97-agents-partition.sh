# AGENTS.md is a stable core; the doctrine that grows lives in one file per
# area under docs/agents/, indexed in the core's "Topic doctrine" section
# with load directives (#920). The index and the directory are fenced in
# both directions: a topic file that is not indexed is unreachable doctrine,
# a dead or directive-less index entry is a suggestion pretending to be one.
if git diff --cached --name-only | grep -qE '^AGENTS\.md$|^docs/agents/'; then
    echo "🔍 Checking the AGENTS.md topic index (every topic listed, every listing real, every entry a load directive)..."
    python3 tests/contract/check-agents-partition.py
fi
