#!/bin/sh

FILE="${1:-files/etc/config/firewall-tollgate}"
FAILURES=0

if [ ! -f "$FILE" ]; then
    echo "FAIL: config file not found: $FILE"
    exit 1
fi

assert_option() {
    _block="$1"
    _key="$2"
    _val="$3"
    _desc="$4"
    _found=$(echo "$_block" | awk -v k="$_key" -v v="$_val" '
        /^[ \t]*(option|list)[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) {
                if (parts[i] == v) {
                    line = $0
                    gsub(/^[ \t]+/, "", line)
                    split(line, kv, /[ \t]+/)
                    if (kv[2] == k) { print "yes"; exit }
                }
            }
        }
    ')
    if [ "$_found" = "yes" ]; then
        echo "[PASS] $_desc"
    else
        echo "[FAIL] $_desc — expected ${_key} '${_val}' not found"
        FAILURES=$((FAILURES + 1))
    fi
}

get_first_block() {
    _type="$1"
    awk -v t="$_type" '
        /^config[ \t]/ {
            if (collect) { done = 1; exit }
            split($0, a)
            if (a[2] == t) { collect = 1 }
            next
        }
        collect && /^[ \t]/ { buf = buf $0 "\n" }
        END { if (done || collect) printf "%s", buf }
    ' "$FILE"
}

get_zone_block() {
    _zname="$1"
    awk -v zn="$_zname" '
        /^config[ \t]/ {
            if (in_z && match_z) { saved = buf; done = 1 }
            in_z = 0; buf = ""; match_z = 0
            if ($0 ~ /^config[ \t]+zone/) in_z = 1
            next
        }
        in_z && /^[ \t]*option[ \t]+name[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) { if (parts[i] == zn) match_z = 1 }
        }
        in_z && /^[ \t]/ { buf = buf $0 "\n" }
        END { if (done) printf "%s", saved; else if (in_z && match_z) printf "%s", buf }
    ' "$FILE"
}

check_forwarding() {
    _src="$1"
    _dest="$2"
    _desc="$3"
    _found=$(awk -v s="$_src" -v d="$_dest" '
        /^config[ \t]/ {
            if (in_f && found_s && found_d) { result = 1; exit }
            in_f = 0; found_s = 0; found_d = 0
            if ($0 ~ /^config[ \t]+forwarding/) in_f = 1
            next
        }
        in_f && /^[ \t]*option[ \t]+src[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) { if (parts[i] == s) found_s = 1 }
        }
        in_f && /^[ \t]*option[ \t]+dest[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) { if (parts[i] == d) found_d = 1 }
        }
        END { if (result) print "found"; else if (in_f && found_s && found_d) print "found" }
    ' "$FILE")
    if [ "$_found" = "found" ]; then
        echo "[PASS] $_desc"
    else
        echo "[FAIL] $_desc"
        FAILURES=$((FAILURES + 1))
    fi
}

check_no_forwarding() {
    _src="$1"
    _dest="$2"
    _desc="$3"
    _found=$(awk -v s="$_src" -v d="$_dest" '
        /^config[ \t]/ {
            if (in_f && found_s && found_d) { result = 1; exit }
            in_f = 0; found_s = 0; found_d = 0
            if ($0 ~ /^config[ \t]+forwarding/) in_f = 1
            next
        }
        in_f && /^[ \t]*option[ \t]+src[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) { if (parts[i] == s) found_s = 1 }
        }
        in_f && /^[ \t]*option[ \t]+dest[ \t]/ {
            split($0, parts, /'\''/)
            for (i in parts) { if (parts[i] == d) found_d = 1 }
        }
        END { if (result) print "found"; else if (in_f && found_s && found_d) print "found" }
    ' "$FILE")
    if [ -z "$_found" ]; then
        echo "[PASS] $_desc"
    else
        echo "[FAIL] $_desc — forwarding from '$_src' to '$_dest' should not exist"
        FAILURES=$((FAILURES + 1))
    fi
}

echo "=== Testing: $FILE ==="
echo ""

echo "--- Existing rule (regression guard) ---"
_rule_block=$(get_first_block "rule")
assert_option "$_rule_block" "name" "Allow-TollGate-In" "Allow-TollGate-In rule: name"
assert_option "$_rule_block" "src" "lan" "Allow-TollGate-In rule: src"
assert_option "$_rule_block" "proto" "tcp" "Allow-TollGate-In rule: proto"
assert_option "$_rule_block" "dest_port" "2121" "Allow-TollGate-In rule: dest_port"
assert_option "$_rule_block" "target" "ACCEPT" "Allow-TollGate-In rule: target"

echo ""
echo "--- Netbird zone ---"
_zone_block=$(get_zone_block "netbird")
assert_option "$_zone_block" "name" "netbird" "Netbird zone: name"
assert_option "$_zone_block" "device" "wt0" "Netbird zone: device wt0"
assert_option "$_zone_block" "input" "ACCEPT" "Netbird zone: input"
assert_option "$_zone_block" "output" "ACCEPT" "Netbird zone: output"
assert_option "$_zone_block" "forward" "REJECT" "Netbird zone: forward"

echo ""
echo "--- Netbird forwardings ---"
check_forwarding "netbird" "lan" "Forwarding netbird -> lan exists"
check_forwarding "netbird" "private" "Forwarding netbird -> private exists"
check_no_forwarding "netbird" "wan" "No forwarding netbird -> wan"

echo ""
echo "=== Summary ==="
if [ "$FAILURES" -eq 0 ]; then
    echo "All checks passed."
    exit 0
else
    echo "$FAILURES check(s) failed."
    exit 1
fi
