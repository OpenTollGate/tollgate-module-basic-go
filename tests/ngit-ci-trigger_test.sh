#!/usr/bin/env bash
# Tests for scripts/ngit-ci-trigger.sh.
#
# These are guard tests: they never publish to a relay and never touch a real
# identity. They use a throwaway git repo, freshly generated throwaway keys,
# and an unreachable relay, so they are safe to run anywhere (including CI).
#
# The trust model under test: the operator DECLARES which identities a run may
# sign with (NGIT_TRUST: 64-hex pubkeys, npub1..., nsec1... entries; MAINT_HEX
# is the legacy single-entry form). No built-in authority ships in the script —
# an undeclared trust set must fail closed, and a signer outside the set must
# be refused. Key material (nsec) must never appear in any output.
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

# A generated key that is NOT trusted by default. `nak key generate` prints
# 64 hex chars (the SECRET); the repo's key files hold bech32. GEN_NPUB must
# encode the identity's PUBLIC half — `nak encode npub <secret-hex>` would
# faithfully bech32 the secret, which decodes back to the secret and matches
# nothing (bech32 is an encoding, not a derivation).
GEN_HEX=$(nak key generate 2>/dev/null | head -1)
GEN_NSEC=$(nak encode nsec "$GEN_HEX" 2>/dev/null)
[ -n "$GEN_NSEC" ] || { echo "SKIP: could not generate a test key (nak encode nsec)"; exit 0; }
printf '%s\n' "$GEN_NSEC" > "$TMP/signing.key"
printf 'not a key at all\n' > "$TMP/empty.key"
# A second throwaway identity, only to have "some other trusted key".
OTHER_HEX=$(nak key generate 2>/dev/null | head -1)
# The identity's true public half, derived the same portable way the script
# derives it (nak v0.16.2's `key public` rejects nsec-string args; the --sec
# event render is the version-stable surface).
TRUE_PUB=$(nak event --sec "$GEN_NSEC" -k 1 -c "tollgate-pubkey-probe" < /dev/null 2>/dev/null \
           | grep -o '"pubkey":"[0-9a-f]\{64\}"' | head -1 | cut -d'"' -f4)
printf '%s' "$TRUE_PUB" | grep -qE '^[0-9a-f]{64}$' \
  || { echo "SKIP: could not derive the test identity's public half"; exit 0; }
GEN_NPUB=$(nak encode npub "$TRUE_PUB" 2>/dev/null)

UNTRUSTED_HEX="ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

# run <trust> <keyfile> <workflow> <commit> [ref]
# Trust first, key second: the trust declaration is what varies per subtest.
run() {
  WT="$TMP/repo" NGIT_TRUST="$1" KEYFILE="$2" RELAYS="ws://127.0.0.1:1" \
    bash "$SCRIPT" "$3" "$4" "${5:-}" 2>&1
}
# run_legacy <maint-hex> <keyfile> <workflow> <commit> — the MAINT_HEX form.
run_legacy() {
  WT="$TMP/repo" MAINT_HEX="$1" KEYFILE="$2" RELAYS="ws://127.0.0.1:1" \
    bash "$SCRIPT" "$3" "$4" 2>&1
}
# run_notrusted <keyfile> <workflow> <commit> — no trust declared at all.
run_notrusted() {
  WT="$TMP/repo" KEYFILE="$1" RELAYS="ws://127.0.0.1:1" \
    env -u NGIT_TRUST -u MAINT_HEX bash "$SCRIPT" "$2" "$3" 2>&1
}

# 1. workflow outside .ngit/act/workflows is refused.
out=$(run "$UNTRUSTED_HEX" "$TMP/signing.key" ".github/workflows/test.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "must be a .ngit/act/workflows"; then
  ok "refuses a workflow outside .ngit/act/workflows"
else
  bad "refuses a workflow outside .ngit/act/workflows" "rc=$rc out=$out"
fi

# 2. a workflow that does not exist at the commit is refused.
out=$(run "$UNTRUSTED_HEX" "$TMP/signing.key" ".ngit/act/workflows/missing.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "not found in"; then
  ok "refuses a workflow file that is absent from the worktree"
else
  bad "refuses a workflow file that is absent from the worktree" "rc=$rc out=$out"
fi

# 3. a path that exists in the worktree but not at the commit is refused.
printf 'name: later\n' > .ngit/act/workflows/later.yml
out=$(run "$UNTRUSTED_HEX" "$TMP/signing.key" ".ngit/act/workflows/later.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "does not exist at commit"; then
  ok "refuses a workflow that is not present at the declared commit"
else
  bad "refuses a workflow that is not present at the declared commit" "rc=$rc out=$out"
fi
rm -f .ngit/act/workflows/later.yml

# 4. an undeclared trust set fails closed — no built-in authority exists.
out=$(run_notrusted "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no trusted identities declared"; then
  ok "fails closed when no trust is declared"
else
  bad "fails closed when no trust is declared" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'nsec1[0-9a-z]'; then
  bad "fail-closed message never prints key material" "output contained an nsec1 token"
else
  ok "fail-closed message never prints key material"
fi

# 5. a signer outside the declared trust set is refused, and never printed.
out=$(run "$UNTRUSTED_HEX" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "not in the declared trust set"; then
  ok "refuses a signing key outside the declared trust set"
else
  bad "refuses a signing key outside the declared trust set" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'nsec1[0-9a-z]'; then
  bad "never prints the private key on refusal" "output contained an nsec1 token"
else
  ok "never prints the private key on refusal"
fi

# 6. a key file with no nsec in it is refused.
out=$(run "$UNTRUSTED_HEX" "$TMP/empty.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no nsec1"; then
  ok "refuses a key file with no nsec in it"
else
  bad "refuses a key file with no nsec in it" "rc=$rc out=$out"
fi

# 7. an NGIT_TRUST entry that is none of hex/npub/nsec is refused by number,
#    without echoing the entry.
out=$(run "this-is-not-an-identity" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "NGIT_TRUST entry 1"; then
  ok "refuses an unparseable NGIT_TRUST entry"
else
  bad "refuses an unparseable NGIT_TRUST entry" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'this-is-not-an-identity'; then
  bad "unparseable-entry refusal never echoes the entry" "output echoed the entry"
else
  ok "unparseable-entry refusal never echoes the entry"
fi

# 8. happy path, trust declared as the NSEC itself ("validate against an nsec
#    you trust"): the public half is derived, signing accepted, publish
#    attempted against an unreachable relay — which fails loudly, proving the
#    guard is about identity, not network reachability.
out=$(run "$GEN_NSEC" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT" "refs/heads/main"); rc=$?
if printf '%s' "$out" | grep -q "sha256   : $WF_SHA"; then
  ok "prints the sha256 of the file's content at the declared commit"
else
  bad "prints the sha256 of the file's content at the declared commit" "expected $WF_SHA; out=$out"
fi
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "signs with an nsec-declared trusted identity, fails loudly when relays refuse"
else
  bad "signs with an nsec-declared trusted identity, fails loudly when relays refuse" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q 'nsec1[0-9a-z]'; then
  bad "never prints the private key on the happy path" "output contained an nsec1 token"
else
  ok "never prints the private key on the happy path"
fi

# 9. trust declared as the NPub form of the same identity.
out=$(run "$GEN_NPUB" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "accepts an npub-declared trusted identity"
else
  bad "accepts an npub-declared trusted identity" "rc=$rc out=$out"
fi

# 10. trust declared as the raw 64-hex pubkey.
out=$(run "$TRUE_PUB" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "accepts a raw-hex-declared trusted identity"
else
  bad "accepts a raw-hex-declared trusted identity" "rc=$rc out=$out"
fi

# 11. legacy MAINT_HEX single-entry form still works (with a deprecation note).
out=$(run_legacy "$TRUE_PUB" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "honours the legacy MAINT_HEX single-entry form"
else
  bad "honours the legacy MAINT_HEX single-entry form" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q "legacy single-entry trust form"; then
  ok "the legacy form announces its deprecation"
else
  bad "the legacy form announces its deprecation" "no deprecation warning in output"
fi

# 12. a multi-entry set: the signer matches the SECOND entry — set semantics,
#     not first-match or single-key.
out=$(run "$OTHER_HEX,$GEN_NPUB" "$TMP/signing.key" ".ngit/act/workflows/fake.yml" "$COMMIT"); rc=$?
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q "no event id"; then
  ok "matches any entry of a multi-identity trust set (comma-separated)"
else
  bad "matches any entry of a multi-identity trust set (comma-separated)" "rc=$rc out=$out"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
