#!/bin/sh
# fake-ndsctl-filecounters — fake-ndsctl variant for the clientd scenario
# battery: same contract as fake-ndsctl.sh, but `json` reads live counters
# from /ndsctl-data/counters.env (DOWNLOADED_KB / UPLOADED_KB) so a scenario
# can advance data usage without restarting the container.
#
# The runner writes the file via:
#   docker exec tg-upstream-bytes sh -c \
#     "printf 'DOWNLOADED_KB=%s\nUPLOADED_KB=0\n' 1024 > /ndsctl-data/counters.env"
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
        if [ -f /ndsctl-data/counters.env ]; then
            . /ndsctl-data/counters.env
        fi
        downloaded="${DOWNLOADED_KB:-1024}"
        uploaded="${UPLOADED_KB:-512}"
        echo "{\"id\":1,\"ip\":\"172.28.0.20\",\"mac\":\"${mac}\",\"added\":$(date +%s),\"active\":$(date +%s),\"duration\":60,\"token\":\"fake-token\",\"state\":\"Authenticated\",\"downloaded\":${downloaded},\"avg_down_speed\":0,\"uploaded\":${uploaded},\"avg_up_speed\":0}"
        exit 0
        ;;
    *)
        echo "OK"
        exit 0
        ;;
esac
