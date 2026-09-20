#!/usr/bin/env bash
# Tests for scripts/verify_publication.sh and scripts/ngit-matrix-expectations.sh.
#
# Offline: no relay is contacted, no key is used and nothing is published. `nak`
# and `curl` are replaced by record/replay shims on PATH, so the gate's decision
# logic — expectations, publisher filtering, mirror checking, scope narrowing and
# every failure shape — is exercised deterministically.
#
#   bash tests/verify_publication_test.sh
#
# Exits non-zero if any assertion fails.

set -uo pipefail

REPO_ROOT=$(git -C "$(dirname -- "$0")" rev-parse --show-toplevel)
VERIFY="$REPO_ROOT/scripts/verify_publication.sh"
MATRIX_HELPER="$REPO_ROOT/scripts/ngit-matrix-expectations.sh"

fail=0
pass=0
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; [ $# -gt 1 ] && printf '       %s\n' "$2"; }

for bin in jq sha256sum awk sed grep mktemp; do
  command -v "$bin" >/dev/null 2>&1 || { echo "SKIP: $bin not on PATH"; exit 0; }
done

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/bin"
BODIES="$TMP/bodies"
mkdir -p "$BIN" "$BODIES"

# --- shims ------------------------------------------------------------------
# `nak req` replays $FAKE_NAK_EVENTS (JSONL), filtered by -a and --tag key=value.
cat > "$BIN/nak" <<'SH'
#!/usr/bin/env bash
set -uo pipefail
[ "${1:-}" = "req" ] || { echo "fake nak: only 'req' is supported" >&2; exit 1; }
shift
authors=(); tags=()
while [ $# -gt 0 ]; do
  case "$1" in
    -a) authors+=("$2"); shift 2 ;;
    --tag) tags+=("$2"); shift 2 ;;
    -k|-l|--limit) shift 2 ;;
    -*) shift ;;
    *) shift ;;
  esac
done
[ -f "${FAKE_NAK_EVENTS:-/dev/null}" ] || exit 0
while IFS= read -r line; do
  [ -n "$line" ] || continue
  keep=1
  if [ ${#authors[@]} -gt 0 ]; then
    keep=0
    for a in "${authors[@]}"; do
      [ "$(printf '%s' "$line" | jq -r .pubkey)" = "$a" ] && keep=1
    done
  fi
  for t in "${tags[@]:-}"; do
    [ -n "$t" ] || continue
    key="${t%%=*}"; val="${t#*=}"
    [ "$(printf '%s' "$line" | jq -r --arg k "$key" '[.tags[] | select(.[0]==$k) | .[1]] | index($v) != null' --arg v "$val")" = "true" ] || keep=0
  done
  [ "$keep" = "1" ] && printf '%s\n' "$line"
done < "${FAKE_NAK_EVENTS:-/dev/null}"
SH

# `curl ... <url> -o <file>` writes the recorded body for <sha256> (the file name
# in $FAKE_CURL_DIR). A URL listed in $FAKE_CURL_FAIL answers nothing (mirror
# down); a URL listed in $FAKE_CURL_WRONG answers the wrong bytes.
cat > "$BIN/curl" <<'SH'
#!/usr/bin/env bash
set -uo pipefail
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
for bad_url in ${FAKE_CURL_FAIL:-}; do
  [ "$url" = "$bad_url" ] && exit 22
done
for wrong_url in ${FAKE_CURL_WRONG:-}; do
  if [ "$url" = "$wrong_url" ]; then printf 'not-the-artifact\n' > "$out"; exit 0; fi
done
name=$(basename "$url"); name="${name%%.*}"
body="$FAKE_CURL_DIR/$name"
[ -f "$body" ] || exit 22
cp "$body" "$out"
SH
chmod +x "$BIN/nak" "$BIN/curl"
export PATH="$BIN:$PATH"

# --- fixtures ---------------------------------------------------------------
PUB_CI=6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d
PUB_OLD=5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a
PUB_STRANGER=1111111111111111111111111111111111111111111111111111111111111111
RELAY_A=https://mirror-a.invalid
RELAY_B=https://mirror-b.invalid

printf 'artifact-a53\n' > "$TMP/a53.ipk"
printf 'artifact-x86\n' > "$TMP/x86.ipk"
printf 'artifact-apk\n' > "$TMP/a53.apk"
SHA_A53=$(sha256sum "$TMP/a53.ipk" | cut -d' ' -f1)
SHA_X86=$(sha256sum "$TMP/x86.ipk" | cut -d' ' -f1)
SHA_APK=$(sha256sum "$TMP/a53.apk" | cut -d' ' -f1)
cp "$TMP/a53.ipk" "$BODIES/$SHA_A53"; cp "$TMP/x86.ipk" "$BODIES/$SHA_X86"; cp "$TMP/a53.apk" "$BODIES/$SHA_APK"
export FAKE_CURL_DIR="$BODIES"

event() { # event <pubkey> <version> <channel> <arch> <format> <sha> [url ...]
  local pub="$1" v="$2" c="$3" arch="$4" fmt="$5" sha="$6"; shift 6
  local urls="[]"
  for u in "$@"; do urls=$(printf '%s' "$urls" | jq -c --arg u "$u" '. + [$u]'); done
  jq -cn --arg pub "$pub" --arg v "$v" --arg c "$c" --arg arch "$arch" --arg fmt "$fmt" \
    --arg sha "$sha" --argjson urls "$urls" \
    '{id: ("ev" + ($pub[0:4]) + $arch + $fmt), pubkey: $pub, kind: 1063, content: "",
      tags: ([["v", $v], ["c", $c], ["A", $arch], ["format", $fmt],
              ["compression", "none"], ["x", $sha], ["n", "tollgate-wrt"]]
             + ($urls | map(["url", .])))}'
}

V=ci-ngit-split.0.26fe889b
: > "$TMP/events.jsonl"
{
  event "$PUB_CI" "$V" dev aarch64_cortex-a53 ipk "$SHA_A53" "$RELAY_A/$SHA_A53.ipk" "$RELAY_B/$SHA_A53.ipk"
  event "$PUB_CI" "$V" dev x86_64 ipk "$SHA_X86" "$RELAY_A/$SHA_X86.ipk" "$RELAY_B/$SHA_X86.ipk"
  event "$PUB_CI" "$V" dev aarch64_cortex-a53 apk "$SHA_APK" "$RELAY_A/$SHA_APK.apk" "$RELAY_B/$SHA_APK.apk"
} > "$TMP/events.jsonl"
# A second version whose .apk was announced with a single mirror only.
printf '%s\n' "$(event "$PUB_CI" "$V" dev aarch64_cortex-a53 apk "$SHA_APK" "$RELAY_A/$SHA_APK.apk")" > "$TMP/events-single-url.jsonl"
export FAKE_NAK_EVENTS="$TMP/events.jsonl"

run_verify() { # run_verify <env...> -- <args...>
  env "$@" bash "$VERIFY" "${ARGS[@]}" 2>&1
}

expect_expectations="$TMP/expect-two"
printf 'aarch64_cortex-a53/ipk\nx86_64/ipk\n' > "$expect_expectations"

# 1. no arguments at all
out=$(bash "$VERIFY" 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "usage:"; then
  ok "refuses to run without arguments (exit 2)"
else
  bad "refuses to run without arguments (exit 2)" "rc=$rc out=$out"
fi

# 2. happy path: both pairs announced, both mirrors serve the right bytes
ARGS=("$V" dev -)
out=$(VERIFY_EXPECT="$(cat "$expect_expectations")" bash "$VERIFY" "${ARGS[@]}" 2>&1); rc=$?
if [ $rc -eq 0 ] && printf '%s' "$out" | grep -q "verify_publication: PASS"; then
  ok "passes when every expected pair is announced and mirrored"
else
  bad "passes when every expected pair is announced and mirrored" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q "ok: x86_64/ipk" && printf '%s' "$out" | grep -q "ok: aarch64_cortex-a53/ipk"; then
  ok "names each verified (arch, format) pair"
else
  bad "names each verified (arch, format) pair" "out=$out"
fi

# 3. expectation source `-` without VERIFY_EXPECT
out=$(bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "needs VERIFY_EXPECT"; then
  ok "refuses '-\` as expectation source without VERIFY_EXPECT (exit 2)"
else
  bad "refuses '-' as expectation source without VERIFY_EXPECT (exit 2)" "rc=$rc out=$out"
fi

# 4. malformed expectation
out=$(VERIFY_EXPECT="aarch64_cortex-a53" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "malformed expectation"; then
  ok "refuses a malformed expectation (exit 2, never a pass)"
else
  bad "refuses a malformed expectation (exit 2, never a pass)" "rc=$rc out=$out"
fi

# 5. a pair the channel does not have is named and fails
out=$(VERIFY_EXPECT="$(cat "$expect_expectations")
mips_24kc/ipk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "expected artifact missing from channel: mips_24kc/ipk"; then
  ok "names the missing (arch, format) pair and exits 1"
else
  bad "names the missing (arch, format) pair and exits 1" "rc=$rc out=$out"
fi

# 6. a version that does not exist is refused (the alpha1-class silent failure):
#    the channel has events, none of them is this version.
out=$(VERIFY_EXPECT="$(cat "$expect_expectations")" bash "$VERIFY" v0.9.9-does-not-exist dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "zero events match"; then
  ok "refuses a version that was never published (negative control)"
else
  bad "refuses a version that was never published (negative control)" "rc=$rc out=$out"
fi

# 6b. a channel with no announcements at all is refused before anything else
out=$(VERIFY_EXPECT="$(cat "$expect_expectations")" bash "$VERIFY" "$V" alpha - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "no kind-1063 events"; then
  ok "refuses a channel with no announcements at all (negative control)"
else
  bad "refuses a channel with no announcements at all (negative control)" "rc=$rc out=$out"
fi

# 7. announcements signed by an unrelated key are not accepted
printf '%s\n' "$(event "$PUB_STRANGER" "$V" dev aarch64_cortex-a53 ipk "$SHA_A53" "$RELAY_A/$SHA_A53.ipk" "$RELAY_B/$SHA_A53.ipk")" > "$TMP/stranger.jsonl"
out=$(FAKE_NAK_EVENTS="$TMP/stranger.jsonl" VERIFY_EXPECT="$(cat "$expect_expectations")" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "for either release publisher"; then
  ok "ignores announcements from a key that is not a release publisher"
else
  bad "ignores announcements from a key that is not a release publisher" "rc=$rc out=$out"
fi

# 8. the historical publisher key alone is accepted too (the two-key era)
printf '%s\n' "$(event "$PUB_OLD" "$V" dev aarch64_cortex-a53 ipk "$SHA_A53" "$RELAY_A/$SHA_A53.ipk" "$RELAY_B/$SHA_A53.ipk")" > "$TMP/old.jsonl"
out=$(FAKE_NAK_EVENTS="$TMP/old.jsonl" VERIFY_EXPECT="aarch64_cortex-a53/ipk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 0 ] && printf '%s' "$out" | grep -q "PASS"; then
  ok "accepts the historical GitHub Actions publisher key"
else
  bad "accepts the historical GitHub Actions publisher key" "rc=$rc out=$out"
fi

# 9. mirror down: one of the two mirrors answers nothing
out=$(FAKE_CURL_FAIL="$RELAY_B/$SHA_A53.ipk" VERIFY_EXPECT="aarch64_cortex-a53/ipk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "only 1/2 mirrors served a correct copy"; then
  ok "fails when only one mirror serves the artifact"
else
  bad "fails when only one mirror serves the artifact" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q "mirror down: mirror-b.invalid"; then
  ok "names the mirror that did not answer"
else
  bad "names the mirror that did not answer" "out=$out"
fi

# 10. sha256 mismatch against the x tag
out=$(FAKE_CURL_WRONG="$RELAY_A/$SHA_A53.ipk" VERIFY_EXPECT="aarch64_cortex-a53/ipk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "aarch64_cortex-a53/ipk: sha256 mismatch from"; then
  ok "fails and names the pair on a sha256 mismatch"
else
  bad "fails and names the pair on a sha256 mismatch" "rc=$rc out=$out"
fi

# 11. a single url tag cannot satisfy a two-mirror requirement
out=$(FAKE_NAK_EVENTS="$TMP/events-single-url.jsonl" VERIFY_EXPECT="aarch64_cortex-a53/apk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 1 ] && printf '%s' "$out" | grep -q "only 1 url tag(s) listed"; then
  ok "fails when an event carries fewer url tags than required mirrors"
else
  bad "fails when an event carries fewer url tags than required mirrors" "rc=$rc out=$out"
fi

# 12. format scope: narrowing is reported, and the excluded pair is not verified
printf 'aarch64_cortex-a53/ipk\nx86_64/ipk\naarch64_cortex-a53/apk\n' > "$TMP/expect-three"
out=$(VERIFY_SCOPE=ipk VERIFY_EXPECT="$(cat "$TMP/expect-three")" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 0 ] && printf '%s' "$out" | grep -q "SCOPE: ipk"; then
  ok "narrows the scope and passes on the in-scope pairs only"
else
  bad "narrows the scope and passes on the in-scope pairs only" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -q "NOT verified by this run" && printf '%s' "$out" | grep -q "aarch64_cortex-a53/apk"; then
  ok "lists the excluded expectations as NOT verified"
else
  bad "lists the excluded expectations as NOT verified" "out=$out"
fi

# 13. a scope that excludes everything is an error, not a pass
out=$(VERIFY_SCOPE=apk VERIFY_EXPECT="aarch64_cortex-a53/ipk" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "excluded every expectation"; then
  ok "refuses a scope that excludes every expectation (exit 2)"
else
  bad "refuses a scope that excludes every expectation (exit 2)" "rc=$rc out=$out"
fi

# 14. the GitHub lane's matrix-JSON source still works unchanged
MATRIX='{"include":[{"architecture":"aarch64_cortex-a53","ipk":true,"apk":true},{"architecture":"x86_64","ipk":true,"apk":false}]}'
out=$(VERIFY_MIRRORS=1 bash "$VERIFY" "$V" dev "$MATRIX" 2>&1); rc=$?
if [ $rc -eq 0 ] && printf '%s' "$out" | grep -q "ok: aarch64_cortex-a53/apk"; then
  ok "reads expectations from a build-matrix JSON (GitHub lane path)"
else
  bad "reads expectations from a build-matrix JSON (GitHub lane path)" "rc=$rc out=$out"
fi

# 15. expectations that are empty are an error, not a pass
out=$(VERIFY_EXPECT="" bash "$VERIFY" "$V" dev - 2>&1); rc=$?
if [ $rc -eq 2 ]; then
  ok "refuses an empty expectation set (exit 2)"
else
  bad "refuses an empty expectation set (exit 2)" "rc=$rc out=$out"
fi

# --- ngit-matrix-expectations.sh --------------------------------------------
# The release matrix is sharded across one workflow file per group of legs, so
# the gate reads the union of them; a single shard would verify a subset.
WF=("$REPO_ROOT"/.ngit/act/workflows/build-package-*.yml)
out=$(bash "$MATRIX_HELPER" "${WF[@]}" 2>/dev/null); rc=$?
if [ $rc -eq 0 ] && [ "$(printf '%s\n' "$out" | grep -c .)" = "8" ]; then
  ok "extracts the 8 compression=none (arch, format) pairs from the ngit matrix"
else
  bad "extracts the 8 compression=none (arch, format) pairs from the ngit matrix" "rc=$rc out=$out"
fi
if printf '%s' "$out" | grep -qx "aarch64_cortex-a53/apk" && printf '%s' "$out" | grep -qx "mips_24kc/ipk"; then
  ok "the extraction covers ipk and apk rows"
else
  bad "the extraction covers ipk and apk rows" "out=$out"
fi
out=$(bash "$MATRIX_HELPER" "${WF[@]}" --formats ipk 2>/dev/null)
if [ "$(printf '%s\n' "$out" | grep -c .)" = "6" ]; then
  ok "--formats ipk extracts only the 6 .ipk architectures"
else
  bad "--formats ipk extracts only the 6 .ipk architectures" "out=$out"
fi
out=$(bash "$MATRIX_HELPER" "${WF[@]}" --compression all --formats ipk 2>/dev/null)
if [ "$(printf '%s\n' "$out" | grep -c .)" = "6" ]; then
  ok "--compression all de-duplicates the UPX variants into the same pairs"
else
  bad "--compression all de-duplicates the UPX variants into the same pairs" "out=$out"
fi
printf 'name: no matrix here\non:\n  push:\n' > "$TMP/empty.yml"
out=$(bash "$MATRIX_HELPER" "$TMP/empty.yml" 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "no matrix rows"; then
  ok "fails closed when the workflow declares no matrix (never an empty set)"
else
  bad "fails closed when the workflow declares no matrix (never an empty set)" "rc=$rc out=$out"
fi
out=$(bash "$MATRIX_HELPER" "$TMP/does-not-exist.yml" 2>&1); rc=$?
if [ $rc -eq 2 ] && printf '%s' "$out" | grep -q "no such workflow file"; then
  ok "refuses a workflow file that does not exist"
else
  bad "refuses a workflow file that does not exist" "rc=$rc out=$out"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
