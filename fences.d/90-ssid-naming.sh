# The reader twin of the format contract: hasTollGateSSID's acceptance table
# (both brands, bare and single-'!'-decorated, case-insensitive, double-!
# rejected) is pinned by tests/contract/ssid-naming-fixtures.txt, shared
# with the Go unit test. Runs when the matcher, the table, or the fence
# itself is staged.
if git diff --cached --name-only | grep -qE '^src/wireless_gateway_manager/brands|^tests/contract/(ssid-naming|check-ssid-naming)'; then
    echo "🔍 Checking the SSID matcher's acceptance table..."
    bash tests/contract/check-ssid-naming.sh
fi
