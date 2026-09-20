#!/usr/bin/env bash
# Tests for scripts/ngit-ci-trigger.sh.
#
# These are guard tests: they never publish to a relay and never touch the real
# maintainer key. They use a throwaway git repo, a freshly generated key, and an
# unreachable relay, so they are safe to run anywhere (including CI).
#
#   bash tests/ngit-ci-trigger_test.sh
#
# Exits non-zero if any assertion fails.

set -uo pipefail

REPO_ROOT=$(git -C "$(dirname -- "$0")" rev-parse --show-toplevel)
SCRIPT="$REPO_ROOT/scripts/ngit-ci-trigger.sh"

fail=0
pass=0

ok()   { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad()  { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; [ $# -gt 1 ] && printf '       %s\n' "$2"; }

for bin in git nak sha256sum; do
  command -v "$bin" >/dev/null 2>&1 || { echo "SKIP: $bin not on PATH"; exit 0; }
done

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# A throwaway repo that carries a workflow file at a known commit.
mkdir -p "$TMP/repo/.ngit/act/workflows"
cd "$TMP/repo" || exit 1
git init -q .
git config user.email test@example.invalid
git config user.name "test"
printf 'name: fake\non:\n  push:\n    branches: [main]\n' > .ngit/act/workflows/fake.yml
git add -A
git commit -qm "add fake workflow"
COMMIT=$(git rev-parse HEAD)
WF_SHA=$(git show "$COMMIT:.ngit/act/workflows/fake.yml" | sha256sum | cut -d' ' -f1)

# A generated key that is deliberately NOT the maintainer key.
# `nak key generate` prints 64 hex chars; the repo's key files hold bech32.
GEN_HEX=$(nak key generate 2>/dev/null | head -1)
GEN_NSEC=$(nak encode nsec "$GEN_HEX" 2>/dev/null)
[ -n "$GEN_NSEC" ] || { echo "SKIP: could not generate a test key (nak encode nsec)"; exit 0; }
printf '%s\n' "$GEN_NSEC" > "$TMP/wrong.key"
printf 'not a key at all\n' > "$TMP/empty.key"

run() { # run <keyfile> <workflow> <commit> [ref]
  WT="$TMP/repo" KEYFILE="$1" RELAYS="ws://127.0.0.1:1" \
    bash "$SCRIPT" "$2" "$3" "${4:-}" 2>&1
}

# 1. workflow outside .ngit/act/workflows is refused.
out=$(run "$TMP/wrong.key" ".github/workflows/test.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "must be a .ngit/act/workflows"; then
  ok "refuses a workflow outside .ngit/act/workflows"
else
  bad "refuses a workflow outside .ngit/act/workflows" "rc=$rc out=$out"
fi

# 2. a workflow that does not exist at the commit is refused.
out=$(run "$TMP/wrong.key" ".ngit/act/workflows/missing.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "not found in"; then
  ok "refuses a workflow file that is absent from the worktree"
else
  bad "refuses a workflow file that is absent from the worktree" "rc=$rc out=$out"
fi

# 3. a path that exists in the worktree but not at the commit is refused.
printf 'name: later\n' > .ngit/act/workflows/later.yml
out=$(run "$TMP/wrong.key" ".ngit/act/workflows/later.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "does not exist at commit"; then
  ok "refuses a workflow that is not present at the declared commit"
else
  bad "refuses a workflow that is not present at the declared commit" "rc=$rc out=$out"
fi
rm -f .ngit/act/workflows/later.yml

# 4. a non-maintainer key is refused, and the key itself is never printed.
out=$(run "$TMP/wrong.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "is not the maintainer key"; then
  ok "refuses a signing key that is not the maintainer key"
else
  bad "refuses a signing key that is not the maintainer key" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'nsec1'; then
  bad "never prints the private key on refusal" "output contained an nsec1 token"
else
  ok "never prints the private key on refusal"
fi

# 5. a key file with no nsec in it is refused.
out=$(run "$TMP/empty.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no nsec1"; then
  ok "refuses a key file with no nsec in it"
else
  bad "refuses a key file with no nsec in it" "rc=$rc out=$out"
fi

# 6. happy path: correct sha256 computed from the commit, signing key accepted
#    (MAINT_HEX is set to that key's own pubkey), publish attempted. The relay
#    is unreachable, so the run fails at the publish -- which is the point: it
#    proves the guard is about identity, not about network reachability.
SIGNER=$(nak key public "$GEN_NSEC")
out=$(WT="$TMP/repo" KEYFILE="$TMP/wrong.key" MAINT_HEX="$SIGNER" RELAYS="ws://127.0.0.1:1" \
  bash "$SCRIPT" ".ngit/act/workflows/fake.yml" "$COMMIT" "refs/heads/main" 2>&1); rc=$?
if printf '%s' "$out" | grep -q "sha256   : $WF_SHA"; then
  ok "prints the sha256 of the file's content at the declared commit"
else
  bad "prints the sha256 of the file's content at the declared commit" "expected $WF_SHA; out=$out"
fi
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "fails loudly when every relay refuses the event"
else
  bad "fails loudly when every relay refuses the event" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'nsec1'; then
  bad "never prints the private key on the happy path" "output contained an nsec1 token"
else
  ok "never prints the private key on the happy path"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
