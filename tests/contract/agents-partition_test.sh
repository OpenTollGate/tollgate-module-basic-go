#!/usr/bin/env bash
# Contract test: tests/contract/check-agents-partition.py verdicts (#920).
#
# AGENTS.md carries a stable core plus an index of docs/agents/ topic
# files whose entries are LOAD DIRECTIVES. A fence that cannot fail is
# decoration, so every drift shape is planted in a fixture tree and the
# checker must refuse it BY NAME, then the real tree must pass:
#
#   - a topic file missing from the index        -> refused, file named
#   - an index entry whose file does not exist   -> refused, link named
#   - an index entry without a load directive    -> refused, entry named
#   - no Topic doctrine section at all           -> refused
#
# Offline only. Run from anywhere:  bash tests/contract/agents-partition_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-agents-partition.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

verdict() { # verdict <root> <want:accept|refuse> <label> [needle]
    local r="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$CHECK" --root "$r" 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle')"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
}

CORE='AGENTS.md core.

## Topic doctrine (docs/agents/) — load before you touch

- [docs/agents/firewall-engagement.md](docs/agents/firewall-engagement.md)
  — the nftables rules of engagement. Load before touching
  /etc/nftables.d/ or anything that reloads the firewall.
'

seed() { # seed <root>: a clean core + one topic file
    mkdir -p "$1/docs/agents"
    printf '%s' "$CORE" >"$1/AGENTS.md"
    printf '# Firewall engagement rules.\n' >"$1/docs/agents/firewall-engagement.md"
}

# --- the real tree passes ------------------------------------------------------

verdict "$ROOT" accept "the repo's AGENTS.md index and docs/agents/ agree"

# --- 1. a topic file missing from the index is refused and named ---------------

d="$TMP/unlisted"; seed "$d"
printf '# Lab rules.\n' >"$d/docs/agents/labgrid-testing.md"
verdict "$d" refuse "a topic file missing from the index" \
    "docs/agents/labgrid-testing.md: exists but is not in AGENTS.md"

# --- 2. a dead index link is refused and named ---------------------------------

d="$TMP/dead-link"; seed "$d"
printf '%s' "${CORE%- [docs/agents/firewall-engagement.md*}- [docs/agents/ghost-topic.md](docs/agents/ghost-topic.md)
  — nothing. Load before nothing at all.
" >"$d/AGENTS.md"
verdict "$d" refuse "an index entry whose file does not exist" \
    "index links docs/agents/ghost-topic.md, which does not exist"

# --- 3. an index entry without a load directive is refused ---------------------

d="$TMP/no-directive"; mkdir -p "$d/docs/agents"
{
    printf '%s' "AGENTS.md core.

## Topic doctrine (docs/agents/) — load before you touch

- [docs/agents/firewall-engagement.md](docs/agents/firewall-engagement.md)
  — the nftables rules of engagement. See also, maybe.
"
} >"$d/AGENTS.md"
printf '# Firewall engagement rules.\n' >"$d/docs/agents/firewall-engagement.md"
verdict "$d" refuse "an index entry without a load directive" \
    "has no 'Load before' directive"

# --- 4. no Topic doctrine section at all is refused ----------------------------

d="$TMP/no-section"; mkdir -p "$d/docs/agents"
printf '# A core with no index.\n' >"$d/AGENTS.md"
printf '# Orphan topic.\n' >"$d/docs/agents/firewall-engagement.md"
verdict "$d" refuse "a core with no Topic doctrine section" \
    "no '## Topic doctrine' section"

printf '\n%d case(s), %d failure(s)\n' "$cases" "$fails"
[ "$fails" -eq 0 ]
