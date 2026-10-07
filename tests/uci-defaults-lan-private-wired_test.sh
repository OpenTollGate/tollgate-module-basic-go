#!/usr/bin/env bash
# Offline test for the wired LAN ports' placement on br-private in
# packaging/files/etc/uci-defaults/99-tollgate-setup. No router, no daemon,
# no network.
#
# What this pins. The wired LAN ports used to sit on the CAPTIVE bridge
# (br-lan) as members of the base image's bridge device section: a cabled
# client was a guest, paid at the portal, and both administration guards
# (31-admin-board-not-guest-reachable.nft, 32-luci-not-guest-reachable.nft)
# dropped the admin board (:8090/:8443) and LuCI (:8080/:443) for it. The
# operator's criterion for this release is that the physical LAN port lands
# on br-private — the operator's own trusted network: free internet
# (firewall.private_zone forwards to wan), the admin surfaces answering,
# and LuCI reachable — while the public TollGate-* SSID stays on the captive
# bridge behind the portal, with guests still unable to reach the admin
# board or LuCI.
#
# This is deliberately the MINIMAL release path: no role field, no br-mgmt,
# no /etc/config/tollgate role file. The full role machinery lives in #607
# and lands after the release.
#
# Properties pinned here:
#   1. the placement is a MOVE: the wired ports discovered on the captive
#      bridge's device section land on network.private_bridge.ports, and the
#      captive section's port list is CLEARED (a port on two bridges is a
#      port on two layer-2 domains);
#   2. the ports are DISCOVERED, never named: eth1 on the MT3000, lan1…lan5
#      elsewhere — whatever the image's device section lists is what moves,
#      so a multi-port board moves all of its ports;
#   3. it is IDEMPOTENT: a second run changes nothing and duplicates no
#      entry;
#   4. the UPGRADE path preserves the placement: the same-version
#      verify/repair path re-runs the writer, and a router whose port list
#      was reset back onto the captive bridge (factory reset, sysupgrade -n)
#      is repaired; the module's keep-list still carries /etc/config/network
#      so an upgrade keeps the config the writer produced;
#   5. the GUARDS are untouched and still br-lan-scoped:
#      20-nds-enforce.nft, 30-backend-firewall.nft,
#      31-admin-board-not-guest-reachable.nft and
#      32-luci-not-guest-reachable.nft are byte-identical to upstream main
#      (pinned by digest below) and keep their `iifname "br-lan"` literals —
#      that literal is what keeps a guest off :8090/:8443 and LuCI while a
#      br-private client gets in;
#   6. a negative control: the assertions must FAIL against a copy of the
#      script with the writer removed, so the suite cannot pass vacuously.
#
# How it checks: the shipped driver is copied into a temp dir (only its
# SETUP_FLAG and LOGFILE absolute paths are rewritten) and RUN against a
# fake `uci`, `apk`, `ip` and /etc/shadow — the same harness shape
# tests/uci-defaults-same-version-allowlist_test.sh and #607's
# tests/uci-defaults-lan-port-role_test.sh use — so the assertions are about
# the config the shipped code actually generates, not a grep of the source.
#
# Usage: bash tests/uci-defaults-lan-private-wired_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
NFT_DIR="packaging/files/etc/nftables.d"
KEEP_LIST="packaging/files/lib/upgrade/keep.d/tollgate"

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
# Flat-file stand-in: one "key=value" line per option, so a list option is
# one line per entry (which is how `uci get` renders a list). `delete` is
# SECTION-AWARE on purpose: real uci removes a section and every option in
# it, and this writer deletes option lists from sections.
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
drop_key() { # remove <key>= and any <key>.*= — i.e. the section and its options
    awk -v k="$1" 'index($0, k "=") != 1 && index($0, k ".") != 1' "$state" > "$state.tmp" 2>/dev/null
    mv "$state.tmp" "$state"
}
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
        key="${key%%[[:space:]]*}"
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
        drop_key "$1"
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

# Credential/router-home fixtures so the driver never touches the host.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
export ROUTER_HOME_DIR="$TMP/router-home"

# ------------------------------------------------------------- the fixtures
# A stock router: two radios, the stock guest APs, the captive bridge with
# its IMAGE-OWNED port list, one nodogsplash section pinned to the captive
# bridge, and the operator's private network.
#   seed_router <ports...>
seed_router() {
    local p
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
        for p in "$@"; do
            printf 'network.@device[0].ports=%s\n' "$p"
        done
        printf '%s\n' 'network.wan=interface' 'network.wan.device=eth0' 'network.wan.proto=dhcp'
        printf '%s\n' 'system.@system[0]=system' 'system.@system[0].hostname=TollGate'
        printf '%s\n' 'dhcp.@dnsmasq[0]=dnsmasq'
        printf '%s\n' 'nodogsplash.@nodogsplash[0]=nodogsplash'
        printf '%s\n' 'nodogsplash.@nodogsplash[0].gatewayinterface=br-lan'
        printf '%s\n' 'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 443'
        printf '%s\n' 'wireless.private_radio0.ssid=test-private'
        printf '%s\n' 'wireless.private_radio0.key=Alpha-Bravo-Charlie-11'
    } >> "$UCI_STATE"
}

count() { grep -F -c -- "$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has()   { grep -F -q -- "$1" "$UCI_STATE"; }
line()  { grep -F -x -- "$1" "$UCI_STATE" 2>/dev/null; }
no_upstream() { : > "$FAKE_IP_UPSTREAM"; }
draw() { printf '%s\n' "$@" > "$HEXDUMP_QUEUE_FILE"; }

# Run the writer through the repo's LIB_ONLY seam: sources the shipped
# script (which returns before its own driver logic) and calls the private
# network setup followed by the wired-port writer.
run_writer() {
    : > "$LOGFILE"
    # shellcheck disable=SC1090  # the path is built from $ROOT on purpose
    TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT_UNDER_TEST" >/dev/null 2>&1
    R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
    setup_lan_ports_private >/dev/null 2>&1
    return $?
}

# The whole shipped script on the reinstall/upgrade (same-version) path.
run_same_version() {
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

# Save the stock fake uci so per-test shims can restore it.
cp "$TMP/bin/uci" "$TMP/bin/uci.real"
uci_restore() { cp -f "$TMP/bin/uci.real" "$TMP/bin/uci"; }

log_contains() { grep -qF -- "$1" "$LOGFILE"; }

# Run the writer with a custom fake uci shim installed at $TMP/bin/uci.
# The shim runs from a clean $TMP/bin and must exit 0 after forking the
# stock implementation for unrelated commands.
run_writer_with_uci() {
    local shim="$1"
    : > "$LOGFILE"
    # Build the PRECONDITION with the stock fake uci first: the private bridge
    # must exist before the writer under test runs, otherwise the writer
    # correctly bails at its "no private bridge" precondition and the shim
    # would be testing that path instead of the port-move guard. The shim is
    # installed only for the writer itself, so the only thing it breaks is the
    # write this negative control is about.
    uci_restore
    # shellcheck disable=SC1090
    TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT_UNDER_TEST" >/dev/null 2>&1
    R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
    cp -f "$shim" "$TMP/bin/uci"
    chmod +x "$TMP/bin/uci"
    setup_lan_ports_private >/dev/null 2>&1
    local rc=$?
    uci_restore
    return $rc
}

# ------------------------------------------------------------- assertions
ports_on_private() { # <port> [<port> ...]
    local port want=0 n
    for port in "$@"; do
        want=$((want + 1))
        n=$(count "network.private_bridge.ports=$port")
        [ "$n" = 1 ] && ok "network.private_bridge lists $port exactly once" \
                     || bad "network.private_bridge lists $port $n time(s) (want 1)"
    done
}

captive_cleared() { # the image's device section holds no port list
    if grep -qE '^network\.@device\[0\]\.ports=' "$UCI_STATE"; then
        bad "the captive bridge's device section still lists ports ($(grep -E '^network\.@device\[0\]\.ports=' "$UCI_STATE" | cut -d= -f2- | tr '\n' ' ')) — the move must CLEAR it"
    else
        ok "the captive bridge's device section lists no ports"
    fi
}

nodogsplash_still_captive() {
    if has 'nodogsplash.@nodogsplash[0].gatewayinterface=br-lan'; then
        ok "nodogsplash is still pinned to the captive bridge (br-lan)"
    else
        bad "nodogsplash.gatewayinterface is no longer br-lan"
    fi
}

# ===========================================================================
# 1. the writer — a stock router's wired ports move to br-private
# ===========================================================================
echo "== a stock router's wired ports move from the captive bridge to br-private"
seed_router eth1
no_upstream
draw 5a 5a
run_writer
rc=$?
if [ "$rc" = 0 ]; then ok "the writer returns 0 on a stock router"; else bad "the writer returned $rc on a stock router"; fi
ports_on_private eth1
captive_cleared
nodogsplash_still_captive

echo "== a multi-port board moves every port it finds (lan1 lan2 lan3)"
seed_router lan1 lan2 lan3
no_upstream
draw 5a 5a
run_writer
ports_on_private lan1 lan2 lan3
captive_cleared

echo "== the ports are discovered, never named: no port name is hard-coded"
# Comments may name boards' port shapes (eth1 on the MT3000); the CODE must
# not. Strip comment lines before checking, so the assertion bites on a
# hard-coded port in a uci write, not on documentation.
if sed 's/#.*$//' "$ROOT/$SCRIPT" | grep -qE '(^|[^a-z])eth1([^a-z]|$)'; then
    bad "the setup script's CODE names 'eth1' — the ports must be discovered from the bridge's device section"
else
    ok "the setup script's code never names a specific port (eth1/lan1/... are board-specific)"
fi

# ===========================================================================
# 2. idempotence — a second run changes nothing
# ===========================================================================
echo "== a converged router is a no-op: a second run duplicates nothing"
seed_router eth1
no_upstream
draw 5a 5a
run_writer
snapshot1=$(grep -E '^(network\.private_bridge\.ports|network\.@device)' "$UCI_STATE" | sort)
run_writer
snapshot2=$(grep -E '^(network\.private_bridge\.ports|network\.@device)' "$UCI_STATE" | sort)
if [ "$snapshot1" = "$snapshot2" ]; then
    ok "a second run leaves the placement untouched"
else
    bad "a second run changed the placement:"$'\n'"before: $snapshot1"$'\n'"after:  $snapshot2"
fi
ports_on_private eth1
captive_cleared

# ===========================================================================
# 3. the upgrade path — same-version repair re-places reset ports
# ===========================================================================
echo "== the same-version (reinstall/upgrade) path repairs a reset port list"
seed_router eth1
no_upstream
draw 5a 5a
# Simulate the converged state (previous install placed the port), then a
# factory reset put it back on the captive bridge's image-owned section.
run_writer
printf 'network.@device[0].ports=eth1\n' >> "$UCI_STATE"
grep -v -F -- 'network.private_bridge.ports=eth1' "$UCI_STATE" > "$UCI_STATE.tmp"
mv "$UCI_STATE.tmp" "$UCI_STATE"
run_same_version
rc=$?
if [ "$rc" = 0 ]; then ok "the same-version path returns 0"; else bad "the same-version path returned $rc"; fi
ports_on_private eth1
captive_cleared

echo "== the same-version path leaves a converged router's placement alone"
seed_router eth1
no_upstream
draw 5a 5a
run_writer
run_same_version
ports_on_private eth1
captive_cleared

echo "== the module's keep-list still carries /etc/config/network (upgrade survival)"
if grep -qF -- '/etc/config/network' "$KEEP_LIST"; then
    ok "packaging/files/lib/upgrade/keep.d/tollgate lists /etc/config/network — a sysupgrade keeps the placement"
else
    bad "the keep-list no longer lists /etc/config/network — an upgrade would lose the wired-port placement"
fi

# ===========================================================================
# 4. the guards — untouched, and still scoped to the captive bridge
# ===========================================================================
echo "== the guard fragments are unchanged and still br-lan-scoped"
# Digests of the four guards. Three are byte-identical to upstream main
# (54e8c368) because the wired-port placement must not touch them; the guards
# are iifname "br-lan"-literal, which is what keeps a guest off :8090/:8443 and
# LuCI while a br-private client gets in.
#
# 30-backend-firewall is the deliberate exception: it now exempts br-private as
# well as br-lan, because br-private is the one network the admin board is
# reachable from and the board reads every value out of the :2121 API. The
# digest pinned below is therefore the NEW content, not main's. The invariant
# itself is owned by tests/packaging/backend-api-owner-network_test.sh; this
# block only pins the bytes.
#
# 31-admin-board-not-guest-reachable is the second deliberate exception, and a
# comment-only one: the entry-port decision swaps which UI owns :8080/:443, so the
# comment there now states which pair it drops and why :8080/:443 are NOT exempt
# (they are LuCI's pair after the swap, and 32-luci-not-guest-reachable drops it on
# br-lan). The RULES are unchanged — a diff of the two revisions with comment lines
# stripped is empty — and the scope assertions below still prove the br-lan literal,
# which is the property that matters. Re-pinned deliberately, with that proof.
GUARD_DIGESTS='20-nds-enforce 4cae6ef31d23d10ee4a730e1b2797281
30-backend-firewall 9d4702bdc1d2b468fb39a402372bb27c
31-admin-board-not-guest-reachable e854e0650a2a532cf787e69156b3ddae
32-luci-not-guest-reachable c86e5dc022b8a4108f305eba7febefe1'
while read -r frag digest; do
    [ -n "$frag" ] || continue
    got=$(md5sum "$NFT_DIR/$frag.nft" 2>/dev/null | cut -d' ' -f1)
    if [ "$got" = "$digest" ]; then
        ok "$frag.nft matches its pinned digest ($digest)"
    else
        bad "$frag.nft changed (md5 $got, want $digest)"
    fi
done <<< "$GUARD_DIGESTS"
# The scope literal itself: each guard must still carry iifname "br-lan".
if grep -qF -- 'iifname "br-lan"' "$NFT_DIR/20-nds-enforce.nft"; then
    ok "20-nds-enforce.nft is still br-lan-scoped"
else
    bad "20-nds-enforce.nft lost its iifname \"br-lan\" scope"
fi
if grep -qF -- 'iifname "br-lan"' "$NFT_DIR/31-admin-board-not-guest-reachable.nft"; then
    ok "31-admin-board-not-guest-reachable.nft is still br-lan-scoped"
else
    bad "31-admin-board-not-guest-reachable.nft lost its iifname \"br-lan\" scope"
fi
if grep -qF -- 'iifname "br-lan"' "$NFT_DIR/32-luci-not-guest-reachable.nft"; then
    ok "32-luci-not-guest-reachable.nft is still br-lan-scoped"
else
    bad "32-luci-not-guest-reachable.nft lost its iifname \"br-lan\" scope"
fi
# 30-backend-firewall scopes by NOT-the-LAN-bridges
# (iifname != { "br-lan", "br-private", "lo" }): br-lan because a guest pays
# through the :2121 API, br-private because the admin board — which lives on
# that network, not on the captive bridge — reads all of its data from the same
# API. Dropping br-private there is what left the board rendering with every
# panel empty.
if grep -qF -- 'iifname != { "br-lan", "br-private", "lo" }' "$NFT_DIR/30-backend-firewall.nft"; then
    ok "30-backend-firewall.nft exempts both LAN bridges and lo"
else
    bad "30-backend-firewall.nft lost its LAN-bridge exemption set"
fi

# ===========================================================================
# 1b. negative controls for the verify-before-clear guard
# ===========================================================================
echo "== add_list failure: the captive bridge's port list is NOT cleared"
FAIL_ADD_LIST="$TMP/uci-fail-add-list"
cat > "$FAIL_ADD_LIST" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
drop_key() {
    awk -v k="$1" 'index($0, k "=") != 1 && index($0, k ".") != 1' "$state" > "$state.tmp" 2>/dev/null
    mv "$state.tmp" "$state"
}
case "$cmd" in
    get)
        vals="$(values "$1")"
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    set|add|del_list|delete)
        # delegate to the real fake uci by re-exec
        exec "$TMP/bin/uci.real" ${q:+-q} "$cmd" "$@"
        ;;
    add_list)
        key="${1%%=*}"
        if [ "$key" = "network.private_bridge.ports" ]; then
            echo "fake uci add_list failure" >&2
            exit 1
        fi
        printf '%s\n' "$1" >> "$state"
        ;;
    show|export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$FAIL_ADD_LIST"
seed_router eth1
no_upstream
draw 5a 5a
run_writer_with_uci "$FAIL_ADD_LIST"
rc=$?
if [ "$rc" != 0 ]; then ok "add_list failure: writer returns non-zero ($rc)"; else bad "add_list failure: writer returned 0"; fi
if grep -qE '^network\.@device\[0\]\.ports=' "$UCI_STATE"; then
    ok "add_list failure: the captive bridge's port list is preserved"
else
    bad "add_list failure: the captive bridge's port list was cleared despite add_list failure"
fi
if ! grep -qE '^network\.private_bridge\.ports=' "$UCI_STATE"; then
    ok "add_list failure: private_bridge received no ports"
else
    bad "add_list failure: private_bridge received ports despite add_list failure"
fi
if log_contains "ERROR" && log_contains "eth1"; then
    ok "add_list failure: the log names the missing port"
else
    bad "add_list failure: the error log does not name the missing port"
fi


echo "== add_list silent no-op (rc 0, target unchanged): verification catches it"
NOOP_ADD_LIST="$TMP/uci-noop-add-list"
cat > "$NOOP_ADD_LIST" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
drop_key() {
    awk -v k="$1" 'index($0, k "=") != 1 && index($0, k ".") != 1' "$state" > "$state.tmp" 2>/dev/null
    mv "$state.tmp" "$state"
}
case "$cmd" in
    get)
        vals="$(values "$1")"
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    set|add|del_list|delete)
        exec "$TMP/bin/uci.real" ${q:+-q} "$cmd" "$@"
        ;;
    add_list)
        # silently accept but do nothing
        exit 0
        ;;
    show|export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$NOOP_ADD_LIST"
seed_router eth1
no_upstream
draw 5a 5a
run_writer_with_uci "$NOOP_ADD_LIST"
rc=$?
if [ "$rc" != 0 ]; then ok "silent no-op: writer returns non-zero ($rc)"; else bad "silent no-op: writer returned 0"; fi
if grep -qE '^network\.@device\[0\]\.ports=' "$UCI_STATE"; then
    ok "silent no-op: the captive bridge's port list is preserved"
else
    bad "silent no-op: the captive bridge's port list was cleared despite verification failure"
fi
if log_contains "ERROR" && log_contains "eth1"; then
    ok "silent no-op: the log names the missing port"
else
    bad "silent no-op: the error log does not name the missing port"
fi


echo "== empty source and empty target: reported as an error, nothing written"
seed_router
no_upstream
draw 5a 5a
# setup_private_network above creates private_bridge; captive has no ports
run_writer
rc=$?
if [ "$rc" != 0 ]; then ok "empty source+target: writer returns non-zero ($rc)"; else bad "empty source+target: writer returned 0"; fi
if ! grep -qE '^network\.private_bridge\.ports=' "$UCI_STATE"; then
    ok "empty source+target: private_bridge still has no ports"
else
    bad "empty source+target: private_bridge gained ports from nowhere"
fi
if log_contains "ERROR" && log_contains "neither bridge"; then
    ok "empty source+target: log reports the broken state"
else
    bad "empty source+target: log does not report the broken state"
fi


echo "== bridge device section beyond index 23 is still found and moved"
seed_router eth1
no_upstream
# push br-lan far down the anonymous device list
awk 'BEGIN { for (i=0; i<30; i++) print "network.@device[" i "]=device"; print "network.@device[30].name=br-lan"; print "network.@device[30].type=bridge"; print "network.@device[30].ports=eth1" }' /dev/null >> "$UCI_STATE"
# remove the original @device[0] block that seed_router wrote
awk 'index($0, "network.@device[0]") != 1' "$UCI_STATE" > "$UCI_STATE.tmp" && mv "$UCI_STATE.tmp" "$UCI_STATE"
# ensure network.lan still points at br-lan
grep -qF -- 'network.lan.device=br-lan' "$UCI_STATE" || printf 'network.lan.device=br-lan\n' >> "$UCI_STATE"
draw 5a 5a
run_writer
rc=$?
if [ "$rc" = 0 ]; then ok "late bridge: writer returns 0"; else bad "late bridge: writer returned $rc"; fi
ports_on_private eth1
if ! grep -qE '^network\.@device\[30\]\.ports=' "$UCI_STATE"; then
    ok "late bridge: the source section was cleared"
else
    bad "late bridge: the source section still lists ports"
fi


# ===========================================================================
# 5. negative control — the assertions must fail without the writer
# ===========================================================================
echo "== negative control: a script with a no-op writer fails the assertions"
NEG="$TMP/99-tollgate-setup.neg"
sed -e "s|^setup_lan_ports_private() {|setup_lan_ports_private() {\n    return 0 # negative control: the writer is neutralised|" \
    "$SCRIPT_UNDER_TEST" > "$NEG"
if ! grep -qF -- '# negative control: the writer is neutralised' "$NEG"; then
    bad "negative control: could not neutralise setup_lan_ports_private in the copy"
else
    ok "negative control: the writer is neutralised in the copied script"
fi
export UCI_STATE="$TMP/uci.neg"
seed_router eth1
no_upstream
draw 5a 5a
: > "$LOGFILE"
# shellcheck disable=SC1090  # the path is built from $ROOT on purpose
TOLLGATE_SETUP_LIB_ONLY=1 . "$NEG" >/dev/null 2>&1
R2G=radio0 R5G=radio1 setup_private_network >/dev/null 2>&1
setup_lan_ports_private >/dev/null 2>&1
if grep -qE '^network\.private_bridge\.ports=' "$UCI_STATE"; then
    bad "negative control: the port moved without the writer (control is vacuous)"
else
    ok "negative control: without the writer the port stays on the captive bridge"
fi
if grep -qE '^network\.@device\[0\]\.ports=eth1$' "$UCI_STATE"; then
    ok "negative control: the captive bridge keeps its port (the move assertion would fail)"
else
    bad "negative control: the captive bridge lost its port without the writer"
fi
export UCI_STATE="$TMP/uci.state"

# ===========================================================================
echo
if [ "$FAIL" -eq 0 ]; then
    echo "lan-private-wired: all $PASS checks passed"
    exit 0
fi
echo "lan-private-wired: $FAIL check(s) FAILED, $PASS passed"
exit 1
