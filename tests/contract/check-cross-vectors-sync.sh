#!/usr/bin/env bash
# Contract: the canonical cross-vectors file and its embedded copies agree.
#
# cross-vectors/tollgate-cross-vectors.json is the canonical home of the
# TollGate cross-implementation test vectors (#750); consumers EMBED a copy
# next to their tests (the cashu-cross-vectors convention) and must update
# it together with the canonical file. This check makes "update together"
# mechanical: a copy that drifts from the canonical bytes fails here, with
# the regeneration command that fixes all copies at once.
#
# It also refuses a canonical file that is not what it claims: unparseable
# JSON, a schema this tree does not know, no tampered-signature vector (the
# one case every consumer MUST refuse), or a missing provenance pointer.
#
# Exit 0 when every copy is byte-identical and the file is self-consistent.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CANONICAL="$ROOT/cross-vectors/tollgate-cross-vectors.json"

copies=(
  "$ROOT/src/tollgate_protocol/testdata/tollgate-cross-vectors.json"
)

fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }

printf 'check-cross-vectors-sync: root %s\n' "$ROOT"

if [ ! -f "$CANONICAL" ]; then
    fail "$CANONICAL is missing — the canonical vectors file IS this repo's contract"
    printf '\n1 cross-vectors sync check FAILED.\n' >&2
    exit 1
fi

# --- the file is what it claims ----------------------------------------------

check_json="$(python3 - "$CANONICAL" <<'PY'
import json, sys
with open(sys.argv[1]) as fh:
    data = json.load(fh)
problems = []
if data.get("schema") != 1:
    problems.append(f"schema {data.get('schema')!r} != 1")
vectors = data.get("vectors") or []
if not vectors:
    problems.append("no vectors")
if not any(v.get("tampered_signature") for v in vectors):
    problems.append("no tampered-signature vector (the must-fail case)")
if "cross-vectors/tollgate-cross-vectors.json" not in (data.get("provenance") or ""):
    problems.append("provenance does not name the canonical home")
for v in vectors:
    for field in ("name", "event", "cbor_hex", "expected"):
        if not v.get(field):
            problems.append(f"vector {v.get('name', '?')} lacks {field}")
for problem in problems:
    print(problem)
PY
)"
if [ -z "$check_json" ]; then
    pass "canonical file: schema 1, vectors present, tampered case present, provenance intact"
else
    fail "canonical file is not self-consistent:"
    printf '%s\n' "$check_json" | sed 's/^/        /' >&2
fi

# --- the ported and authored contracts are reviewable ------------------------

for contract in advertisement.cddl tollgate.cddl; do
    if [ -f "$ROOT/cross-vectors/$contract" ]; then
        pass "$contract is present"
    else
        fail "cross-vectors/$contract is missing"
    fi
done
if grep -q 'PORTED CANONICAL SCHEMA' "$ROOT/cross-vectors/tollgate.cddl" 2>/dev/null; then
    pass "the wire CDDL carries its port provenance banner"
else
    fail "tollgate.cddl lost its provenance banner — a silent local edit is undetectable"
fi

# --- every embedded copy is byte-identical ------------------------------------

for copy in "${copies[@]}"; do
    rel="${copy#"$ROOT"/}"
    if [ ! -f "$copy" ]; then
        fail "$rel is missing — the embedded copy IS the convention"
        continue
    fi
    if cmp -s "$CANONICAL" "$copy"; then
        pass "$rel is byte-identical to the canonical file"
    else
        fail "$rel drifted from cross-vectors/tollgate-cross-vectors.json — regenerate ALL copies together:"
        printf '        cd cross-vectors/generate && go run . -root ../..\n' >&2
    fi
done

# --- the Go pin is wired ------------------------------------------------------

if grep -rq 'tollgate-cross-vectors.json' "$ROOT/src/tollgate_protocol"/*_test.go 2>/dev/null; then
    pass "a Go test consumes the embedded copy (the pin is wired)"
else
    fail "no test references the embedded copy — it is decorative"
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d cross-vectors sync check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'Canonical cross-vectors and every embedded copy agree.\n'
