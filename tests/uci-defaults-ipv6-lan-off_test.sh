#!/usr/bin/env bash
# Offline tests for the LAN IPv6-off writer of #148/#160 — the captive-portal
# bypass prevention whose dynamic behavior is pinned by
# tests/vm-campaign/v6_captive_regression.py (RA silence, no SLAAC, no escape;
# see issue #783 and the rel-769 lane forensics for why state != behavior).
#
# This suite pins the STATE half: setup_disable_ipv6_lan must write all three
# keys — dhcp.lan.ra='disabled' AND dhcp.lan.dhcpv6='disabled' AND
# network.lan.ip6assign='0'. A writer that only kills one of the two ways a
# client obtains v6 (RA vs DHCPv6), or only un-assigns the prefix while
# leaving a daemon advertising, reopens the bypass. The existing
# uci-defaults-setup-marker-order test pins ra=disabled on the full-setup
# paths; this file pins all three keys against the writer itself, so a
# refactor of either cannot silently drop the others.
#
# No router, SDK or network needed: the writer is extracted from the shipped
# script by function boundary and run against a fake uci over a flat-file
# state (the same style as tests/uci-defaults-dns-preauth_test.sh).
#
# Usage: bash tests/uci-defaults-ipv6-lan-off_test.sh
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

# ---- extract the writer by function boundary; stub its one external
# dependency (log) so the fragment is standalone.
awk '/^setup_disable_ipv6_lan\(\) \{/,/^\}/' "$SCRIPT" > "$TMP/v6_fragment.sh"
grep -q "setup_disable_ipv6_lan" "$TMP/v6_fragment.sh" \
    || { bad "extraction failed — setup_disable_ipv6_lan not found in $SCRIPT"; exit 1; }
printf 'log() { printf "%%s\\n" "LOG: $*" >> "$UCI_STATE.log"; }\n' > "$TMP/logstub.sh"

# ---- fake uci: records every `set` into a flat file; the subset the writer
# uses. get is provided so future guards (idempotence checks) fail loudly
# rather than silently no-op.
export UCI_STATE="$TMP/uci.state"
mkdir -p "$TMP/bin"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
[ "${1:-}" = "-q" ] && shift
cmd="${1:-}"
shift || true
case "$cmd" in
    set)   printf 'set %s\n' "$*" >> "$state" ;;
    get)   grep -F -- "get $*" "$state" >/dev/null 2>&1 && exit 0
           echo "uci: Entry not found" >&2; exit 1 ;;
    *)     : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

run_writer() {
    : > "$UCI_STATE"
    { cat "$TMP/logstub.sh"; cat "$TMP/v6_fragment.sh"
      echo; echo 'setup_disable_ipv6_lan'; } > "$TMP/run.sh"
    sh "$TMP/run.sh"
}

# ------------------------------------------------ 1. the full three-key write
echo "== the writer disables RA, DHCPv6 and the LAN prefix assignment"
run_writer
sets="$(grep -c '^set ' "$UCI_STATE" | tr -d ' ')"
[ "$sets" = "3" ] && ok "exactly three uci set calls (got $sets)" \
                 || bad "expected 3 uci set calls, got $sets"
grep -qF "set dhcp.lan.ra=disabled" "$UCI_STATE" \
    && ok "dhcp.lan.ra='disabled' written" || bad "dhcp.lan.ra='disabled' missing"
grep -qF "set dhcp.lan.dhcpv6=disabled" "$UCI_STATE" \
    && ok "dhcp.lan.dhcpv6='disabled' written" || bad "dhcp.lan.dhcpv6='disabled' missing"
grep -qF "set network.lan.ip6assign=0" "$UCI_STATE" \
    && ok "network.lan.ip6assign='0' written" || bad "network.lan.ip6assign='0' missing"

# ------------------------------------------------ 2. the audit log line
grep -q "IPv6 disabled on LAN" "$UCI_STATE.log" 2>/dev/null \
    && ok "audit log line emitted" || bad "no audit log (installer auditability)"

# ------------------------------------------------ 3. it is still called
echo "== the writer is still wired into the shipped script's setup flow"
grep -qE "^[[:space:]]*setup_disable_ipv6_lan([[:space:]]|$)" "$SCRIPT" \
    && ok "setup_disable_ipv6_lan is invoked by $SCRIPT" \
    || bad "setup_disable_ipv6_lan defined but never called — the state would never apply"

echo
echo "== uci-defaults-ipv6-lan-off: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
