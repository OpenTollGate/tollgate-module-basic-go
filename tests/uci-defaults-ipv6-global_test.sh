#!/usr/bin/env bash
# Offline tests for the GLOBAL IPv6 disable in 99-tollgate-setup
# (setup_disable_ipv6 — issues #148 and #783).
#
# #148's LAN half (dhcp.lan.ra/dhcpv6 disabled, network.lan.ip6assign=0)
# shipped long ago and is regression-pinned here. #783 bench-verified on
# rc1 that a pre-auth client still reaches the internet over IPv6 when the
# WAN offers it — so the disable is now global: network.lan.ipv6=0,
# network.wan.ipv6=0 (when a wan section exists), and the stock wan6
# section disabled (when present). A missing wan/wan6 section is adoption,
# not an error.
#
# No router, SDK or network needed: the function is extracted from the
# setup script by function boundary (the same style as the NTP/band
# tests) and runs against a fake uci.
#
# Usage: bash tests/uci-defaults-ipv6-global_test.sh
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

# ---- extract setup_disable_ipv6 by function boundary, rewriting the log call
awk '/^setup_disable_ipv6\(\) \{/,/^\}/' "$SCRIPT" > "$TMP/v6_fragment.sh"
grep -q "network.lan.ipv6='0'" "$TMP/v6_fragment.sh" \
    || { bad "extraction failed — setup_disable_ipv6 not found or pre-#783 shape in $SCRIPT"; exit 1; }
# The extraction carries only the function definition; supply the real
# script's `log` helper as a stub and invoke the function.
printf '\nlog() { echo "log: $*"; }\nsetup_disable_ipv6\n' >> "$TMP/v6_fragment.sh"

mkdir -p "$TMP/bin"

# ---- fake uci: flat-file state, the subset the function uses
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
    get)
        key="${1:-}"
        if [ -f "$STATE/$key" ]; then cat "$STATE/$key"; exit 0; fi
        for f in "$STATE/$key".*; do [ -e "$f" ] && exit 0; done
        exit 1
        ;;
    set)
        arg="${1:-}"
        key="${arg%%=*}"
        val="${arg#*=}"
        mkdir -p "$STATE/$(dirname "$key")"
        printf '%s' "$val" > "$STATE/$key"
        ;;
    commit)
        # The postinst block commits dhcp/network; flat-file state needs no
        # flush, but the command must succeed so `|| true` paths stay honest.
        exit 0
        ;;
    *)
        echo "uci-shim: unsupported command: $cmd" >&2
        exit 99
        ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

run_fragment() {  # run_fragment <state-dir>: runs the extracted function
    UCI_STATE="$1" PATH="$TMP/bin:$PATH" sh "$TMP/v6_fragment.sh" 2>"$1/stderr.log"
}

state_has() { [ -f "$1/$2" ] && [ "$(cat "$1/$2")" = "$3" ]; }

# ---- case 1: stock topology (lan + wan + wan6 all present)
S="$TMP/stock"; mkdir -p "$S"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$S/$(dirname "$k")"; echo "'placeholder'" > "$S/$k"
done
run_fragment "$S"
state_has "$S" dhcp.lan.ra disabled           && ok "stock: dhcp.lan.ra=disabled (#148 half kept)" || bad "stock: dhcp.lan.ra not disabled"
state_has "$S" dhcp.lan.dhcpv6 disabled        && ok "stock: dhcp.lan.dhcpv6=disabled (#148 half kept)" || bad "stock: dhcp.lan.dhcpv6 not disabled"
state_has "$S" network.lan.ip6assign 0         && ok "stock: network.lan.ip6assign=0 (#148 half kept)" || bad "stock: ip6assign not 0"
state_has "$S" network.lan.ipv6 0              && ok "stock: network.lan.ipv6=0 (global, #783)" || bad "stock: network.lan.ipv6 not 0"
state_has "$S" network.wan.ipv6 0              && ok "stock: network.wan.ipv6=0 (global, #783)" || bad "stock: network.wan.ipv6 not 0"
state_has "$S" network.wan6.disabled 1         && ok "stock: network.wan6.disabled=1 (#783)" || bad "stock: wan6 not disabled"

# ---- case 2: idempotence — a second run changes nothing (state already converged)
before="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
run_fragment "$S"
after="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
[ "$before" = "$after" ] && ok "idempotent: second run is a no-op" || bad "idempotent: second run changed state"

# ---- case 3: wan6-less topology (some images/modem setups) — adoption, no error
S3="$TMP/no-wan6"; mkdir -p "$S3"
for k in network.lan.ip6assign network.wan.proto; do
    mkdir -p "$S3/$(dirname "$k")"; echo "'placeholder'" > "$S3/$k"
done
run_fragment "$S3"; rc=$?
[ $rc -eq 0 ] && ok "no-wan6: function exits 0 (missing section is adoption)" || bad "no-wan6: function exited $rc"
[ ! -f "$S3/network.wan6.disabled" ] && ok "no-wan6: no wan6 section invented" || bad "no-wan6: wan6.disabled written for a section that does not exist"
state_has "$S3" network.wan.ipv6 0 && ok "no-wan6: wan.ipv6=0 still set" || bad "no-wan6: wan.ipv6 not set"

# ---- case 4: previously-ENABLED v6 (upgrade case) — the policy re-asserts
S4="$TMP/pre-enabled"; mkdir -p "$S4"
for k in network.lan.ipv6 network.wan.ipv6; do
    mkdir -p "$S4/$(dirname "$k")"; echo "1" > "$S4/$k"
done
mkdir -p "$S4/dhcp.lan"; echo "'enabled'" > "$S4/dhcp.lan.ra"
run_fragment "$S4"
state_has "$S4" network.lan.ipv6 0 && state_has "$S4" network.wan.ipv6 0 && state_has "$S4" dhcp.lan.ra disabled \
    && ok "upgrade: enabled v6 is re-disabled (policy, not adoption)" || bad "upgrade: pre-enabled v6 survived"

# ---- case 5: the INSTALL-time block in packaging/postinst converges too ----
# The upgrade path drove a second copy of the policy into postinst (applied
# before the network restart, because the marker-driven setup driver's
# verify/repair pass does not re-assert it). That copy is extracted here by
# its boundary comments and must produce the same five axes.
POSTINST="packaging/postinst"
awk '/^# IPv6 is disabled at INSTALL time/,/^echo "IPv6 disabled globally at install time/' \
    "$POSTINST" > "$TMP/postinst_fragment.sh"
grep -q "network.lan.ipv6='0'" "$TMP/postinst_fragment.sh" \
    || { bad "extraction failed — install-time IPv6 block not found or pre-#783 shape in $POSTINST"; }

S5="$TMP/postinst-stock"; mkdir -p "$S5"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$S5/$(dirname "$k")"; echo "'placeholder'" > "$S5/$k"
done
UCI_STATE="$S5" PATH="$TMP/bin:$PATH" sh "$TMP/postinst_fragment.sh" >/dev/null 2>"$S5/stderr.log"; rc=$?
[ $rc -eq 0 ] && ok "postinst: install-time block exits 0" || bad "postinst: install-time block exited $rc"
state_has "$S5" dhcp.lan.ra disabled      && ok "postinst: dhcp.lan.ra=disabled" || bad "postinst: dhcp.lan.ra not disabled"
state_has "$S5" dhcp.lan.dhcpv6 disabled   && ok "postinst: dhcp.lan.dhcpv6=disabled" || bad "postinst: dhcp.lan.dhcpv6 not disabled"
state_has "$S5" network.lan.ip6assign 0    && ok "postinst: network.lan.ip6assign=0" || bad "postinst: ip6assign not 0"
state_has "$S5" network.lan.ipv6 0         && ok "postinst: network.lan.ipv6=0" || bad "postinst: network.lan.ipv6 not 0"
state_has "$S5" network.wan.ipv6 0         && ok "postinst: network.wan.ipv6=0" || bad "postinst: network.wan.ipv6 not 0"
state_has "$S5" network.wan6.disabled 1    && ok "postinst: network.wan6.disabled=1" || bad "postinst: wan6 not disabled"

# ---- case 6: the two policy copies cannot drift apart ----------------------
# One rule, two carriers (setup function + postinst block) — the #791/#796
# one-declaration discipline, fenced: the sorted set of `uci set` keys each
# copy writes must be identical, or a future axis lands in one and not the
# other and the install-time posture silently goes stale.
setup_keys="$(grep -o "uci set [^=]*=" "$TMP/v6_fragment.sh" | awk '{print $3}' | sort -u)"
postinst_keys="$(grep -o "uci set [^=]*=" "$TMP/postinst_fragment.sh" | awk '{print $3}' | sort -u)"
[ -n "$setup_keys" ] && [ "$setup_keys" = "$postinst_keys" ] \
    && ok "drift fence: setup and postinst copies write the same axes" \
    || bad "drift fence: policy copies diverge — setup writes [$setup_keys], postinst writes [$postinst_keys]"

# ---- summary
echo
echo "passed $PASS, failed $FAIL"
[ $FAIL -eq 0 ]
