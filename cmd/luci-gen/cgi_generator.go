package main

func generateCGI(_ *UISchema, _ map[string]*StructDef) string {
	return `#!/bin/sh

CONFIG="/etc/tollgate/config.json"
IDENTITIES="/etc/tollgate/identities.json"

json_header() {
	printf "Content-Type: application/json\r\n\r\n"
}

json_esc() {
	tr -d '\000-\010\013\014\016-\037' | awk '
	BEGIN { ORS=""; printf "\"" }
	{
		gsub(/\\/, "\\\\")
		gsub(/"/, "\\\"")
		gsub(/\t/, "\\t")
		gsub(/\r/, "\\r")
	}
	NR > 1 { printf "\\n" }
	{ printf "%s", $0 }
	END { printf "\"" }
	'
}

run_cmd() {
	"$@" 2>&1 || true
}

read_json_file() {
	cat "$1" 2>/dev/null || echo "{}"
}

get_logs() {
	logread 2>/dev/null | grep 'tollgate-wrt' | tail -n 300 || true
}

read_body() {
	if [ "${CONTENT_LENGTH:-0}" -gt 0 ] 2>/dev/null; then
		dd bs=1 count="$CONTENT_LENGTH" 2>/dev/null
	fi
}

validate_json_body() {
	printf '%s' "$1" | jsonfilter -q -e '$' >/dev/null 2>&1
}

save_file() {
	path="$1"
	label="$2"
	body="$3"

	if ! validate_json_body "$body"; then
		json_header
		printf '{"ok":false,"error":"Invalid JSON."}'
		exit 0
	fi

	ts=$(date +%Y%m%d-%H%M%S)
	bak="${path}.bak.${ts}"

	if [ -f "$path" ]; then
		cp "$path" "$bak" 2>/dev/null || {
			json_header
			printf '{"ok":false,"error":"Failed to create backup: %s"}' "$bak"
			exit 0
		}
	fi

	tmp="${path}.tmp.$$"
	printf '%s\n' "$body" > "$tmp" 2>/dev/null || {
		rm -f "$tmp" 2>/dev/null
		json_header
		printf '{"ok":false,"error":"Failed to write temporary file."}'
		exit 0
	}

	mv "$tmp" "$path" 2>/dev/null || {
		rm -f "$tmp" 2>/dev/null
		json_header
		printf '{"ok":false,"error":"Failed to replace file."}'
		exit 0
	}

	json_header
	printf '{"ok":true,"message":"Saved %s","backup":"%s"' "$label" "$bak"
	if [ "$path" = "$CONFIG" ]; then
		status=$(run_cmd /usr/bin/tollgate status)
		printf ',"status":'
		printf '%s\n' "$status" | json_esc
	fi
	printf '}'
	exit 0
}

action=""
qs="${QUERY_STRING:-}"
old_ifs="$IFS"
IFS="&"
for p in $qs; do
	k="${p%%=*}"
	v="${p#*=}"
	case "$k" in
		action) action="$v" ;;
	esac
done
IFS="$old_ifs"

body="$(read_body)"

case "$action" in
dashboard)
	wallet=$(run_cmd /usr/bin/tollgate wallet balance)
	version=$(run_cmd /usr/bin/tollgate version)
	status=$(run_cmd /usr/bin/tollgate status)
	logs=$(get_logs)
	json_header
	printf '{"ok":true,"wallet_balance":'
	printf '%s\n' "$wallet" | json_esc
	printf ',"version":'
	printf '%s\n' "$version" | json_esc
	printf ',"status":'
	printf '%s\n' "$status" | json_esc
	printf ',"logs":'
	printf '%s\n' "$logs" | json_esc
	printf '}'
	;;

files)
	config=$(read_json_file "$CONFIG")
	identities=$(read_json_file "$IDENTITIES")
	json_header
	printf '{"ok":true,"config":%s,"identities":%s}' "$config" "$identities"
	;;

validate_config|validate_identities)
	json_header
	if validate_json_body "$body"; then
		printf '{"ok":true}'
	else
		printf '{"ok":false,"error":"Invalid JSON."}'
	fi
	;;

save_config)
	save_file "$CONFIG" "config.json" "$body"
	;;

save_identities)
	save_file "$IDENTITIES" "identities.json" "$body"
	;;

*)
	json_header
	printf '{"ok":false,"error":"Unknown action: %s"}' "${action:-none}"
	;;
esac
`
}
