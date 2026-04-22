#!/bin/sh

WIRELESS_CFG="/etc/config/wireless"
TMP_SCAN_DIR="/tmp/upstream-daemon"
INTERVAL="${UPSTREAM_SCAN_INTERVAL:-300}"
HYSTERESIS_DB="${UPSTREAM_HYSTERESIS_DB:-12}"
SIGNAL_FLOOR="${UPSTREAM_SIGNAL_FLOOR:--85}"
FAST_CHECK="${UPSTREAM_FAST_CHECK:-30}"
LOST_THRESHOLD="${UPSTREAM_LOST_THRESHOLD:-2}"

log() {
	logger -t upstream-daemon "$1"
}

get_radios() {
	sed -n "s/.*config wifi-device '\(radio[0-9]*\)'.*/\1/p" "$WIRELESS_CFG" 2>/dev/null
}

ensure_radios_enabled() {
	local radio changed=0
	for radio in $(get_radios); do
		local disabled
		disabled=$(uci -q get wireless."$radio".disabled)
		if [ "$disabled" = "1" ]; then
			uci set wireless."$radio".disabled=0
			changed=1
			log "Enabled radio: $radio"
		fi
	done
	if [ "$changed" = "1" ]; then
		uci commit wireless
		wifi up 2>/dev/null
		sleep 5
	fi
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
		retry=$((retry + 1))
		sleep 2
	done
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
				[ -z "$ssid" ] && continue
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
		scan_radio "$radio" &
	done
	wait

	local all_results="$TMP_SCAN_DIR/all.tsv"
	> "$all_results"

	for radio in $(get_radios); do
		parse_scan_file "$radio" >> "$all_results"
	done

	if [ ! -s "$all_results" ]; then
		rm -rf "$TMP_SCAN_DIR"
		return 1
	fi

	sort -t"	" -k1 -rn "$all_results" > "$TMP_SCAN_DIR/sorted.tsv"
}

get_sta_sections() {
	uci show wireless 2>/dev/null | \
		sed -n 's/wireless\.\([^.]*\)=wifi-iface/\1/p' | while read -r iface; do
		[ "$(uci -q get wireless."$iface".mode)" = "sta" ] && echo "$iface"
	done
}

get_active_sta() {
	local iface
	for iface in $(get_sta_sections); do
		[ "$(uci -q get wireless."$iface".disabled)" != "1" ] && echo "$iface" && return 0
	done
	return 1
}

get_sta_ssid() {
	uci -q get wireless."$1".ssid
}

get_sta_radio() {
	uci -q get wireless."$1".device
}

find_sta_iface_for_radio() {
	local radio="$1"
	local radio_num
	radio_num=$(echo "$radio" | sed 's/radio//')

	local iface name
	for iface in /sys/class/net/*; do
		name=$(basename "$iface")
		case "$name" in
			*"sta"*)
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

get_current_signal() {
	local sta_iface="$1"
	local signal

	signal=$(iwinfo "$sta_iface" assoclist 2>/dev/null | head -1 | awk '{print $2}')
	if [ -n "$signal" ] && echo "$signal" | grep -qE '^-?[0-9]+$'; then
		echo "$signal"
		return 0
	fi

	signal=$(iwinfo "$sta_iface" info 2>/dev/null | grep "Signal:" | head -1 | awk -F'[ /]' '{for(i=1;i<=NF;i++) if($i=="Signal:") {print $(i+1); break}}')
	if [ -n "$signal" ] && echo "$signal" | grep -qE '^-?[0-9]+$'; then
		echo "$signal"
		return 0
	fi

	return 1
}

is_sta_associated() {
	local sta_iface="$1"
	iwinfo "$sta_iface" info 2>/dev/null | grep -q "Access Point:\|Associated with"
}

find_strongest_candidate() {
	local sorted_file="$TMP_SCAN_DIR/sorted.tsv"
	[ -f "$sorted_file" ] || return 1

	local known_ssids=""
	local iface
	for iface in $(get_sta_sections); do
		if [ "$(uci -q get wireless."$iface".disabled)" = "1" ]; then
			local ssid
			ssid=$(get_sta_ssid "$iface")
			[ -n "$ssid" ] && known_ssids="${known_ssids}${ssid}
"
		fi
	done

	[ -z "$known_ssids" ] && return 1

	local best_signal="-999" best_radio="" best_iface="" best_ssid=""
	while IFS="	" read -r signal ssid encrypt channel bssid radio; do
		case "$known_ssids" in
			*"$ssid"*)
				if [ "$signal" -gt "$best_signal" ]; then
					best_signal="$signal"
					best_radio="$radio"
					best_ssid="$ssid"
					for iface in $(get_sta_sections); do
						if [ "$(get_sta_ssid "$iface")" = "$ssid" ] && [ "$(uci -q get wireless."$iface".disabled)" = "1" ]; then
							best_iface="$iface"
							break
						fi
					done
				fi
				;;
		esac
	done < "$sorted_file"

	if [ -n "$best_iface" ]; then
		printf '%s\t%s\t%s\t%s\n' "$best_signal" "$best_radio" "$best_iface" "$best_ssid"
		return 0
	fi
	return 1
}

wait_for_sta_ip() {
	local radio="$1"
	local radio_num
	radio_num=$(echo "$radio" | sed 's/radio//')
	local max_wait=30 count=0

	while [ $count -lt $max_wait ]; do
		local iface
		for iface in /sys/class/net/*; do
			local name
			name=$(basename "$iface")
			case "$name" in
				*"sta"*|*"wlan"*)
					local phy_num
					phy_num=$(cat "$iface/phy80211/index" 2>/dev/null || readlink "$iface/device" 2>/dev/null | grep -o 'phy[0-9]*' | tr -d 'phy')
					if [ "$phy_num" = "$radio_num" ]; then
						local ip
						ip=$(ifconfig "$name" 2>/dev/null | grep "inet addr" | sed 's/.*inet addr:\([^ ]*\).*/\1/')
						if [ -n "$ip" ]; then
							echo "$name"
							return 0
						fi
					fi
					;;
			esac
		done
		sleep 1
		count=$((count + 1))
	done
	return 1
}

ensure_wwan_setup() {
	if ! uci -q get network.wwan > /dev/null 2>&1; then
		uci set network.wwan=interface
		uci set network.wwan.proto=dhcp
		uci commit network
		/etc/init.d/network reload 2>/dev/null
		log "Created wwan network interface (DHCP)"
	fi

	local wan_zone
	wan_zone=$(uci -q show firewall | sed -n 's/firewall\.\([^=]*\)=zone/\1/p' | while read -r z; do
		[ "$(uci -q get firewall."$z".name)" = "wan" ] && echo "$z" && break
	done)

	if [ -n "$wan_zone" ]; then
		local networks
		networks=$(uci -q get firewall."$wan_zone".network)
		case " $networks " in
			*" wwan "*) ;;
			*)
				uci add_list firewall."$wan_zone".network=wwan
				uci commit firewall
				/etc/init.d/firewall reload 2>/dev/null
				log "Added wwan to wan firewall zone"
				;;
		esac
	fi
}

switch_upstream() {
	local active_iface="$1"
	local cand_iface="$2"
	local cand_ssid="$3"
	local reason="$4"

	log "Switching upstream ($reason): ${active_iface:-none} -> $cand_iface ($cand_ssid)"

	if [ -n "$active_iface" ]; then
		uci set wireless."$active_iface".disabled=1
	fi

	uci set wireless."$cand_iface".disabled=0
	uci commit wireless
	wifi reload 2>/dev/null

	local radio
	radio=$(get_sta_radio "$cand_iface")
	if [ -z "$radio" ]; then
		log "ERROR: no radio for $cand_iface"
		return 1
	fi

	local sta_iface
	sta_iface=$(wait_for_sta_ip "$radio")
	if [ -n "$sta_iface" ]; then
		local ip
		ip=$(ifconfig "$sta_iface" 2>/dev/null | grep "inet addr" | sed 's/.*inet addr:\([^ ]*\).*/\1/')
		log "Connected to $cand_ssid on $sta_iface (IP: ${ip:-pending})"
		/etc/init.d/dnsmasq restart 2>/dev/null
		/etc/init.d/firewall restart 2>/dev/null
		return 0
	else
		log "Timed out waiting for IP on $cand_ssid, re-enabling previous upstream"
		if [ -n "$active_iface" ]; then
			uci set wireless."$active_iface".disabled=0
			uci set wireless."$cand_iface".disabled=1
			uci commit wireless
			wifi reload 2>/dev/null
		fi
		return 1
	fi
}

check_connectivity() {
	local sta_iface="$1"

	iwinfo "$sta_iface" info 2>/dev/null | grep -q "Access Point:\|Associated with" || return 1

	local gw
	gw=$(ip route 2>/dev/null | grep default | head -1 | awk '{print $3}')
	[ -z "$gw" ] && return 1

	ping -c 1 -W 2 "$gw" >/dev/null 2>&1
}

run_scan_cycle() {
	local active_iface="$1"
	local active_ssid="$2"
	local current_signal="$3"
	local reason="$4"

	if scan_all_radios; then
		local candidate_line
		candidate_line=$(find_strongest_candidate)

		if [ -n "$candidate_line" ]; then
			local cand_signal cand_radio cand_iface cand_ssid
			cand_signal=$(echo "$candidate_line" | cut -f1)
			cand_radio=$(echo "$candidate_line" | cut -f2)
			cand_iface=$(echo "$candidate_line" | cut -f3)
			cand_ssid=$(echo "$candidate_line" | cut -f4)

			log "Best candidate: $cand_ssid ($cand_signal dBm)"

			local should_switch=0

			if [ -z "$active_iface" ]; then
				should_switch=1
				log "$reason: no active upstream, connecting"
			elif [ -z "$current_signal" ]; then
				should_switch=1
				log "$reason: active upstream not associated"
			elif [ "$current_signal" -lt "$SIGNAL_FLOOR" ]; then
				should_switch=1
				log "$reason: active signal ${current_signal}dBm below floor ${SIGNAL_FLOOR}dBm"
			else
				local diff
				diff=$((cand_signal - current_signal))
				if [ "$diff" -ge "$HYSTERESIS_DB" ]; then
					should_switch=1
					log "$reason: candidate +${diff}dB stronger"
				fi
			fi

			if [ "$should_switch" = "1" ]; then
				switch_upstream "$active_iface" "$cand_iface" "$cand_ssid" "$reason"
			fi
		else
			log "$reason: no known upstream candidates available"
		fi

		rm -rf "$TMP_SCAN_DIR"
	else
		log "$reason: scan failed, retrying next cycle"
	fi
}

main() {
	log "Starting (interval=${INTERVAL}s, fast_check=${FAST_CHECK}s, lost_threshold=${LOST_THRESHOLD}, hysteresis=${HYSTERESIS_DB}dB, floor=${SIGNAL_FLOOR}dBm)"

	ensure_radios_enabled
	ensure_wwan_setup

	sleep 10

	scan_counter=0
	lost_count=0
	scan_cycles=$((INTERVAL / FAST_CHECK))

	while true; do
		ensure_radios_enabled

		active_iface=$(get_active_sta)
		active_radio=""
		active_sta_dev=""
		current_signal=""
		active_ssid=""

		if [ -n "$active_iface" ]; then
			active_radio=$(get_sta_radio "$active_iface")
			active_ssid=$(get_sta_ssid "$active_iface")
			if [ -n "$active_radio" ]; then
				active_sta_dev=$(find_sta_iface_for_radio "$active_radio")
			fi
		fi

		should_scan=0
		reason="scheduled"

		if [ -n "$active_sta_dev" ]; then
			if check_connectivity "$active_sta_dev"; then
				if [ "$lost_count" -gt 0 ]; then
					log "Connectivity restored after $lost_count check(s)"
				fi
				lost_count=0
				scan_counter=$((scan_counter + 1))
				if [ "$scan_counter" -ge "$scan_cycles" ]; then
					should_scan=1
					reason="scheduled"
				fi
			else
				lost_count=$((lost_count + 1))
				log "Connectivity lost ($lost_count/$LOST_THRESHOLD)"
				if [ "$lost_count" -ge "$LOST_THRESHOLD" ]; then
					should_scan=1
					reason="emergency"
				fi
			fi
		else
			should_scan=1
			reason="no-active-upstream"
		fi

		if [ "$should_scan" = "1" ]; then
			if [ -n "$active_sta_dev" ] && is_sta_associated "$active_sta_dev"; then
				current_signal=$(get_current_signal "$active_sta_dev")
			fi

			log "Active: ${active_ssid:-none} signal=${current_signal:-N/A}dBm"

			run_scan_cycle "$active_iface" "$active_ssid" "$current_signal" "$reason"
			scan_counter=0
			lost_count=0
		fi

		sleep "$FAST_CHECK"
	done
}

main "$@"
