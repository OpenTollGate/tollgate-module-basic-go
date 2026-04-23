### Progress Update (a005c80f0f8183923e0338bd185f5f653038377f)

The upstream WiFi connection daemon and CLI tool are working as intended. Tested by:

1. Connecting to `c03rad0r` upstream via `wifiscan.sh connect`
2. Connecting to `c03rad0r-7a` via `wifiscan.sh connect` (disables `c03rad0r`, preserves it as a known candidate)
3. Turning off `c03rad0r-7a` — the daemon detected the connectivity loss within ~60 seconds, scanned for known upstreams, and automatically switched to `c03rad0r`
4. Confirmed internet access restored (`ping 9.9.9.9` succeeded after the switch)

The `/etc/config/wireless` config now shows multiple disabled STA interfaces with their SSIDs and passwords intact, which the daemon uses as its candidate pool.

### Before merge: port to `WirelessGatewayManager` + `tollgate` CLI

The shell implementation (`upstream-daemon.sh` and `wifiscan.sh`) is a working proof-of-concept. Before merging, this functionality needs to be ported into the Go codebase:

- **`WirelessGatewayManager`** (`src/wireless_gateway_manager/`) — add the connectivity monitoring, known-upstream tracking, and signal-based switching logic. This is distinct from the existing TollGate peering logic but should live alongside it in the same manager.
- **`tollgate` CLI** (`src/cmd/tollgate-cli/`) — expose `scan`, `connect <SSID>`, `list-upstream`, and `remove-upstream` subcommands to replace the `wifiscan.sh` interface.
- The procd init script (`tollgate-upstream`) would be replaced by the Go service's existing procd management in `tollgate-wrt`.

This avoids having two independent shell/Go systems managing upstream WiFi connections (code smell) and gives users a single consistent CLI for both TollGate peering and generic upstream management.

