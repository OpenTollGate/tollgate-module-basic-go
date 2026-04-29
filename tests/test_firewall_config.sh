#!/bin/sh

SETUP_SCRIPT="${1:-files/etc/uci-defaults/99-tollgate-setup}"
FW_CONFIG="${2:-files/etc/config/firewall-tollgate}"
FAILURES=0

check_file() {
    _path="$1"
    if [ ! -f "$_path" ]; then
        echo "FAIL: file not found: $_path"
        exit 1
    fi
}

assert_contains() {
    _haystack="$1"
    _needle="$2"
    _desc="$3"
    if echo "$_haystack" | grep -qF "$_needle"; then
        echo "[PASS] $_desc"
    else
        echo "[FAIL] $_desc — expected to find: $_needle"
        FAILURES=$((FAILURES + 1))
    fi
}

assert_not_contains() {
    _haystack="$1"
    _needle="$2"
    _desc="$3"
    if echo "$_haystack" | grep -qF "$_needle"; then
        echo "[FAIL] $_desc — should NOT contain: $_needle"
        FAILURES=$((FAILURES + 1))
    else
        echo "[PASS] $_desc"
    fi
}

extract_function() {
    _name="$1"
    _file="$2"
    awk -v fn="$_name" '
        /^[a-z_]+\(\)/ {
            if ($0 ~ "^" fn "\\(\\)") { in_fn = 1; buf = $0 "\n"; next }
            if (in_fn) { in_fn = 0 }
        }
        in_fn && /^}/ { buf = buf $0 "\n"; in_fn = 0; print buf; exit }
        in_fn { buf = buf $0 "\n" }
    ' "$_file"
}

check_file "$SETUP_SCRIPT"
check_file "$FW_CONFIG"

SETUP_CONTENT=$(cat "$SETUP_SCRIPT")
FW_CONTENT=$(cat "$FW_CONFIG")

echo "=== Testing: setup script ==="
echo ""

echo "--- firewall-tollgate regression guard ---"
assert_contains "$FW_CONTENT" "Allow-TollGate-In" "firewall-tollgate contains Allow-TollGate-In rule"
assert_not_contains "$FW_CONTENT" "netbird" "firewall-tollgate does NOT contain netbird entries"

echo ""
echo "--- setup_firewall_include is a no-op ---"
INCLUDE_FN=$(extract_function "setup_firewall_include" "$SETUP_SCRIPT")
if [ -n "$INCLUDE_FN" ]; then
    assert_contains "$INCLUDE_FN" "return" "setup_firewall_include is a no-op (contains return)"
    assert_not_contains "$INCLUDE_FN" "uci add firewall include" "setup_firewall_include does not create include entry"
else
    echo "[FAIL] setup_firewall_include function not found"
    FAILURES=$((FAILURES + 1))
fi

echo ""
echo "--- setup_netbird_zone function exists ---"
NETBIRD_FN=$(extract_function "setup_netbird_zone" "$SETUP_SCRIPT")
if [ -n "$NETBIRD_FN" ]; then
    echo "[PASS] setup_netbird_zone function exists"
else
    echo "[FAIL] setup_netbird_zone function not found"
    FAILURES=$((FAILURES + 1))
fi

echo ""
echo "--- setup_netbird_zone creates zone ---"
if [ -n "$NETBIRD_FN" ]; then
    assert_contains "$NETBIRD_FN" "netbird_zone='zone'" "creates zone section"
    assert_contains "$NETBIRD_FN" "netbird_zone.name='netbird'" "zone name is netbird"
    assert_contains "$NETBIRD_FN" "add_list firewall.netbird_zone.device='wt0'" "zone device is wt0"
    assert_contains "$NETBIRD_FN" "netbird_zone.input='ACCEPT'" "zone input is ACCEPT"
    assert_contains "$NETBIRD_FN" "netbird_zone.output='ACCEPT'" "zone output is ACCEPT"
    assert_contains "$NETBIRD_FN" "netbird_zone.forward='REJECT'" "zone forward is REJECT"
else
    echo "[SKIP] netbird zone checks (function not found)"
    FAILURES=$((FAILURES + 6))
fi

echo ""
echo "--- setup_netbird_zone creates forwardings ---"
if [ -n "$NETBIRD_FN" ]; then
    assert_contains "$NETBIRD_FN" "netbird_lan_fwd='forwarding'" "creates lan forwarding section"
    assert_contains "$NETBIRD_FN" "netbird_lan_fwd.src='netbird'" "lan fwd src is netbird"
    assert_contains "$NETBIRD_FN" "netbird_lan_fwd.dest='lan'" "lan fwd dest is lan"
    assert_contains "$NETBIRD_FN" "netbird_private_fwd='forwarding'" "creates private forwarding section"
    assert_contains "$NETBIRD_FN" "netbird_private_fwd.src='netbird'" "private fwd src is netbird"
    assert_contains "$NETBIRD_FN" "netbird_private_fwd.dest='private'" "private fwd dest is private"
    assert_not_contains "$NETBIRD_FN" "dest='wan'" "no forwarding to wan"
else
    echo "[SKIP] forwarding checks (function not found)"
    FAILURES=$((FAILURES + 6))
fi

echo ""
echo "--- setup_netbird_zone is idempotent ---"
if [ -n "$NETBIRD_FN" ]; then
    assert_contains "$NETBIRD_FN" "uci -q get firewall.netbird_zone" "has idempotency guard (uci -q get)"
else
    echo "[SKIP] idempotency check (function not found)"
    FAILURES=$((FAILURES + 1))
fi

echo ""
echo "--- setup_netbird_zone is called in driver ---"
DRIVER=$(awk '/^# -- driver/,/^exit 0/' "$SETUP_SCRIPT")
assert_contains "$DRIVER" "setup_netbird_zone" "driver section calls setup_netbird_zone"

echo ""
echo "=== Summary ==="
if [ "$FAILURES" -eq 0 ]; then
    echo "All checks passed."
    exit 0
else
    echo "$FAILURES check(s) failed."
    exit 1
fi
