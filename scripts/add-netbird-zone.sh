#!/bin/sh

GREEN='\033[32m'
RED='\033[31m'
BOLD='\033[1m'
RESET='\033[0m'

echo "${BOLD}=== Adding netbird firewall zone ===${RESET}"

uci set firewall.netbird_zone=zone
uci set firewall.netbird_zone.name='netbird'
uci add_list firewall.netbird_zone.device='wt0'
uci set firewall.netbird_zone.input='ACCEPT'
uci set firewall.netbird_zone.output='ACCEPT'
uci set firewall.netbird_zone.forward='REJECT'

uci set firewall.netbird_lan_fwd=forwarding
uci set firewall.netbird_lan_fwd.src='netbird'
uci set firewall.netbird_lan_fwd.dest='lan'

uci set firewall.netbird_private_fwd=forwarding
uci set firewall.netbird_private_fwd.src='netbird'
uci set firewall.netbird_private_fwd.dest='private'

uci commit firewall

echo "${BOLD}=== Restarting firewall ===${RESET}"
/etc/init.d/firewall restart

echo ""
echo "${BOLD}=== Verification ===${RESET}"

FAIL=0

echo "Checking netbird zone..."
uci show firewall.netbird_zone >/dev/null 2>&1 && echo "${GREEN}[PASS]${RESET} netbird_zone exists" || { echo "${RED}[FAIL]${RESET} netbird_zone not found"; FAIL=1; }

echo "Checking netbird→lan forwarding..."
uci show firewall.netbird_lan_fwd >/dev/null 2>&1 && echo "${GREEN}[PASS]${RESET} netbird_lan_fwd exists" || { echo "${RED}[FAIL]${RESET} netbird_lan_fwd not found"; FAIL=1; }

echo "Checking netbird→private forwarding..."
uci show firewall.netbird_private_fwd >/dev/null 2>&1 && echo "${GREEN}[PASS]${RESET} netbird_private_fwd exists" || { echo "${RED}[FAIL]${RESET} netbird_private_fwd not found"; FAIL=1; }

echo ""
echo "Checking UCI values..."
_zname=$(uci get firewall.netbird_zone.name 2>/dev/null)
_zinput=$(uci get firewall.netbird_zone.input 2>/dev/null)
_zoutput=$(uci get firewall.netbird_zone.output 2>/dev/null)
_zforward=$(uci get firewall.netbird_zone.forward 2>/dev/null)
_zdevice=$(uci get firewall.netbird_zone.device 2>/dev/null)
_flan_src=$(uci get firewall.netbird_lan_fwd.src 2>/dev/null)
_flan_dest=$(uci get firewall.netbird_lan_fwd.dest 2>/dev/null)
_fpriv_src=$(uci get firewall.netbird_private_fwd.src 2>/dev/null)
_fpriv_dest=$(uci get firewall.netbird_private_fwd.dest 2>/dev/null)

check_val() {
    _expected="$1"
    _actual="$2"
    _label="$3"
    if [ "$_actual" = "$_expected" ]; then
        echo "${GREEN}[PASS]${RESET} $_label = '$_actual'"
    else
        echo "${RED}[FAIL]${RESET} $_label expected '$_expected', got '$_actual'"
        FAIL=1
    fi
}

check_val "netbird" "$_zname" "zone name"
check_val "ACCEPT" "$_zinput" "zone input"
check_val "ACCEPT" "$_zoutput" "zone output"
check_val "REJECT" "$_zforward" "zone forward"
check_val "wt0" "$_zdevice" "zone device"
check_val "netbird" "$_flan_src" "fwd lan src"
check_val "lan" "$_flan_dest" "fwd lan dest"
check_val "netbird" "$_fpriv_src" "fwd private src"
check_val "private" "$_fpriv_dest" "fwd private dest"

echo ""
echo "Checking fw4 ruleset..."
_fw4_output=$(fw4 print 2>&1)
if echo "$_fw4_output" | grep -q "netbird"; then
    echo "${GREEN}[PASS]${RESET} netbird zone appears in fw4 ruleset"
    echo "$_fw4_output" | grep -A3 "netbird"
else
    echo "${RED}[FAIL]${RESET} netbird zone NOT in fw4 ruleset"
    FAIL=1
fi

echo ""
echo "Checking wt0 interface..."
if ip addr show wt0 >/dev/null 2>&1; then
    _wt0_ip=$(ip -4 addr show wt0 | grep -oE 'inet [0-9.]+')
    echo "${GREEN}[PASS]${RESET} wt0 interface exists: $_wt0_ip"
else
    echo "${RED}[FAIL]${RESET} wt0 interface not found (Netbird may not be running)"
    FAIL=1
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
    echo "${BOLD}${GREEN}All checks passed. Try SSH from your VPS now.${RESET}"
else
    echo "${BOLD}${RED}$FAIL check(s) failed. Review errors above.${RESET}"
fi

exit $FAIL
