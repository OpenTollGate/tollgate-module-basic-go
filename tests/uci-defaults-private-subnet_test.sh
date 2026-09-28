#!/usr/bin/env bash
# Offline test for collision-aware management-subnet selection in
# setup_private_network() (99-tollgate-setup) — no router, no network.
#
# The real bug this guards: the private network's /24 was derived
# deterministically as "the LAN's /24 with the third octet stepped by one"
# (192.168.1.0/24 -> 192.168.2.0/24) with no look at what the router is
# attached to upstream. When the WAN side is itself a private LAN in that same
# /24 — a hotel, a site uplink, another router's LAN — the management subnet
# and the uplink subnet are the same network, and the private bridge's default
# route points at an address the router also owns: traffic to the upstream
# client range is routed back into the router instead of out of the WAN.
# Nothing in the tree detected it (IPAddressRandomized in the config manager is
# only ever logged, and identity.DeriveIPv4's CGNAT space is not wired into
# network setup), so every collision needed a hand fix on the device.
#
# The fix: keep the LAN-adjacent /24 when it is safe (operators expect it, and
# the DNS/DHCP defaults assume it) and fall back to a random, non-overlapping
# /24 from 10/8 when it is not. The upstream side has to be read from both
# places an address can live: UCI (static WAN) and the kernel (DHCP/STA/FIPS
# uplinks have no UCI address — the field failure was a DHCP upstream).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"
export GATEWAY_NAME="TestGate"
export RANDOM_SUFFIX="abcd"
export FAKE_IP_UPSTREAM="$TMP/ip.upstream"
export HEXDUMP_QUEUE_FILE="$TMP/hexdump.queue"
: > "$FAKE_IP_UPSTREAM"
: > "$HEXDUMP_QUEUE_FILE"

cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
# Minimal uci shim; a list option is one "key=value" line per entry.
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
case "$cmd" in
    get)
        vals="$(grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-)"
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    set)
        key="${1%%=*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list) printf '%s\n' "$1" >> "$state" ;;
    add) printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state" ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    commit|revert|show|export) : ;;
    *) : ;;
esac
exit 0
SHIM

cat > "$TMP/bin/ip" <<'SHIM'
#!/usr/bin/env bash
# Fake `ip -4 -o addr show`: the host's own interface list is never consulted,
# so the test cannot depend on (or leak) the machine it runs on.
lan_ip="$(grep -F -- 'network.lan.ipaddr=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1)"
[ -n "$lan_ip" ] || lan_ip="192.168.1.1"
printf '1: lo    inet 127.0.0.1/8 scope host lo\n'
printf '2: br-lan    inet %s/24 brd 192.168.1.255 scope global br-lan\n' "$lan_ip"
[ -s "$FAKE_IP_UPSTREAM" ] && cat "$FAKE_IP_UPSTREAM"
exit 0
SHIM

cat > "$TMP/bin/hexdump" <<'SHIM'
#!/usr/bin/env bash
# Fake hexdump for the 1-byte "%02X" form the random octets use: pops one hex
# byte from $HEXDUMP_QUEUE_FILE, so a scenario can dictate the draw (and the
# retry after a colliding draw) deterministically.
f="${HEXDUMP_QUEUE_FILE:-}"
if [ -n "$f" ] && [ -s "$f" ]; then
    val="$(head -n1 "$f")"
    tail -n +2 "$f" > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f"
    printf '%s\n' "$val"
    exit 0
fi
printf '%s\n' "${HEXDUMP_FIXED:-5a}"
SHIM

chmod +x "$TMP/bin/uci" "$TMP/bin/ip" "$TMP/bin/hexdump"
PATH="$TMP/bin:$PATH"
export PATH

TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1 || true

lan() { # lan <ip> <netmask>
    : > "$UCI_STATE"
    printf 'network.lan.ipaddr=%s\n' "$1" >> "$UCI_STATE"
    printf 'network.lan.netmask=%s\n' "$2" >> "$UCI_STATE"
    printf 'wireless.private_radio0.ssid=test-private\n' >> "$UCI_STATE"
    printf 'wireless.private_radio0.key=Alpha-Bravo-Charlie-11\n' >> "$UCI_STATE"
}
wan_static() { # wan_static <ip> <netmask>
    printf 'network.wan.ipaddr=%s\n' "$1" >> "$UCI_STATE"
    printf 'network.wan.netmask=%s\n' "$2" >> "$UCI_STATE"
}
upstream() { printf '%s\n' "$1" > "$FAKE_IP_UPSTREAM"; }   # one `ip -o addr` line
no_upstream() { : > "$FAKE_IP_UPSTREAM"; }
draw() { printf '%s\n' "$@" > "$HEXDUMP_QUEUE_FILE"; }     # hex bytes, in order
pick() {
    : > "$LOGFILE"
    R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
    sed -n 's/^network\.private\.ipaddr=//p' "$UCI_STATE" | tail -n1
}
prefix() { echo "$1" | cut -d. -f1-3; }
shape_ok() { echo "$1" | grep -qE '^([0-9]{1,3}\.){3}1$'; }

echo "== the LAN-adjacent /24 is kept when the upstream side does not collide"
lan 192.168.1.1 255.255.255.0
upstream '3: eth1    inet 10.55.0.17/16 brd 10.55.255.255 scope global eth1'
draw 5a 5a
ip_out=$(pick)
[ "$ip_out" = 192.168.2.1 ] \
    && ok "non-colliding upstream (10.55.0.0/16): deterministic 192.168.2.1 kept" \
    || bad "non-colliding upstream: expected 192.168.2.1, got '${ip_out}'"

echo "== a colliding upstream (DHCP address) forces a random non-overlapping /24"
lan 192.168.1.1 255.255.255.0
upstream '3: eth1    inet 192.168.2.17/24 brd 192.168.2.255 scope global eth1'
draw 5a 5a
ip_out=$(pick)
shape_ok "$ip_out" || bad "colliding upstream: '${ip_out}' is not a x.y.z.1 address"
[ "$(prefix "$ip_out")" != "192.168.2" ] \
    && ok "upstream 192.168.2.0/24 on the WAN: management /24 moved off 192.168.2.0/24" \
    || bad "upstream collision ignored: management /24 is still 192.168.2.0/24"
[ "$ip_out" = "10.90.90.1" ] \
    && ok "the drawn /24 is the one /dev/urandom produced (10.90.90.1)" \
    || bad "expected the drawn 10.90.90.1, got '${ip_out}'"
grep -q 'overlaps an upstream-side network' "$LOGFILE" \
    && ok "the fallback is recorded in the setup log" \
    || bad "no log line recording the subnet collision"

echo "== a colliding static WAN address (UCI, no kernel address) does the same"
lan 192.168.1.1 255.255.255.0
no_upstream
wan_static 192.168.2.2 255.255.255.0
draw 0a 0b
ip_out=$(pick)
[ "$ip_out" = "10.10.11.1" ] \
    && ok "static WAN in the candidate /24: drew 10.10.11.1" \
    || bad "static WAN collision: expected 10.10.11.1, got '${ip_out}'"

echo "== a draw that lands on another occupied /24 is retried"
lan 192.168.1.1 255.255.255.0
printf '3: eth1    inet 192.168.2.17/24 brd 192.168.2.255 scope global eth1\n4: fips0    inet 10.9.9.9/24 brd 10.9.9.255 scope global fips0\n' > "$FAKE_IP_UPSTREAM"
draw 09 09 14 1e
ip_out=$(pick)
[ "$ip_out" = "10.20.30.1" ] \
    && ok "colliding draw 10.9.9.0/24 skipped, next draw 10.20.30.1 used" \
    || bad "expected the retry to land on 10.20.30.1, got '${ip_out}'"

echo "== a LAN mask wider than /24 also triggers the fallback"
lan 192.168.0.1 255.255.0.0
no_upstream
draw 5a 5a
ip_out=$(pick)
[ "$ip_out" = "10.90.90.1" ] \
    && ok "LAN 192.168.0.0/16: the adjacent 192.168.1.0/24 sits inside the LAN, drew 10.90.90.1" \
    || bad "LAN /16 overlap: expected 10.90.90.1, got '${ip_out}'"

echo "== no upstream information at all keeps the deterministic behaviour"
lan 192.168.1.1 255.255.255.0
no_upstream
draw 5a 5a
ip_out=$(pick)
[ "$ip_out" = 192.168.2.1 ] \
    && ok "no upstream addresses: deterministic 192.168.2.1 kept" \
    || bad "no upstream information: expected 192.168.2.1, got '${ip_out}'"

echo "== .255 third octet still steps down (no self-collision at the top of the /16)"
lan 192.168.255.1 255.255.255.0
upstream '3: eth1    inet 10.1.1.1/24 brd 10.1.1.255 scope global eth1'
draw 5a 5a
ip_out=$(pick)
[ "$ip_out" = "192.168.254.1" ] \
    && ok "LAN 192.168.255.0/24: candidate 192.168.254.1 kept" \
    || bad "expected 192.168.254.1, got '${ip_out}'"

echo "== the fallback is a draw, not a constant"
lan 192.168.1.1 255.255.255.0
upstream '3: eth1    inet 192.168.2.17/24 brd 192.168.2.255 scope global eth1'
draw 01 02
first=$(pick)
draw 0a 0b
second=$(pick)
[ "$first" = "10.1.2.1" ] && ok "first draw 10.1.2.1" || bad "first draw: expected 10.1.2.1, got '${first}'"
[ "$second" = "10.10.11.1" ] && ok "second draw 10.10.11.1" || bad "second draw: expected 10.10.11.1, got '${second}'"
[ "$first" != "$second" ] \
    && ok "two runs over the same collision pick different /24s (randomised, not fixed)" \
    || bad "the fallback /24 is fixed ('${first}' twice)"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
