#!/usr/bin/env bash
# Offline tests for the pre-auth DNS entries of issue #749
# (packaging/files/etc/uci-defaults/99-tollgate-setup:
# assert_nodogsplash_allow_entries).
#
# The real bug this guards: the shipped list allowed tcp/53 (the nodogsplash
# feed default) but not udp/53 — and stub resolvers (glibc/musl, Android,
# browsers) query UDP FIRST. The ndsRTR chain dropped the UDP query, the
# client burned a ~5s timeout per attempt before any TCP fallback, and
# hostname resolution from a captive client was dead in practice (the portal
# page itself is usually reached by name). Measured on the local-lab campaign
# behind PRTA PR #101: router-side nslookup instant, client-side timeout,
# IP-literal curl fine.
#
# The fix asserts BOTH 'allow tcp port 53' and 'allow udp port 53' with
# protocol-aware guards (uci_list_has_entry): a port-only guard
# (uci_list_has_port) matches 53 on either protocol, would see the feed's
# tcp/53 default, and skip the udp add — the exact trap this matrix pins.
# tcp/53 is asserted too because this writer's contract is that a stale or
# absent list is REPAIRED on every setup path (a list that lost tcp/53 would
# break TCP-fallback resolvers the same way).
#
# No router, SDK or network needed: assert_nodogsplash_allow_entries and its
# two guard helpers are extracted from the shipped script by function
# boundary (the same style as tests/uci-defaults-ntp-preauth_test.sh) and run
# against a fake uci over a flat-file state.
#
# Usage: bash tests/uci-defaults-dns-preauth_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---- extract the writer + both guards by function boundary, redirecting
# the one absolute path the writer touches (the config file's existence
# check) into the sandbox so the test never touches the host's /etc.
{
    awk '/^uci_list_has_port\(\) \{/,/^\}/' "$SCRIPT"
    awk '/^uci_list_has_entry\(\) \{/,/^\}/' "$SCRIPT"
    awk '/^assert_nodogsplash_allow_entries\(\) \{/,/^\}/' "$SCRIPT" \
        | sed "s|/etc/config/nodogsplash|$TMP/config/nodogsplash|g"
} > "$TMP/dns_fragment.sh"
mkdir -p "$TMP/config"
mkdir -p "$TMP/bin"
grep -q 'allow udp port 53' "$TMP/dns_fragment.sh" \
    || { bad "extraction failed — the DNS entries not found in $SCRIPT"; exit 1; }

# ---- fake uci: flat-file state, the subset the writer uses (same shape as
# the same-version allowlist test's shim).
export UCI_STATE="$TMP/uci.state"
export UCI_LIST_SEP="${UCI_LIST_SEP:-}"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
sep="${UCI_LIST_SEP:-}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
case "$cmd" in
    get)
        vals="$(values "$1")"
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

KEY="nodogsplash.@nodogsplash[0].users_to_router"
seed() { # seed <list entry>... — one argument per entry
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    local e
    for e in "$@"; do printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"; done
}
count_entry() { grep -F -c "$KEY=$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has_entry() { grep -F -q "$KEY=$1" "$UCI_STATE"; }

run_writer() { . "$TMP/dns_fragment.sh" && assert_nodogsplash_allow_entries; }

# ------------------------------------------------------- 1. the #749 matrix
echo "== a feed-default list (tcp/53, no udp/53) gains udp/53, tcp/53 not duplicated"
seed 'allow tcp port 22' 'allow tcp port 53' 'allow tcp port 2121' 'allow tcp port 2050' 'allow tcp port 2051'
run_writer
if [ "$(count_entry 'allow udp port 53')" = "1" ]; then
    ok "udp/53 added exactly once beside the feed's tcp/53 (#749 fixed)"
else
    bad "udp/53 count = $(count_entry 'allow udp port 53'), want 1"
fi
if [ "$(count_entry 'allow tcp port 53')" = "1" ]; then
    ok "existing tcp/53 not duplicated"
else
    bad "tcp/53 duplicated: count = $(count_entry 'allow tcp port 53')"
fi

echo "== a stale list with NO 53 entry at all is repaired on both protocols"
seed 'allow tcp port 2121'
run_writer
has_entry 'allow tcp port 53' && ok "tcp/53 asserted on a list that lost it" \
    || bad "tcp/53 missing after repair"
has_entry 'allow udp port 53' && ok "udp/53 asserted on a list that never had it" \
    || bad "udp/53 missing after repair"

echo "== udp/53 present but tcp/53 missing: the guard is protocol-aware"
seed 'allow udp port 53' 'allow tcp port 2121'
run_writer
has_entry 'allow tcp port 53' \
    && ok "tcp/53 added although udp/53 occupies the port (port-only guard would have skipped it)" \
    || bad "tcp/53 not added — the guard is not protocol-aware"
[ "$(count_entry 'allow udp port 53')" = "1" ] \
    && ok "udp/53 not duplicated when already present" \
    || bad "udp/53 duplicated: $(count_entry 'allow udp port 53')"

echo "== idempotence: a second run of the writer adds nothing"
before_tcp=$(count_entry 'allow tcp port 53'); before_udp=$(count_entry 'allow udp port 53')
run_writer
[ "$(count_entry 'allow tcp port 53')" = "$before_tcp" ] && [ "$(count_entry 'allow udp port 53')" = "$before_udp" ] \
    && ok "re-run adds no duplicate DNS entries" \
    || bad "re-run changed the DNS entries (tcp $(count_entry 'allow tcp port 53')/$before_tcp, udp $(count_entry 'allow udp port 53')/$before_udp)"

# --------------------------------------- 2. the quoted single-line rendering
echo "== uci's quoted single-line list rendering is handled by the guard"
# uci get may render the whole list as one quoted line: 'a' 'b' 'c'. The
# fake joins with the separator it is given; emulate the quoting by seeding
# the joined form directly and having the writer read it back through the
# same state file.
: > "$UCI_STATE"
printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
printf "%s='%s' '%s' '%s'\n" "$KEY" \
    'allow tcp port 22' 'allow tcp port 53' 'allow tcp port 2121' >> "$UCI_STATE"
run_writer
[ "$(count_entry 'allow udp port 53')" = "1" ] \
    && ok "udp/53 added when the list renders as one quoted line" \
    || bad "quoted single-line rendering defeated the guard (udp count $(count_entry 'allow udp port 53'))"

echo
echo "== uci-defaults-dns-preauth: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
