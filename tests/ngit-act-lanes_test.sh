#!/usr/bin/env bash
# Offline test for the ngit act lane split (#520): the shape of the lanes the
# coordinator actually executes (.ngit/act/workflows/), which no other test
# pins. Three invariants:
#
#   1. the split itself — test.yml carries the Go/contract legs, test-gates.yml
#      carries exactly packaging-suites/hygiene/release-check-fast, so the
#      heaviest push invocation is no longer one 16-job file on a runner capped
#      at 2 concurrent jobs / 1800 s;
#   2. no job-level `if:` other than always() in ANY act workflow — a job
#      skipped by an if: never publishes a kind 9841, and act's schema
#      validator rejects whole files for less (.ngit/README.md, port table);
#      release-check-fast shipped exactly this bug and was one of the two jobs
#      that never published;
#   3. no act job depends on git metadata — `git grep` in hygiene was vacuously
#      green on a checkout where git rev-parse reports "not a git repository".
#
# Also parses every act workflow as YAML, so an edit that breaks the file the
# coordinator reads fails here first.
#
# Usage: bash tests/ngit-act-lanes_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

ACT=.ngit/act/workflows

# ------------------------------------------------------------- YAML well-formed
yaml_broken=""
for f in "$ACT"/*.yml; do
    if ! python3 -c 'import sys, yaml; yaml.safe_load(open(sys.argv[1]))' "$f" 2>/dev/null; then
        yaml_broken="$yaml_broken $f"
    fi
done
if [ -z "$yaml_broken" ]; then
    ok "every $ACT/*.yml parses as YAML"
else
    bad " YAML parse failure:$yaml_broken"
fi

jobs_of() { # jobs_of <file> — job ids, one per line
    python3 -c '
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
for j in (doc.get("jobs") or {}):
    print(j)
' "$1"
}

# --------------------------------------------------------------------- the split
GATES_JOBS="$(jobs_of "$ACT/test-gates.yml" | sort | tr '\n' ' ')"
TEST_JOBS="$(jobs_of "$ACT/test.yml" | sort | tr '\n' ' ')"
if [ "$GATES_JOBS" = "hygiene packaging-suites release-check-fast " ]; then
    ok "test-gates.yml declares exactly the three gate jobs (got: $GATES_JOBS)"
else
    bad "test-gates.yml job set drifted (got:$GATES_JOBS)"
fi

moved_missing=""
for j in packaging-suites hygiene release-check-fast; do
    case " $TEST_JOBS " in *" $j "*) moved_missing="$moved_missing $j" ;; esac
done
if [ -z "$moved_missing" ]; then
    ok "test.yml no longer carries the moved gate jobs"
else
    bad "test.yml still declares the moved job(s):$moved_missing"
fi

kept_missing=""
for j in go-test main-test contract-lint build-purity deps-and-imports; do
    case " $TEST_JOBS " in *" $j "*) ;; *) kept_missing="$kept_missing $j" ;; esac
done
if [ -z "$kept_missing" ]; then
    ok "test.yml still declares every Go/contract leg it kept"
else
    bad "test.yml lost a Go/contract leg:$kept_missing"
fi

gates_triggers="$(python3 -c '
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
on = doc.get(True) or doc.get("on") or {}
print(sorted(on.get("push", {}).get("branches", [])), sorted(on.get("pull_request", {}).get("branches", [])))
' "$ACT/test-gates.yml")"
if [ "$gates_triggers" = "['develop', 'main', 'master'] ['develop', 'main', 'master']" ]; then
    ok "test-gates.yml fires on the same push/PR branches as the test lane"
else
    bad "test-gates.yml trigger drift (got: $gates_triggers)"
fi

# ------------------------------------------------- no job-level if: beyond always()
# A job-level `if:` sits at exactly four spaces of indent under its job id;
# step-level if: (upload steps use `if: always()`) is deeper and unaffected.
# `needs.<job>.outputs` guards are allowed: they are cross-job flow control
# the runner evaluates itself (router-test.yml's hardware gate), unlike an
# environment-dependent guard such as github.ref, which a payload-synthesizing
# coordinator can silently mis-evaluate — the #520 class this pins.
violations="$(grep -nE '^    if: ' "$ACT"/*.yml \
    | grep -v 'always()' \
    | grep -vE 'if: *needs\.' || true)"
if [ -z "$violations" ]; then
    ok "no environment-dependent job-level if: in any act workflow"
else
    bad "job-level if: outside always()/needs. (a skipped job publishes no 9841):"
    printf '%s\n' "$violations" | sed 's/^/       /'
fi

# --------------------------------------------- no git-metadata dependence in jobs
# `git grep` is the proven vacuous-pass vector (hygiene, #520): on a checkout
# with no git metadata it exits 2 and an if/then shape reads that as "no
# matches". `git rev-list ... || echo 0` in the release shards is the
# documented height fallback (.ngit/README.md) and is deliberately allowed,
# as are comment lines — this greps runnable lines only.
git_dependent="$(grep -n 'git grep' "$ACT"/*.yml | grep -vE '^[^:]+:[0-9]+: *#' || true)"
if [ -z "$git_dependent" ]; then
    ok "no act job greps through git metadata the runner's checkout lacks"
else
    bad "act job(s) depend on git metadata the runner's checkout does not carry:"
    printf '%s\n' "$git_dependent" | sed 's/^/       /'
fi

# ------------------------------------------- the fixed gate jobs keep their fixes
if grep -q 'grep -rEn --exclude-dir=.git' "$ACT/test-gates.yml"; then
    ok "hygiene scans the tree with grep -rEn, not git grep"
else
    bad "hygiene lost its grep -rEn scan (git grep is vacuous on act checkouts)"
fi
if grep -q 'GITHUB_REF' "$ACT/test-gates.yml"; then
    ok "release-check-fast gates main-only INSIDE the step (ref logic as bash)"
else
    bad "release-check-fast lost its in-step ref gate"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
