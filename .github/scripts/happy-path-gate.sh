#!/usr/bin/env bash
#
# Run the happy-path suite as a RELEASE GATE.
#
# Usage: happy-path-gate.sh --artifact <extracted-package-dir> [--out <dir>]
#
# The suite itself is tests/happy-path/run.sh (merged in #544). This wrapper adds
# what a release gate needs on a bare runner, and nothing else:
#
#   0. The identity fixture a router always has: a DHCP lease for the harness's
#      client, because the module resolves identity from the lease/ARP table and
#      refuses (post-#548) a client it cannot identify. See the block below.
#
#   1. --strict. The gate runs on an artifact built from THIS commit, and this
#      tip carries upstream #541 (/session-state) and a captive-portal bundle
#      that ships the in-page renewal CTA (#60). So an ABSENT expected surface
#      must FAIL, not SKIP. On an older published artifact those checks
#      legitimately SKIP; on a freshly built one their absence is the
#      renewal-after-expiry dead end, which is precisely what a SKIP used to
#      hide. Measured on the tip (13f9fcd4, x86_64 package built from source):
#      both checks PASS with --strict, where the published pre15 artifact
#      SKIPs them.
#
#   2. Exactly one carve-out, by name, for the suite's OWN known-issue list.
#      --strict also promotes every entry of tests/happy-path/known-issues.txt
#      back to fatal, and one entry is still open at this tip: the portal's
#      Lightning lane cannot create an invoice because the advertisement echoes
#      the mint URL without the trailing slash the wallet keys mints with
#      ("mint does not exist") - that is the mint-URL-normalisation work, not
#      this gate's, and the same suite explains why a permanently red check
#      stops being read. So a failure is fatal UNLESS its check id is listed in
#      tests/happy-path/known-issues.txt. The list is read from that file, never
#      hardcoded here: delete the line when the fix lands and the carve-out
#      disappears by itself.
#
# Everything else fails closed, including a non-zero exit that reports no failed
# check (a harness failure is never tolerated).
#
set -uo pipefail

ARTIFACT=""
OUT=""
while [ $# -gt 0 ]; do
    case "$1" in
        --artifact) ARTIFACT="${2:-}"; shift 2 ;;
        --out)      OUT="${2:-}";      shift 2 ;;
        -h|--help)  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done
if [ -z "$ARTIFACT" ]; then
    echo "usage: $0 --artifact <extracted-package-dir> [--out <dir>]" >&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
KNOWN_FILE="$REPO_ROOT/tests/happy-path/known-issues.txt"
LOG="$(mktemp "${TMPDIR:-/tmp}/hp-gate.XXXXXX.log")"

# ---------------------------------------------------------------------------
# 0. Give the runner the identity fixture a router always has.
#
# The module resolves a client's MAC from the DHCP lease file and the kernel ARP
# table (src/main.go: dhcpLeasePath = "/tmp/dhcp.leases"), and since #548 it
# REFUSES a client it cannot identify rather than substituting the sentinel. A
# router always holds a lease for a customer who is on the Wi-Fi, so on the
# router the harness's client resolves; a fresh CI runner holds none, and the
# suite then fails api:whoami (mac=), the enforcement check and the portal's
# live-module lane with `device-unresolved` - failures that say nothing about
# the artifact under test. The harness serves the portal on 127.0.0.2 and the
# module on 127.0.0.1, so the lease maps both to the client MAC the suite uses
# everywhere else (02:00:00:00:00:20).
#
# This is environment setup, not a relaxed assertion: with the lease present,
# every one of those checks is asserted exactly as before.
# ---------------------------------------------------------------------------
LEASE_FILE="${HP_LEASE_FILE:-/tmp/dhcp.leases}"
if [ ! -s "$LEASE_FILE" ]; then
    : > "$LEASE_FILE"
    printf '1700000000 02:00:00:00:00:20 127.0.0.1 hp-client *\n' >> "$LEASE_FILE"
    printf '1700000000 02:00:00:00:00:20 127.0.0.2 hp-client *\n' >> "$LEASE_FILE"
    echo "== happy-path gate: wrote the client lease fixture $LEASE_FILE"
    cat "$LEASE_FILE"
else
    echo "== happy-path gate: using the existing $LEASE_FILE"
    cat "$LEASE_FILE"
fi

SUITE_ARGS=(--artifact "$ARTIFACT" --strict)
[ -n "$OUT" ] && SUITE_ARGS+=(--out "$OUT")

echo "== happy-path gate: run.sh ${SUITE_ARGS[*]}"
bash "$REPO_ROOT/tests/happy-path/run.sh" "${SUITE_ARGS[@]}" 2>&1 | tee "$LOG"
RC=${PIPESTATUS[0]}

if [ "$RC" -eq 0 ]; then
    echo "== happy-path gate: PASS (suite exit 0)"
    exit 0
fi

KNOWN="$(awk '!/^[[:space:]]*#/ && NF {print $1}' "$KNOWN_FILE" 2>/dev/null | tr '\n' ' ')"
FAILED="$(sed -n 's/^HPFAILED//p' "$LOG" | tail -1)"
echo "== happy-path gate: suite exit $RC; failed:${FAILED:-<none reported>}"
echo "== happy-path gate: tolerated (known-issues.txt): ${KNOWN:-<none>}"

if [ -z "$FAILED" ]; then
    # Non-zero exit with no HPFAILED line means the harness broke, not the
    # product. Fail closed either way.
    echo "== happy-path gate: FAIL - suite exited $RC without reporting a failed check" >&2
    exit 1
fi

UNEXPECTED=""
for id in $FAILED; do
    case " $KNOWN " in
        *" $id "*) echo "   tolerated: $id" ;;
        *)         UNEXPECTED="$UNEXPECTED $id" ;;
    esac
done

if [ -n "$UNEXPECTED" ]; then
    echo "== happy-path gate: FAIL - happy path broken:${UNEXPECTED}" >&2
    exit 1
fi

echo "== happy-path gate: PASS - only documented known issues failed"
exit 0
