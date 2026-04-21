#!/bin/sh
# OpenWrt WiFi Scanner & Connector

WIRELESS_CFG="/etc/config/wireless"
NETWORK_CFG="/etc/config/network"
FIREWALL_CFG="/etc/config/firewall"
TMP_SCAN_DIR="/tmp/wifiscan_$$"

get_radios() {
	sed -n "s/.*config wifi-device '\(radio[0-9]*\)'.*/\1/p" "$WIRELESS_CFG" 2>/dev/null
}

wait_for_interface() {
	local radio="$1"
	local max_wait=15 count=0
	while [ $count -lt $max_wait ]; do
		if iwinfo "$radio" info 2>/dev/null | grep -q "ESSID\|no information"; then
			return 0
		fi
		count=$((count + 1))
		sleep 1
	done
	return 1
}

ensure_radios_enabled() {
	local radio changed=0
	for radio in $(get_radios); do
		local disabled
		disabled=$(uci -q get wireless."$radio".disabled)
		if [ "$disabled" = "1" ]; then
			uci set wireless."$radio".disabled=0
			changed=1
			echo "[*] Enabled radio: $radio"
		fi
	done
	if [ "$changed" = "1" ]; then
		uci commit wireless
		wifi up 2>/dev/null
		echo "[*] Radios enabled, waiting for interfaces to initialize..."
		local radio
		for radio in $(get_radios); do
			printf "  Waiting for %s..." "$radio"
			if wait_for_interface "$radio"; then
				echo " ready."
			else
				echo " timeout (continuing anyway)."
			fi
		done
		sleep 2
	fi
}

setup_wwan_network() {
	if uci -q get network.wwan > /dev/null 2>&1; then
		echo "[*] Network 'wwan' already exists."
		return 0
	fi
	echo "[*] Creating network interface 'wwan' (DHCP)..."
	uci set network.wwan=interface
	uci set network.wwan.proto=dhcp
	uci commit network
	/etc/init.d/network reload 2>/dev/null
	return 0
}

setup_wwan_firewall() {
	local wan_zone
	wan_zone=$(uci -q show firewall | sed -n 's/firewall\.\([^=]*\)=zone/\1/p' | while read -r z; do
		local name
		name=$(uci -q get firewall."$z".name)
		if [ "$name" = "wan" ]; then
			echo "$z"
			break
		fi
	done)

	if [ -z "$wan_zone" ]; then
		echo "[!] Could not find 'wan' firewall zone."
		return 1
	fi

	local networks
	networks=$(uci -q get firewall."$wan_zone".network)
	case " $networks " in
		*" wwan "*)
			echo "[*] Firewall zone 'wan' already includes 'wwan'."
			return 0
			;;
	esac

	echo "[*] Adding 'wwan' to firewall zone 'wan'..."
	uci add_list firewall."$wan_zone".network=wwan
	uci commit firewall
	/etc/init.d/firewall reload 2>/dev/null
	return 0
}

scan_radio() {
	local radio="$1"
	local outfile="$TMP_SCAN_DIR/${radio}.raw"
	local errfile="$TMP_SCAN_DIR/${radio}.err"
	local retry=0 max_retry=3

	while [ $retry -lt $max_retry ]; do
		iwinfo "$radio" scan > "$outfile" 2>"$errfile"
		local scan_err
		scan_err=$(cat "$errfile" 2>/dev/null)
		if [ -s "$outfile" ] && ! grep -qi "no scan result\|not associated\|failed" "$outfile"; then
			return 0
		fi
		if [ -n "$scan_err" ]; then
			printf "    Retry %d/%d for %s (%s)\n" $((retry + 1)) "$max_retry" "$radio" "$scan_err"
		fi
		retry=$((retry + 1))
		sleep 2
	done

	printf "  [!] No results from %s after %d attempts.\n" "$radio" "$max_retry"
	return 1
}

parse_scan_file() {
	local radio="$1"
	local infile="$TMP_SCAN_DIR/${radio}.raw"
	[ -f "$infile" ] || return 0

	local ssid="" signal="" encrypt="" channel="" bssid=""

	while IFS= read -r line; do
		line=$(echo "$line" | sed 's/^[[:space:]]*//')
		case "$line" in
			*Address:*)
				bssid=$(echo "$line" | awk '{print $NF}')
				;;
			ESSID:*)
				ssid=$(echo "$line" | sed 's/.*ESSID: *"//' | sed 's/"$//')
				[ -z "$ssid" ] && ssid="(hidden)"
				;;
			*Signal:*)
				signal=$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i=="Signal:") {print $(i+1); break}}')
				;;
			*Encryption:*)
				encrypt=$(echo "$line" | sed 's/.*Encryption: *//')
				;;
			*Channel:*)
				channel=$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i=="Channel:") {print $(i+1); break}}')
				;;
			"")
				if [ -n "$ssid" ] && [ -n "$signal" ]; then
					printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
						"$signal" "$ssid" "$encrypt" "$channel" "$bssid" "$radio"
					ssid="" signal="" encrypt="" channel="" bssid=""
				fi
				;;
		esac
	done < "$infile"
}

scan_all_radios() {
	rm -rf "$TMP_SCAN_DIR"
	mkdir -p "$TMP_SCAN_DIR"

	local radio
	for radio in $(get_radios); do
		printf "[*] Scanning on %s...\n" "$radio"
		scan_radio "$radio" &
	done
	wait

	local all_results="$TMP_SCAN_DIR/all.tsv"
	> "$all_results"

	for radio in $(get_radios); do
		parse_scan_file "$radio" >> "$all_results"
	done

	if [ ! -s "$all_results" ]; then
		echo "[!] No networks found."
		rm -rf "$TMP_SCAN_DIR"
		return 1
	fi

	sort -t"	" -k1 -n "$all_results" > "$TMP_SCAN_DIR/sorted.tsv"
}

show_networks() {
	ensure_radios_enabled
	if ! scan_all_radios; then
		return 1
	fi

	printf "\n%-4s  %-30s  %-8s  %-5s  %-10s  %s\n" \
		"#   " "SSID" "Signal" "Ch" "Encryption" "Radio"
	local i=80
	while [ $i -gt 0 ]; do printf '-'; i=$((i - 1)); done
	printf '\n'

	local idx=1
	local line signal ssid encrypt channel bssid radio

	while IFS="	" read -r signal ssid encrypt channel bssid radio; do
		uci_enc=$(detect_encryption "$encrypt")
		printf "%-4s  %-30s  %-8s  %-5s  %-10s  %s\n" \
			"$idx." "$ssid" "$signal dBm" "$channel" "$uci_enc" "$radio"
		idx=$((idx + 1))
	done < "$TMP_SCAN_DIR/sorted.tsv"

	printf "\nTotal: %d network(s) found.\n" $((idx - 1))
	printf "Usage: %s connect <SSID> [PASSPHRASE]\n\n" "$0"

	rm -rf "$TMP_SCAN_DIR"
}

detect_encryption() {
	case "$1" in
		none|None|NONE|open|Open|"WEP"*)
			echo "none"
			;;
		*"WPA3 SAE"*|*"SAE"*"mixed"*)
			if echo "$1" | grep -qi "mixed\|WPA2"; then
				echo "sae-mixed"
			else
				echo "sae"
			fi
			;;
		*"WPA2"*"PSK"*)
			echo "psk2"
			;;
		*"WPA"*"PSK"*)
			echo "psk"
			;;
		*"WPA2"*"EAP"*|*"WPA3"*"EAP"*|*"WPA"*"EAP"*)
			echo "wpa2-eap"
			;;
		*)
			echo "psk2"
			;;
	esac
}

find_best_radio_for_ssid() {
	local target_ssid="$1"
	[ -f "$TMP_SCAN_DIR/sorted.tsv" ] || return 1

	while IFS="	" read -r signal ssid encrypt channel bssid radio; do
		if [ "$ssid" = "$target_ssid" ]; then
			echo "$radio"
			return 0
		fi
	done < "$TMP_SCAN_DIR/sorted.tsv"

	return 1
}

find_sta_interface() {
	local radio="$1"
	local radio_num
	radio_num=$(echo "$radio" | sed 's/radio//')

	local iface
	for iface in /sys/class/net/*; do
		local name
		name=$(basename "$iface")
		case "$name" in
			*"sta"*|*"wlan"*)
				local phy_num
				phy_num=$(cat "$iface/phy80211/index" 2>/dev/null || readlink "$iface/device" 2>/dev/null | grep -o 'phy[0-9]*' | tr -d 'phy')
				if [ "$phy_num" = "$radio_num" ]; then
					echo "$name"
					return 0
				fi
				;;
		esac
	done

	return 1
}

wait_for_sta_ip() {
	local radio="$1"
	local max_wait=30 count=0

	while [ $count -lt $max_wait ]; do
		local sta_iface
		sta_iface=$(find_sta_interface "$radio")
		if [ -n "$sta_iface" ]; then
			local ip
			ip=$(ifconfig "$sta_iface" 2>/dev/null | grep "inet addr" | sed 's/.*inet addr:\([^ ]*\).*/\1/')
			if [ -n "$ip" ]; then
				echo "$sta_iface"
				return 0
			fi
		fi
		sleep 1
		count=$((count + 1))
	done

	return 1
}

connect_ssid() {
	local target_ssid="$1"
	local passphrase="$2"

	ensure_radios_enabled

	if ! scan_all_radios; then
		echo "[!] Cannot find any networks. Check your radios."
		return 1
	fi

	local radio
	radio=$(find_best_radio_for_ssid "$target_ssid")
	if [ -z "$radio" ]; then
		echo "[!] SSID '$target_ssid' not found in scan."
		rm -rf "$TMP_SCAN_DIR"
		return 1
	fi

	local encrypt_str=""
	local uci_enc=""
	while IFS="	" read -r signal ssid encrypt channel bssid r; do
		if [ "$ssid" = "$target_ssid" ]; then
			encrypt_str="$encrypt"
			uci_enc=$(detect_encryption "$encrypt")
			break
		fi
	done < "$TMP_SCAN_DIR/sorted.tsv"

	echo "[*] Found '$target_ssid' on $radio (encryption: $uci_enc)"

	if [ "$uci_enc" = "none" ]; then
		passphrase=""
	else
		if [ -z "$passphrase" ]; then
			printf "Passphrase: "
			read -r -s passphrase
			printf "\n"
			if [ -z "$passphrase" ]; then
				echo "[!] Passphrase is required for encrypted networks."
				rm -rf "$TMP_SCAN_DIR"
				return 1
			fi
		fi
	fi

	setup_wwan_network
	setup_wwan_firewall

	cp "$WIRELESS_CFG" "${WIRELESS_CFG}.bak.$(date +%Y%m%d%H%M%S)"

	local iface="default_${radio}"
	if ! uci -q get wireless."$iface" > /dev/null 2>&1; then
		echo "[*] Creating new wireless interface for $radio."
		uci add wireless wifi-iface
		iface=$(uci show wireless | grep "=wifi-iface" | tail -1 | sed 's/wireless\.//;s/=wifi-iface//')
	fi

	uci set wireless."$iface".device="$radio"
	uci set wireless."$iface".network="wwan"
	uci set wireless."$iface".mode="sta"
	uci set wireless."$iface".ssid="$target_ssid"
	uci set wireless."$iface".encryption="$uci_enc"

	if [ -n "$passphrase" ]; then
		uci set wireless."$iface".key="$passphrase"
	else
		uci -q delete wireless."$iface".key
	fi

	uci commit wireless
	wifi reload 2>/dev/null

	echo "[*] Connecting to '$target_ssid' via $radio (interface: wwan)..."

	local sta_iface
	sta_iface=$(wait_for_sta_ip "$radio")
	if [ -n "$sta_iface" ]; then
		local ip
		ip=$(ifconfig "$sta_iface" 2>/dev/null | grep "inet addr" | sed 's/.*inet addr:\([^ ]*\).*/\1/')
		echo "[+] Connected to '$target_ssid' on $sta_iface (IP: $ip)"

		echo "[*] Refreshing DNS and firewall..."
		/etc/init.d/dnsmasq restart 2>/dev/null
		/etc/init.d/firewall restart 2>/dev/null

		local gw
		gw=$(ip route 2>/dev/null | grep default | head -1 | awk '{print $3}')
		if [ -n "$gw" ]; then
			echo "[+] Default gateway: $gw"
		else
			echo "[!] No default gateway set. Run: ip route"
		fi
	else
		echo "[!] Timed out waiting for STA interface to get an IP."
		echo "[*] Check status with: ifconfig -a | grep sta"
		echo "[*] Check log with: logread | grep -i netifd"
	fi

	rm -rf "$TMP_SCAN_DIR"
}

usage() {
	printf "Usage: %s [command] [args]\n\n" "$0"
	printf "Commands:\n"
	printf "  (no args)              Scan and list available networks\n"
	printf "  connect <SSID>         Connect to an SSID (prompts for passphrase)\n"
	printf "  connect <SSID> <PASS>  Connect to an SSID with given passphrase\n"
	printf "  help                   Show this help\n"
}

main() {
	case "${1:-}" in
		connect)
			[ -z "$2" ] && echo "[!] Usage: $0 connect <SSID> [PASSPHRASE]" && exit 1
			connect_ssid "$2" "$3"
			;;
		help|--help|-h)
			usage
			;;
		"")
			show_networks
			;;
		*)
			echo "[!] Unknown command: $1"
			usage
			exit 1
			;;
	esac
}

main "$@"
