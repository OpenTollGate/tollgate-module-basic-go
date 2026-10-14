#!/usr/bin/env bash
# Rootfs-container tier of the uci-defaults validation (#521): runs the real
# first-boot setup script END-TO-END inside real OpenWrt userspace — BusyBox
# ash, the real uci binary, real sed/awk/tr — against fixture wireless
# topologies with authored band ground truth, on both package ecosystems.
#
# Complements the offline tests (uci-defaults-band_test.sh & friends), which
# drive the script's functions through a fake uci: fast, but by construction
# blind to real-uci semantics and GNU-vs-BusyBox differences — exactly where
# this tier earns its keep (the fake-uci tier once passed a matrix that real
# uci then failed on private-network setup).
#
# Requires docker. When docker is unavailable the test prints skip lines and
# exits 0, so it can ride runners without container support.
#
# Usage:   bash tests/uci-defaults-rootfs_test.sh
# Images:  UCI_ROOTFS_IMAGES="x86-64-openwrt-24.10 x86-64-25.12-SNAPSHOT" (default)
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    echo "skip: docker not available — rootfs tier needs a container runtime"
    exit 0
fi

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
IMAGES="${UCI_ROOTFS_IMAGES:-x86-64-openwrt-24.10 x86-64-25.12-SNAPSHOT}"
TOPOLOGIES="normal swapped single-2g triband legacy-hwmode upgrade-misbound sta-preserved"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
WORK="$TMP/work"
mkdir -p "$WORK/fixtures"
cp "$SCRIPT" "$WORK/setup.sh"

# ---------------------------------------------------------------- fixtures
# Hand-authored /etc/config/wireless topologies. Ground truth (which radio
# owns which band) is authored per fixture and asserted by the driver — the
# section names deliberately disagree with the bands on the swapped and
# misbound topologies, because radio numbering carries no band meaning.

cat > "$WORK/fixtures/normal.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '1'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF

cat > "$WORK/fixtures/swapped.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '1'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF

cat > "$WORK/fixtures/single-2g.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '6'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF

cat > "$WORK/fixtures/triband.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '6'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-device 'radio2'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:01.0'
	option channel '53'
	option band '6g'
	option htmode 'EHT80'

config wifi-iface 'default_radio2'
	option device 'radio2'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt-6G'
	option encryption 'none'
EOF

cat > "$WORK/fixtures/legacy-hwmode.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '3'
	option hwmode '11g'
	option htmode 'HT20'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option hwmode '11a'
	option htmode 'HT40'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF

cat > "$WORK/fixtures/upgrade-misbound.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '1'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'tollgate_2g_open'
	option device 'radio0'
	option mode 'ap'
	option network 'lan'
	option ssid 'TollGate-OLD1'
	option encryption 'none'

config wifi-iface 'tollgate_5g_open'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'TollGate-OLD1'
	option encryption 'none'

config wifi-iface 'private_radio0'
	option device 'radio0'
	option mode 'ap'
	option network 'private'
	option ssid 'keepme'
	option encryption 'psk2+ccmp'
	option key 'Alpha-Bravo-01'
EOF

cat > "$WORK/fixtures/sta-preserved.wireless" <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'platform/soc/a000000.wifi'
	option channel '6'
	option band '2g'
	option htmode 'HT20'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'sta'
	option network 'wwan'
	option ssid 'UpstreamNet'
	option encryption 'psk2'

config wifi-device 'radio1'
	option type 'mac80211'
	option path 'pci0000:00/0000:00:00.0'
	option channel '36'
	option band '5g'
	option htmode 'HE80'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option mode 'ap'
	option network 'lan'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF

# ------------------------------------------------------------------ driver
# POSIX sh; runs INSIDE the container against real uci. Prints PASS:/FAIL:
# lines the host harness counts. Fresh --rm container per topology.

cat > "$WORK/driver.sh" <<'EOF'
#!/bin/sh
# In-container driver: seed router defaults, install the fixture, run the
# real setup script (full path, then same-version verify path), assert.
TOPO="$1"

case "$TOPO" in
    normal)           R2G=radio0; R5G=radio1 ;;
    swapped)          R2G=radio1; R5G=radio0 ;;
    single-2g)        R2G=radio0; R5G=""    ;;
    triband)          R2G=radio0; R5G=radio1 ;;
    legacy-hwmode)    R2G=radio0; R5G=radio1 ;;
    upgrade-misbound) R2G=radio1; R5G=radio0 ;;
    sta-preserved)    R2G=radio0; R5G=radio1 ;;
    *) echo "FAIL: unknown topology $TOPO"; exit 1 ;;
esac

# The openwrt/rootfs image ships without the default /etc/config files a
# real router has; seed minimal ones (a full-system environment keeps its
# own — the -s guards make this a no-op there).
[ -s /etc/config/network ] || cat > /etc/config/network <<'NET'
config interface 'loopback'
	option device 'lo'
	option proto 'static'
	option ipaddr '127.0.0.1'
	option netmask '255.0.0.0'

config device
	option name 'br-lan'
	option type 'bridge'
	list ports 'eth0'

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'
	option ip6assign '60'
NET
[ -s /etc/config/system ] || cat > /etc/config/system <<'SYS'
config system
	option hostname 'OpenWrt'
	option timezone 'UTC'
SYS
[ -s /etc/config/dhcp ] || cat > /etc/config/dhcp <<'DHC'
config dnsmasq
	option domainneeded '1'
	option rebind_protection '1'
	option local '/lan/'
	option domain 'lan'

config dhcp 'lan'
	option interface 'lan'
	option start '100'
	option limit '150'
	option leasetime '12h'
DHC
[ -s /etc/config/firewall ] || cat > /etc/config/firewall <<'FW'
config defaults
	option input 'REJECT'
	option output 'ACCEPT'
	option forward 'REJECT'

config zone
	option name 'lan'
	list network 'lan'
	option input 'ACCEPT'
	option output 'ACCEPT'
	option forward 'ACCEPT'

config zone
	option name 'wan'
	list network 'wan'
	list network 'wan6'
	option input 'REJECT'
	option output 'ACCEPT'
	option forward 'REJECT'
	option masq '1'

config forwarding
	option src 'lan'
	option dest 'wan'
FW

cp "/work/fixtures/$TOPO.wireless" /etc/config/wireless
rm -f /etc/tollgate-setup-done /tmp/tollgate-setup.log
sh /work/setup.sh >/tmp/setup.out 2>/tmp/setup.err
echo "SETUP_RC=$?"

check() { [ "$2" = "$3" ] && echo "PASS: $1" || echo "FAIL: $1 (got '$2', want '$3')"; }

# Resolve a TollGate AP section: either literally named <label>, or a stock
# section (default_radioN) adopted and labeled via its .name option.
ap_section() {
    uci -q get "wireless.$1" >/dev/null 2>&1 && { echo "$1"; return; }
    for s in $(uci show wireless | sed -n "s/^wireless\.\([^.]*\)=wifi-iface$/\1/p"); do
        [ "$(uci -q get "wireless.$s.name" 2>/dev/null)" = "$1" ] && { echo "$s"; return; }
    done
}

assert_bindings() {
    S2G=$(ap_section tollgate_2g_open); S5G=$(ap_section tollgate_5g_open)
    # A band whose radio exists MUST get its tollgate_*_open AP section — a
    # missing section is the #103/#173 reinstall class and fails, not skips.
    if [ -n "$R2G" ]; then
        if [ -n "$S2G" ]; then
            check "2g AP ($S2G) on $R2G" "$(uci -q get wireless.$S2G.device)" "$R2G"
        else
            echo "FAIL: 2g radio $R2G exists but tollgate_2g_open AP section missing"
        fi
    fi
    if [ -n "$R5G" ]; then
        if [ -n "$S5G" ]; then
            check "5g AP ($S5G) on $R5G" "$(uci -q get wireless.$S5G.device)" "$R5G"
        else
            echo "FAIL: 5g radio $R5G exists but tollgate_5g_open AP section missing"
        fi
    fi
    [ -n "$R2G" ] && check "private_radio0 on $R2G" "$(uci -q get wireless.private_radio0.device)" "$R2G"
    [ -n "$R5G" ] && check "private_radio1 on $R5G" "$(uci -q get wireless.private_radio1.device)" "$R5G"
    if [ -z "$R5G" ]; then
        if [ -n "$S5G" ]; then
            echo "FAIL: phantom 5g AP ($S5G) on single-band"
        else
            echo "PASS: no phantom 5g AP on single-band"
        fi
    fi
    return 0
}
assert_bindings
# The pre-auth allow list is a stability contract (#472/#513/#516/#518) that
# main has since narrowed to the customer journey: every setup path must hold
# the three journey entries exactly once — and no rerun may duplicate one —
# while the four admin surfaces (LuCI :8080/:443, admin board :8090/:8443)
# must stay OUT of the pre-auth list: a guest on the open SSID must not reach
# an admin login before paying. Mirrors the offline sibling's JOURNEY_PORTS/
# ADMIN_PORTS split (uci-defaults-same-version-allowlist_test.sh). The
# whole-field match mirrors the writer's uci_list_has_port (:8443 must not
# satisfy a :443-class check).
assert_allowlist() {
    nds_users=$(uci -q get nodogsplash.@nodogsplash[0].users_to_router 2>/dev/null || echo "")
    if [ -z "$nds_users" ]; then
        echo "FAIL: users_to_router list absent after setup"
        return 0
    fi
    for port in 2121 2050 2051; do
        if printf '%s\n' "$nds_users" | grep -qE "(^|[[:space:]'])port $port([[:space:]]'|'|\$)"; then
            echo "PASS: allow-list has :$port"
        else
            echo "FAIL: allow-list missing :$port"
        fi
        n=$(printf '%s\n' "$nds_users" | grep -oE "(^|[[:space:]'])port $port([[:space:]]'|'|\$)" | wc -l)
        if [ "$n" -eq 1 ]; then
            echo "PASS: :$port appears exactly once"
        else
            echo "FAIL: :$port appears $n times (duplicate class #516)"
        fi
    done
    for port in 8080 443 8090 8443; do
        if printf '%s\n' "$nds_users" | grep -qE "(^|[[:space:]'])port $port([[:space:]]'|'|\$)"; then
            echo "FAIL: :$port in pre-auth list — admin surface reachable before payment"
        else
            echo "PASS: allow-list has no :$port (admin surface)"
        fi
    done
}
assert_allowlist

case "$TOPO" in
    upgrade-misbound)
        check "private SSID preserved" "$(uci -q get wireless.private_radio0.ssid)" "keepme"
        check "private key preserved"  "$(uci -q get wireless.private_radio0.key)"  "Alpha-Bravo-01"
        ;;
    sta-preserved)
        check "STA mode untouched" "$(uci -q get wireless.default_radio0.mode)" "sta"
        uci -q get wireless.default_radio0.name >/dev/null 2>&1 \
            && echo "FAIL: STA section renamed" \
            || echo "PASS: STA section not renamed"
        ;;
    triband)
        uci -q get wireless.default_radio2.name >/dev/null 2>&1 \
            && echo "FAIL: 6GHz stock iface adopted" \
            || echo "PASS: 6GHz stock iface untouched"
        ;;
esac

# Same-version rerun must take the verify/repair path and keep the bindings.
# The raw script this container runs is never packaged, so its recorded
# marker is unorderable and the script's decision taxonomy hands the
# same-version rerun to VERIFY_REPAIR, not plain VERIFY — and per the
# script's own rule, everything that is not FULL is the verify/repair path.
# Accept both verify-family tokens with a boundary after the token; a FULL
# verdict (or a missing verdict line) fails.
sh /work/setup.sh >/dev/null 2>&1
if grep -qE "Setup branch (VERIFY|VERIFY_REPAIR)([[:space:]]|\$)" /tmp/tollgate-setup.log; then
    echo "PASS: rerun took the verify/repair path"
else
    echo "FAIL: rerun re-ran full setup"
fi
assert_bindings
# The rerun is the duplicate-class witness: it must add nothing.
assert_allowlist
exit 0
EOF

# ------------------------------------------------------------------ runner

for TAG in $IMAGES; do
    echo "== image openwrt/rootfs:$TAG"
    if ! docker pull -q "openwrt/rootfs:$TAG" >/dev/null 2>&1; then
        bad "openwrt/rootfs:$TAG pulled"
        continue
    fi
    ok "openwrt/rootfs:$TAG pulled"
    for TOPO in $TOPOLOGIES; do
        OUT="$(docker run --rm -v "$WORK:/work:ro" "openwrt/rootfs:$TAG" \
            sh /work/driver.sh "$TOPO" 2>&1)"
        RC=$?
        if [ $RC -ne 0 ]; then
            bad "$TAG/$TOPO: container exited $RC"
            printf '%s\n' "$OUT" | sed 's/^/    /' >&2
            continue
        fi
        TOP_PASS=0
        TOP_FAIL=0
        while IFS= read -r line; do
            case "$line" in
                PASS:*) ok "$TAG/$TOPO: ${line#PASS: }"; TOP_PASS=$((TOP_PASS + 1)) ;;
                FAIL:*) bad "$TAG/$TOPO: ${line#FAIL: }"; TOP_FAIL=$((TOP_FAIL + 1)) ;;
                SETUP_RC=0) : ;;
                SETUP_RC=*) bad "$TAG/$TOPO: setup exited ${line#SETUP_RC=}"; TOP_FAIL=$((TOP_FAIL + 1)) ;;
            esac
        done <<< "$OUT"
        if [ $TOP_PASS -eq 0 ] && [ $TOP_FAIL -eq 0 ]; then
            bad "$TAG/$TOPO: no assertions produced"
            printf '%s\n' "$OUT" | sed 's/^/    /' >&2
        fi
    done
done

echo
echo "uci-defaults rootfs tier: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
