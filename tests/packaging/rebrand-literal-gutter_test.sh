#!/usr/bin/env bash
# Gutter test: the re-brand's name must never appear in this repository's tree.
#
# TollGate is the non-profit reference implementation of the protocol;
# the commercial re-brand is a SEPARATE build (its own org, its own repo, its
# own webroot and its own uhttpd section, passed in as the generic `brand`
# build input). Upstream must not carry that brand as a literal anywhere —
# not in the setup script, not in its docs, not in the CHANGELOG — because a
# bare literal is how the module grew a SECOND :8090 admin writer in the first
# place: a brand-gated legacy configUI section that shipped to every consumer
# and fought the portal-staged board (uhttpd.admin) for the port.
#
# This is a working-tree gutter, not a history rewrite: the name legitimately
# appears in old commits, and that is accepted. What it must not do is come
# back into the tree — so this test fails the moment a literal is reintroduced
# on a code, packaging, build or test surface (packaging, src, scripts,
# .github, .ngit, tests, hooks, Makefile, root-level scripts).
#
# Prose exemption: descriptive references in documentation are allowed. The
# merged discovery-signaling decision (#621) names the re-brand's SSID prefix
# and the README documents the whitelabel file value; scrubbing merged
# records would rewrite history. The risk this gutter exists for is a
# brand-gated CODE path, so docs/, CHANGELOG.md and README.md are exempt by
# name and everything else still fails. The uhttpd.<brand> section check
# below stays tree-wide, prose included.
#
# The name is never spelled out in THIS file either: the scan pattern is a
# bracket expression that matches the literal without the file bytes forming it,
# so the gutter can scan the whole tree (itself included) without tripping over
# its own pattern. The negative control below proves the pattern still matches
# a real occurrence.
#
# Usage: bash tests/packaging/rebrand-literal-gutter_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# The literal under ban. `net4[s]ats` matches the nine-character name; the file
# bytes "net4[s]ats" do not match a scan for it. Built here so the pattern is
# defined exactly once.
BANNED='net4[s]ats'

# The banned SECTION name, same trick: the string `uhttpd.<name>` is the shape
# the purged legacy writer created.
BANNED_SECTION="uhttpd\\.${BANNED}"

# scan_tree: every occurrence of $1 in the TRACKED tree, minus the paths where
# the re-brand is DISCUSSED rather than shipped. The ban's object is the
# shipped/config gutter (the #649 purge class); decision records, the
# changelog, the README branding paragraph and the SSID contract checker
# legitimately spell the whitelabel's name — CONTRIBUTING itself does. git grep
# when this is a git work tree; a recursive grep otherwise, so the gutter is
# not silently inert outside a repo.
DISCUSSION_ALLOWLIST=(
    'docs'
    'CHANGELOG.md'
    'README.md'
    'CONTRIBUTING.md'
    'tests/contract/check-ssid-format.sh'
    # The SSID matcher's pinned acceptance table: the whitelabel fixtures
    # are the contract's own data, consumed by brands_test.go (which keeps
    # the literal out of src/) and check-ssid-naming.sh. Same legitimate
    # home as the format checker above it.
    'tests/contract/ssid-naming-fixtures.txt'
)
scan_tree() { # scan_tree <extended-regex>
    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        local excludes=()
        local path
        for path in "${DISCUSSION_ALLOWLIST[@]}"; do
            excludes+=(":!$path")
        done
        git grep -I -i -E -- "$1" -- . "${excludes[@]}" 2>/dev/null
    else
        grep -rI -i -E -- "$1" . 2>/dev/null | grep -v '^\./\.git/' \
            | grep -vE '^(\./)?(docs/|CHANGELOG\.md:|README\.md:|CONTRIBUTING\.md:|tests/contract/check-ssid-format\.sh:|tests/contract/ssid-naming-fixtures\.txt:)' || true
    fi
}

echo "== the re-brand's name appears nowhere in the tracked tree"
hits="$(scan_tree "$BANNED" | grep -v -E "^(docs/|CHANGELOG\.md:|README\.md:)" || true)"
if [ -z "$hits" ]; then
    ok "0 hits for the banned literal in $(git rev-parse --is-inside-work-tree >/dev/null 2>&1 && printf 'the tracked tree' || printf 'the tree')"
else
    bad "the banned literal is back ($(printf '%s\n' "$hits" | grep -c . ) hit(s)):"
    printf '%s\n' "$hits" | sed 's/^/       /'
fi

# The section shape the legacy second :8090 writer created must not exist
# either — including in the uci-defaults that install it.
echo
echo "== no uhttpd section named after the re-brand exists in the tree"
sec_hits="$(scan_tree "$BANNED_SECTION" || true)"
if [ -z "$sec_hits" ]; then
    ok "0 hits for a uhttpd section named after the banned literal"
else
    bad "a uhttpd section named after the banned literal is back ($(printf '%s\n' "$sec_hits" | grep -c . ) hit(s)):"
    printf '%s\n' "$sec_hits" | sed 's/^/       /'
fi

# ---------------------------------------------------------------- controls
# A gutter that cannot see a planted occurrence is worthless. Plant one, in a
# file, and require the SAME pattern to find it; then require a near-miss (the
# name minus its last character) to stay invisible.
echo
echo "== negative control: the scan pattern sees a planted occurrence"
planted="$TMP/planted.txt"
printf 'uhttpd.%s%s=uhttpd\n' "$(printf '%s' "$BANNED" | tr -d '[]')" '' > "$planted"
if grep -i -E -- "$BANNED" "$planted" >/dev/null 2>&1; then
    ok "the pattern matches a planted occurrence (the gutter is not inert)"
else
    bad "the pattern does NOT match a planted occurrence — the gutter cannot fail, so it proves nothing"
fi
near_miss="$TMP/near-miss.txt"
printf 'uhttpd.net4sat=uhttpd\n' > "$near_miss"
if grep -i -E -- "$BANNED" "$near_miss" >/dev/null 2>&1; then
    bad "the pattern matches a near-miss (a different brand name) — it is too loose to be a gutter"
else
    ok "the pattern does not match a near-miss brand name (it is anchored to the banned literal)"
fi
if printf '%s\n' "$hits" | grep -qF 'planted.txt'; then
    bad "scan_tree reported the control file as a tracked hit — the tree scan is not scoped to the tree"
else
    ok "scan_tree ignores the control file (it is outside the tracked tree)"
fi

# ------------------------------------------- the uci-defaults' section vocabulary
# Structural half of the same invariant: the section names the shipped
# uci-defaults reference must be the ones the install contract knows about.
# A new name here is how a second admin writer gets introduced.
echo
echo "== the shipped uci-defaults reference only known uhttpd sections"
KNOWN='main|portal|trusted|admin|crt|key|cert'
foreign="$(grep -rhoE 'uhttpd\.[a-zA-Z][a-zA-Z0-9_]*' packaging/files/etc/uci-defaults/ 2>/dev/null |
           sed 's/^uhttpd\.//' | sort -u | grep -v -E "^($KNOWN)$" || true)"
if [ -z "$foreign" ]; then
    ok "every uhttpd.<name> in packaging/files/etc/uci-defaults/ is a known section (or a cert/key path)"
else
    bad "unknown uhttpd section name(s) in the uci-defaults: $(printf '%s' "$foreign" | tr '\n' ' ')"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
