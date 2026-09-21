#!/bin/sh
# fake-ndsctl — Drop-in replacement for ndsctl in cloud-lab Docker containers.
#
# The real ndsctl talks to NoDogSplash's opennds daemon to authorize/deauthorize
# MAC addresses and fetch client stats. In the cloud lab there is no NoDogSplash,
# so this script simulates successful responses.
#
# It also maintains a simple log so tests can assert that gate open/close
# calls actually happened.
#
# Usage: ndsctl <command> [mac_address]
#   auth <mac>    — Authorize a MAC (always succeeds)
#   deauth <mac>  — Deauthorize a MAC (always succeeds)
#   json <mac>    — Return fake client stats JSON (for data-based sessions)
#   anything else — Return success

LOG_FILE="${NDSCTL_LOG:-/tmp/ndsctl.log}"

mac="$2"
timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

case "$1" in
    auth)
        echo "${timestamp} AUTH ${mac}" >> "$LOG_FILE"
        echo "Auth: ${mac} - Granted"
        exit 0
        ;;
    deauth)
        echo "${timestamp} DEAUTH ${mac}" >> "$LOG_FILE"
        echo "Auth: ${mac} - Removed"
        exit 0
        ;;
    json)
        # Return fake client stats for data tracking.
        # Downloaded/Uploaded are in KB (matching real ndsctl format).
        # The valve multiplies by 1024 to get bytes.
        #
        # Usage can be driven from tests: when /tmp/fake-nds-usage exists
        # and holds "<downloaded_kb> <uploaded_kb>", those values win over
        # the static defaults, so a test can advance a data-metered session
        # past its renewal threshold by writing to the file.
        if [ -r /tmp/fake-nds-usage ] && [ "$(wc -w < /tmp/fake-nds-usage)" -eq 2 ]; then
            downloaded=$(awk '{print $1}' /tmp/fake-nds-usage)
            uploaded=$(awk '{print $2}' /tmp/fake-nds-usage)
        else
            downloaded="${FAKE_NDS_DOWNLOADED_KB:-1024}"
            uploaded="${FAKE_NDS_UPLOADED_KB:-512}"
        fi
        echo "{\"id\":1,\"ip\":\"172.28.0.20\",\"mac\":\"${mac}\",\"added\":$(date +%s),\"active\":$(date +%s),\"duration\":60,\"token\":\"fake-token\",\"state\":\"Authenticated\",\"downloaded\":${downloaded},\"avg_down_speed\":0,\"uploaded\":${uploaded},\"avg_up_speed\":0}"
        exit 0
        ;;
    *)
        # NoDogSplash responds with generic text for unknown commands
        echo "OK"
        exit 0
        ;;
esac
