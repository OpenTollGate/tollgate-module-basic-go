# The open AP's SSID is written into the beacon of every router that takes the
# default, and it is the name clients and the operator match on — keep it the
# contract shape (`<brand>-<4 hex chars>`) so a band suffix or a fixed literal
# cannot come back. Runs whenever anything under packaging/files/ is staged:
# that tree is what the .ipk/.apk payload is staged from.
if git diff --cached --name-only | grep -qE '^packaging/files/'; then
    echo "🔍 Checking the AP SSID format contract..."
    bash tests/contract/check-ssid-format.sh
fi
