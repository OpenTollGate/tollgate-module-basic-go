#!/usr/bin/env bash
# Offline test for setup_mgmt_bridge() in
# packaging/files/etc/uci-defaults/99-tollgate-setup — no router, no network.
#
# What this pins. The wired LAN ports used to sit on the CAPTIVE bridge, so the
# operator administering the router from an Ethernet cable was treated as a
# guest: both administration guards are scoped to the captive bridge by name
# (31-admin-board-not-guest-reachable.nft, 32-luci-not-guest-reachable.nft), so
# a cable reached neither the board (:8090/:8443) nor LuCI (:8080/:443) before
# paying — and the port shared a layer-2 domain with the strangers on the open
# SSID, the one exposure this repo measured as unclosable by bridge-port
# isolation. The writer MOVES the ports onto a bridge of their own, br-mgmt,
# which nodogsplash does not gate and which reaches the administration surfaces
# only (see 33-mgmt-bridge-scope.nft).
#
# Three properties are load-bearing and each has assertions here:
#   1. the move is a MOVE — the captive bridge's port list is cleared, not
#      copied (a copy would leave the port on both bridges);
#   2. it is idempotent AND convergent — a second run changes nothing, and a
#      `sysupgrade -n`/factory reset that puts the port back on the captive
#      bridge is repaired by the same-version install path, because the port
#      list is owned by the base image, not by this repository;
#   3. it FAILS LOUDLY AND CHANGES NOTHING when there is nothing to move (a
#      portless br-mgmt would report a management bridge with no way in), and
#      it gives the bridge no path off the router (its own zone, forward
#      REJECT, no forwarding to wan, no reuse of firewall.private_zone — that
#      zone forwards to the wan and would hand the cable free internet).
#
# How it checks: the shipped driver is copied into a temp dir (only its
# SETUP_FLAG and LOGFILE absolute paths are rewritten) and RUN against a fake
# `uci`, `apk`, `ip` and /etc/shadow — the harness
# tests/uci-defaults-private-subnet_test.sh and
# tests/packaging/guest-client-isolation_test.sh use — so the assertions are
# about the config the shipped code actually generates, not about a grep of the
# source. The last section mutates the writer out of a copy of the script and
# requires the assertions to FAIL against it, so the guard is proven to bite.
#
# Usage: bash tests/uci-defaults-mgmt-bridge_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ---------------------------------------------------------------- fake apk
FAKE_VERSION="0.6.0_alpha4-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, so a list option is one
# line per entry (which is how `uci get` renders a list). `del_list` is
# implemented properly — the trap the repo has already been bitten by is a shim
# that falls through to `*) :` and turns every assertion about a removal into a
# tautology.
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
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
    set)
        key="${1%%=*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# ----------------------------------------------------------------- fake ip
# `upstream_networks` reads the kernel for addresses that are not the LAN or the
# private bridge, so the host's own interfaces must never be consulted (the test
# would otherwise depend on, and leak, the machine it runs on).
export FAKE_IP_UPSTREAM="$TMP/ip.upstream"
: > "$FAKE_IP_UPSTREAM"
cat > "$TMP/bin/ip" <<'SHIM'
#!/bin/sh
lan_ip="$(grep -F -- 'network.lan.ipaddr=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1)"
[ -n "$lan_ip" ] || lan_ip="192.168.1.1"
case "$lan_ip" in
    */*) : ;;
    *) lan_ip="$lan_ip/24" ;;
esac
printf '1: lo    inet 127.0.0.1/8 scope host lo\n'
printf '2: br-lan    inet %s brd 192.168.1.255 scope global br-lan\n' "$lan_ip"
[ -s "$FAKE_IP_UPSTREAM" ] && cat "$FAKE_IP_UPSTREAM"
exit 0
SHIM
chmod +x "$TMP/bin/ip"

# ------------------------------------------------------------- fake hexdump
# The random-fallback path draws octets with hexdump; the queue makes a draw
# deterministic (and proves the fallback is a draw, not a constant).
export HEXDUMP_QUEUE_FILE="$TMP/hexdump.queue"
: > "$HEXDUMP_QUEUE_FILE"
cat > "$TMP/bin/hexdump" <<'SHIM'
#!/bin/sh
f="${HEXDUMP_QUEUE_FILE:-}"
if [ -n "$f" ] && [ -s "$f" ]; then
    val="$(head -n1 "$f")"
    tail -n +2 "$f" > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f"
    printf '%s\n' "$val"
    exit 0
fi
printf '%s\n' "${HEXDUMP_FIXED:-5a}"
SHIM
chmod +x "$TMP/bin/hexdump"

export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi

# The driver also runs the admin-credential gate (reads the live /etc/shadow and
# would invoke `passwd root` on an empty hash) and writes the :80 stub document
# into a docroot — pin all of them to fixtures so the test never touches the
# machine it runs on.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
export ROUTER_HOME_DIR="$TMP/router-home"

# ------------------------------------------------------------- the fixtures
# A stock-router seed: two radios, the stock guest APs, the captive bridge with
# its IMAGE-OWNED port list, and (optionally) the owner's private network —
# which by default takes the /24 next to the LAN's, and is therefore exactly the
# network the management bridge must not land on.
#   seed_stock <ports> [private-ipaddr]
seed_stock() {
    local ports="$1" private_ip="${2:-}" p
    : > "$UCI_STATE"
    {
        printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g'
        printf '%s\n' 'wireless.radio0.channel=1' 'wireless.radio0.disabled=0'
        printf '%s\n' 'wireless.radio1=wifi-device' 'wireless.radio1.band=5g'
        printf '%s\n' 'wireless.radio1.channel=36' 'wireless.radio1.disabled=0'
        printf '%s\n' 'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0'
        printf '%s\n' 'wireless.default_radio0.mode=ap' 'wireless.default_radio0.network=lan'
        printf '%s\n' 'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1'
        printf '%s\n' 'wireless.default_radio1.mode=ap' 'wireless.default_radio1.network=lan'
        printf '%s\n' 'network.lan=interface' 'network.lan.device=br-lan'
        printf '%s\n' 'network.lan.proto=static' 'network.lan.ipaddr=192.168.1.1/24'
        printf '%s\n' 'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'network.@device[0]=device' 'network.@device[0].name=br-lan'
        printf '%s\n' 'network.@device[0].type=bridge'
        for p in $ports; do
            printf 'network.@device[0].ports=%s\n' "$p"
        done
        printf '%s\n' 'network.wan=interface' 'network.wan.device=eth0' 'network.wan.proto=dhcp'
        printf '%s\n' 'system.@system[0]=system' 'system.@system[0].hostname=TollGate'
        printf '%s\n' 'dhcp.@dnsmasq[0]=dnsmasq'
        if [ -n "$private_ip" ]; then
            printf '%s\n' 'network.private=interface' 'network.private.proto=static'
            printf '%s\n' 'network.private.device=br-private'
            printf 'network.private.ipaddr=%s\n' "$private_ip"
            printf '%s\n' 'network.private.netmask=255.255.255.0'
            printf '%s\n' 'network.private_bridge=device' 'network.private_bridge.type=bridge'
            printf '%s\n' 'network.private_bridge.name=br-private'
            printf '%s\n' 'firewall.private_zone=zone' 'firewall.private_zone.name=private'
            printf '%s\n' 'firewall.private_zone.network=private'
            printf '%s\n' 'firewall.private_zone.input=ACCEPT'
            printf '%s\n' 'firewall.private_zone.output=ACCEPT'
            printf '%s\n' 'firewall.private_zone.forward=ACCEPT'
            printf '%s\n' 'firewall.private_forwarding=forwarding'
            printf '%s\n' 'firewall.private_forwarding.src=private'
            printf '%s\n' 'firewall.private_forwarding.dest=wan'
        fi
    } >> "$UCI_STATE"
}

# A stock router whose captive bridge declares NO ports and which has no
# management bridge yet: the one case the writer must refuse.
seed_portless() {
    : > "$UCI_STATE"
    {
        printf '%s\n' 'network.lan=interface' 'network.lan.device=br-lan'
        printf '%s\n' 'network.lan.proto=static' 'network.lan.ipaddr=192.168.1.1/24'
        printf '%s\n' 'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'network.@device[0]=device' 'network.@device[0].name=br-lan'
        printf '%s\n' 'network.@device[0].type=bridge'
    } >> "$UCI_STATE"
}

count() { grep -F -c "$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has()   { grep -F -q "$1" "$UCI_STATE"; }
line()  { grep -F -x "$1" "$UCI_STATE" 2>/dev/null; }
upstream() { printf '%s\n' "$1" > "$FAKE_IP_UPSTREAM"; }
no_upstream() { : > "$FAKE_IP_UPSTREAM"; }
draw() { printf '%s\n' "$@" > "$HEXDUMP_QUEUE_FILE"; }

run_same_version() { # the reinstall/upgrade path: flag already at SETUP_VERSION
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

ipaddr() { sed -n 's/^network\.mgmt\.ipaddr=//p' "$UCI_STATE" | tail -n1; }

# --------------------------------------------------------------- assertions
# Kept in functions so the RED controls at the bottom can run the exact same
# checks against deliberately broken copies of the script.
check_bridge_written() { # <label> <expected ipaddr>
    local label="$1" want_ip="$2" got
    local -a missing=()
    for key in 'network.mgmt=interface' 'network.mgmt.proto=static' \
               'network.mgmt.device=br-mgmt' 'network.mgmt.netmask=255.255.255.0' \
               'network.mgmt.ip6assign=0' 'network.mgmt_bridge=device' \
               'network.mgmt_bridge.type=bridge' 'network.mgmt_bridge.name=br-mgmt' \
               'dhcp.mgmt=dhcp' 'dhcp.mgmt.interface=mgmt' 'dhcp.mgmt.start=100' \
               'dhcp.mgmt.limit=150' 'dhcp.mgmt.leasetime=12h' \
               'dhcp.mgmt.ra=disabled' 'dhcp.mgmt.dhcpv6=disabled' \
               'firewall.mgmt_zone=zone' 'firewall.mgmt_zone.name=mgmt' \
               'firewall.mgmt_zone.network=mgmt' 'firewall.mgmt_zone.input=ACCEPT' \
               'firewall.mgmt_zone.output=ACCEPT' 'firewall.mgmt_zone.forward=REJECT'; do
        has "$key" || missing+=("$key")
    done
    if [ "${#missing[@]}" = 0 ]; then
        ok "$label: br-mgmt, dhcp.mgmt and firewall.mgmt_zone written in full"
    else
        bad "$label: missing ${#missing[@]} option(s): $(printf '%s ' "${missing[@]}")"
    fi
    got=$(ipaddr)
    [ "$got" = "$want_ip" ] && ok "$label: network.mgmt.ipaddr=$want_ip" \
                            || bad "$label: network.mgmt.ipaddr='$got' (want '$want_ip')"
}

check_ports_moved() { # <label> [<expected ports on br-mgmt (space separated)>]
    local label="$1" want="${2:-}" p n
    if line 'network.@device[0].ports=eth1' >/dev/null; then
        bad "$label: the captive bridge still lists its port — the move must CLEAR it, not copy it"
    else
        ok "$label: the captive bridge's port list is cleared"
    fi
    for p in $want; do
        n=$(count "network.mgmt_bridge.ports=$p")
        [ "$n" = 1 ] && ok "$label: br-mgmt lists port $p exactly once" \
                     || bad "$label: br-mgmt port $p present $n time(s) (want 1)"
    done
}

check_no_way_out() { # <label> — the "management, not a customer network" half
    local label="$1"
    if line 'firewall.mgmt_zone.forward=REJECT' >/dev/null; then
        ok "$label: the mgmt zone is forward REJECT (no way out to the wan)"
    else
        bad "$label: the mgmt zone is not forward REJECT — a wired client would be forwarded"
    fi
    local fwd
    fwd=$(grep -E '^firewall\.[a-z0-9_]*forwarding\.src=mgmt$' "$UCI_STATE" 2>/dev/null || true)
    if [ -z "$fwd" ]; then
        ok "$label: no forwarding section names the mgmt zone as a source"
    else
        bad "$label: a forwarding section forwards the mgmt zone ('$fwd')"
    fi
    if line 'firewall.private_zone.network=private' >/dev/null; then
        ok "$label: firewall.private_zone still owns only the private network (not reused)"
    else
        bad "$label: the mgmt zone reused or clobbered firewall.private_zone — its forward ACCEPT + wan forwarding would hand the cable free internet"
    fi
}

check_nothing_written() { # <label>
    local label="$1" stray
    stray=$(grep -E '^(network\.mgmt|network\.mgmt_bridge|dhcp\.mgmt|firewall\.mgmt_zone)=' "$UCI_STATE" 2>/dev/null || true)
    if [ -z "$stray" ]; then
        ok "$label: nothing was written"
    else
        bad "$label: the refused run still wrote: $(printf '%s ' $stray)"
    fi
}

# ------------------------------------------------- 1. the same-version path
echo "== the wired ports move to br-mgmt on the same-version (reinstall) path"
seed_stock 'eth1' '192.168.2.1'
no_upstream
draw 5a 5a
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
# The private network holds the LAN+1 /24 by default, so the management bridge
# must skip it: two bridges on one /24 is a broken router.
check_bridge_written "same-version install" "192.168.3.1"
check_ports_moved "same-version install" "eth1"
check_no_way_out "same-version install"
grep -q 'Wired LAN ports moved from br-lan to br-mgmt' "$LOGFILE" \
    && ok "the move is recorded in the setup log" \
    || bad "the setup log does not record the port move"
grep -q 'Management bridge br-mgmt: 192.168.3.1/24' "$LOGFILE" \
    && ok "the address decision is recorded in the setup log" \
    || bad "the setup log does not record the chosen address"

echo "== a second same-version run changes nothing (idempotent)"
run_same_version
check_bridge_written "second run" "192.168.3.1"
check_ports_moved "second run" "eth1"
[ "$(count 'network.mgmt_bridge=device')" = 1 ] \
    && ok "idempotent: network.mgmt_bridge declared once" \
    || bad "idempotent: network.mgmt_bridge declared $(count 'network.mgmt_bridge=device') time(s)"
[ "$(count 'firewall.mgmt_zone=zone')" = 1 ] \
    && ok "idempotent: firewall.mgmt_zone declared once" \
    || bad "idempotent: firewall.mgmt_zone declared $(count 'firewall.mgmt_zone=zone') time(s)"
grep -q 'Wired LAN ports already on br-mgmt' "$LOGFILE" \
    && ok "the converged run says so instead of moving anything again" \
    || bad "the converged run did not report the already-moved state"

echo "== a factory reset / sysupgrade -n that puts the port back is repaired"
# The port list is owned by the base image: after a reset the captive bridge
# lists the port again while br-mgmt still holds it. The writer must converge
# without duplicating the entry.
printf 'network.@device[0].ports=eth1\n' >> "$UCI_STATE"
run_same_version
check_ports_moved "post-reset reinstall" "eth1"
check_bridge_written "post-reset reinstall" "192.168.3.1"

echo "== a multi-port device moves every wired LAN port"
seed_stock 'lan1 lan2 lan3' '192.168.2.1'
no_upstream
run_same_version
for p in lan1 lan2 lan3; do
    n=$(count "network.mgmt_bridge.ports=$p")
    [ "$n" = 1 ] && ok "br-mgmt lists $p exactly once" || bad "br-mgmt port $p present $n time(s)"
done
if grep -qE '^network\.@device\[0\]\.ports=' "$UCI_STATE"; then
    bad "the captive bridge still lists ports after a multi-port move"
else
    ok "the captive bridge's port list is cleared for all three ports"
fi

echo "== the address steps past an upstream collision too"
seed_stock 'eth1' '192.168.2.1'
upstream '3: eth1    inet 192.168.3.17/24 brd 192.168.3.255 scope global eth1'
draw 5a 5a
run_same_version
[ "$(ipaddr)" = "192.168.4.1" ] \
    && ok "an upstream network on the LAN+2 /24 pushes br-mgmt to 192.168.4.1" \
    || bad "upstream collision: expected 192.168.4.1, got '$(ipaddr)'"

echo "== with every LAN-adjacent /24 taken, the fallback is a draw from 10/8"
seed_stock 'eth1' '192.168.2.1'
printf '3: eth1    inet 192.168.3.17/24 brd 192.168.3.255 scope global eth1\n4: eth2    inet 192.168.4.17/24 brd 192.168.4.255 scope global eth2\n' > "$FAKE_IP_UPSTREAM"
draw 5a 5a
run_same_version
[ "$(ipaddr)" = "10.90.90.1" ] \
    && ok "no free LAN-adjacent /24: drew 10.90.90.1" \
    || bad "expected the drawn 10.90.90.1, got '$(ipaddr)'"
grep -q 'overlaps an upstream-side network' "$LOGFILE" \
    && ok "the fallback is recorded in the setup log" \
    || bad "no log line recording the subnet collision"

echo "== a router with no private network takes the LAN+1 /24"
seed_stock 'eth1'
no_upstream
draw 5a 5a
run_same_version
[ "$(ipaddr)" = "192.168.2.1" ] \
    && ok "no private network: br-mgmt takes the adjacent 192.168.2.1" \
    || bad "expected 192.168.2.1, got '$(ipaddr)'"

echo "== a router with no wired ports at all is refused, loudly, with no writes"
seed_portless
no_upstream
: > "$LOGFILE"
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "the refused run still exits 0 (the rest of the setup completed)" \
              || bad "the refused run exited $rc — the driver must continue after the refusal"
grep -q 'ERROR: skipping the wired-LAN management bridge' "$LOGFILE" \
    && ok "the refusal is logged as an ERROR" \
    || bad "the refusal is not logged (an operator would see nothing)"
check_nothing_written "portless router"
grep -qE '^network\.@device\[0\]\.ports=' "$UCI_STATE" \
    && bad "the refused run invented a port on the captive bridge" \
    || ok "the refused run left the captive bridge's (empty) port list alone"

echo "== the address is never re-derived for a router that already has one"
seed_stock 'eth1' '192.168.2.1'
no_upstream
printf 'network.mgmt=interface\nnetwork.mgmt.proto=static\nnetwork.mgmt.device=br-mgmt\nnetwork.mgmt.ipaddr=10.44.44.1\nnetwork.mgmt.netmask=255.255.255.0\nnetwork.mgmt_bridge=device\nnetwork.mgmt_bridge.type=bridge\nnetwork.mgmt_bridge.name=br-mgmt\nnetwork.mgmt_bridge.ports=eth1\n' >> "$UCI_STATE"
run_same_version
[ "$(ipaddr)" = "10.44.44.1" ] \
    && ok "an operator-chosen management address survives a reinstall" \
    || bad "the reinstall moved the management address to '$(ipaddr)'"

# ------------------------------------------------- 2. the full-setup path
# The full path cannot be driven end to end here: it writes /etc/profile and
# /proc/sys/kernel/hostname, i.e. the host. Its writer is exercised through the
# same LIB_ONLY seam the other 99-tollgate-setup tests use, and both driver call
# sites are pinned statically — a writer reachable from only one of the two
# setup paths cannot converge a router that never runs a full setup again.
echo "== the full-setup path calls the writer, after the private network"
if grep -qE '^setup_private_network .*$' "$SCRIPT_UNDER_TEST" &&
   grep -qE '^setup_mgmt_bridge .*$' "$SCRIPT_UNDER_TEST"; then
    priv_line=$(grep -nE '^setup_private_network ' "$SCRIPT_UNDER_TEST" | head -n1 | cut -d: -f1)
    mgmt_line=$(grep -nE '^setup_mgmt_bridge ' "$SCRIPT_UNDER_TEST" | head -n1 | cut -d: -f1)
    commit_line=$(grep -nE '^commit_all$' "$SCRIPT_UNDER_TEST" | head -n1 | cut -d: -f1)
    if [ "$priv_line" -lt "$mgmt_line" ] && [ "$mgmt_line" -lt "$commit_line" ]; then
        ok "full setup: setup_private_network -> setup_mgmt_bridge -> commit_all"
    else
        bad "full setup order is wrong (private=$priv_line mgmt=$mgmt_line commit=$commit_line) — the private /24 must exist first and the writes must be committed"
    fi
else
    bad "the full-setup driver does not call setup_mgmt_bridge"
fi
if grep -qE '^    setup_mgmt_bridge$' "$SCRIPT_UNDER_TEST"; then
    ok "the verify/repair path calls the same writer"
else
    bad "the verify/repair path does not call setup_mgmt_bridge — a reset router would never be repaired"
fi

echo "== the writer itself, driven through the LIB_ONLY seam"
seed_stock 'eth1' '192.168.2.1'
no_upstream
draw 5a 5a
GATEWAY_NAME="TollGate-TEST"
export GATEWAY_NAME
# shellcheck disable=SC1090  # the path is built from $ROOT on purpose
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
if command -v setup_mgmt_bridge >/dev/null 2>&1; then
    ok "script exposes setup_mgmt_bridge()"
    : > "$LOGFILE"
    setup_mgmt_bridge >/dev/null 2>&1
    rc=$?
    [ "$rc" = 0 ] && ok "setup_mgmt_bridge returns 0 on a router with ports" \
                  || bad "setup_mgmt_bridge returned $rc on a router with ports"
    check_bridge_written "full-setup writer" "192.168.3.1"
    check_ports_moved "full-setup writer" "eth1"
    check_no_way_out "full-setup writer"
else
    bad "script does not expose setup_mgmt_bridge — the full setup path has nothing to call"
fi

echo "== the refusal is the writer's return value, not only a log line"
seed_portless
: > "$LOGFILE"
setup_mgmt_bridge >/dev/null 2>&1
rc=$?
[ "$rc" != 0 ] && ok "setup_mgmt_bridge returns non-zero when there is nothing to move" \
               || bad "setup_mgmt_bridge returned 0 with no ports anywhere — a caller cannot tell it did nothing"
check_nothing_written "LIB_ONLY refusal"

# ------------------------------------------------- 3. one gate, pinned (D6)
# The change that would look like it implements the request while breaking the
# gate is a second `config nodogsplash` section. Nothing shipped may create one,
# and the (single) gateway interface stays the captive bridge.
echo "== exactly one nodogsplash section, pinned to the captive bridge"
n_add=$(grep -c "uci add nodogsplash nodogsplash" "$SCRIPT_UNDER_TEST")
[ "$n_add" = 1 ] && ok "one writer creates the nodogsplash section" \
                 || bad "$n_add writers create a nodogsplash section (want 1 — a second instance breaks the first)"
n_gw=$(grep -c "uci set nodogsplash.@nodogsplash\[0\].gatewayinterface='br-lan'" "$SCRIPT_UNDER_TEST")
[ "$n_gw" = 1 ] && ok "the gateway interface is written once, as br-lan" \
                || bad "$n_gw writers of gatewayinterface='br-lan' (want 1)"
stray_gw=$(grep -nE "uci set nodogsplash\.[^ ]*gatewayinterface" "$SCRIPT_UNDER_TEST" |
           grep -v "nodogsplash\.@nodogsplash\[0\]\.gatewayinterface='br-lan'" || true)
if [ -z "$stray_gw" ]; then
    ok "no other section is ever given a gateway interface"
else
    bad "a gateway interface is written outside @nodogsplash[0]: $stray_gw"
fi
if grep -qE "uci set network\.mgmt\.gatewayinterface|nodogsplash.*'br-mgmt'" "$SCRIPT_UNDER_TEST"; then
    bad "the mgmt bridge was given a captive gate — one nodogsplash cannot gate two bridges (see the ADR)"
else
    ok "br-mgmt is never given a captive gate"
fi

# -------------------------------------------------------- 4. it actually ships
echo "== the writer under test is the one that ships"
grep -q 'files/etc/uci-defaults/99-tollgate-setup' packaging/Makefile \
    && ok "packaging/Makefile installs etc/uci-defaults/99-tollgate-setup" \
    || bad "packaging/Makefile no longer installs the setup script"
grep -q 'packaging/files/etc/uci-defaults/99-tollgate-setup' packaging/local-build-ipk.sh \
    && ok "packaging/local-build-ipk.sh stages 99-tollgate-setup into the payload" \
    || bad "packaging/local-build-ipk.sh no longer stages the setup script"

# ------------------------------------------------------------- 5. RED controls
# A guard that has never failed is decoration. Strip each load-bearing line from
# a copy of the shipped script and require the matching assertions to fail.
echo "== RED controls: each property must fail when its line is removed"
red_probe() { # <mutant> <seed fn> <check fn> <label>
    local mutant="$1" seedf="$2" checkf="$3" label="$4" before fails
    SCRIPT_UNDER_TEST="$mutant"
    "$seedf"
    no_upstream
    draw 5a 5a
    run_same_version
    before=$FAIL
    fails=$( ( "$checkf" "RED" >/dev/null 2>&1; echo "$((FAIL - before))" ) )
    if [ "${fails:-0}" -gt 0 ]; then
        ok "RED: $label — the guard fails without it ($fails assertions red)"
    else
        bad "RED: $label — the guard passed against a script without it, so it tests nothing"
    fi
}
seed_moved() { seed_stock 'eth1' '192.168.2.1'; }

# The mutant for (b) removes the writer's call, so there is no address to
# compare against: the check is "the bridge's options exist at all".
check_bridge_written_red() { # <label>
    local label="$1" absent=0 key
    for key in 'network.mgmt=interface' 'network.mgmt.device=br-mgmt' \
               'network.mgmt_bridge.name=br-mgmt' 'dhcp.mgmt.interface=mgmt' \
               'firewall.mgmt_zone.name=mgmt'; do
        has "$key" || absent=$((absent + 1))
    done
    if [ "$absent" = 0 ]; then
        ok "$label: the management bridge is configured"
    else
        bad "$label: $absent of the management bridge's options are missing"
    fi
}

# Build a copy of the SHIPPED script with one line removed. The copy gets the
# same SETUP_FLAG/LOGFILE rewrite as the script under test: a mutant that kept
# the real paths would find no marker on the machine running the test, take the
# FULL-setup branch, and the probe would be measuring a different path than the
# one it names (and writing to the host's /etc/profile and /proc while at it).
make_mutant() { # <out> <fixed string to remove>; non-zero when it removed nothing
    local out="$1" strip="$2"
    sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
        -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
        "$ROOT/$SCRIPT" | grep -v -F -- "$strip" > "$out"
    if grep -q -F -- "$strip" "$out"; then
        return 1
    fi
    return 0
}

MUTANT_COPY="$TMP/99-tollgate-setup.mutant"

# (a) the ports are moved, not copied: without the delete the captive bridge
#     keeps its list and the port ends up on BOTH bridges.
if make_mutant "$MUTANT_COPY" 'uci -q delete "$sec.ports"'; then
    red_probe "$MUTANT_COPY" seed_moved check_ports_moved "the captive bridge's port list is cleared"
else
    bad "RED control (a): could not strip the port-list delete — the control is vacuous"
fi

# (b) the same-version (reinstall/upgrade) path reaches the writer at all.
if make_mutant "$MUTANT_COPY" '    setup_mgmt_bridge'; then
    red_probe "$MUTANT_COPY" seed_moved check_bridge_written_red "the bridge is written on the reinstall path"
else
    bad "RED control (b): could not strip the verify-path call — the control is vacuous"
fi

# (c) the no-way-out half: without forward REJECT the zone is only as strict as
#     its default, which is what would hand the cable free internet.
if make_mutant "$MUTANT_COPY" "uci set firewall.mgmt_zone.forward='REJECT'"; then
    red_probe "$MUTANT_COPY" seed_moved check_no_way_out "the mgmt zone is forward REJECT"
else
    bad "RED control (c): could not strip the zone's forward policy — the control is vacuous"
fi

SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
