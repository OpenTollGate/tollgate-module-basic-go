## Upstream WiFi Connection Daemon & CLI

Adds a shell-based daemon and CLI tool for managing upstream WiFi STA connections on OpenWrt routers.

### Relationship to existing `GatewayManager`

The Go `GatewayManager` (`src/wireless_gateway_manager/`) currently handles **discovering and connecting to upstream TollGate routers** (off by default) — scanning for nearby TollGates, negotiating connections, and handling the TollGate-specific protocol.

**This PR adds functionality that is distinct from but complementary to the `GatewayManager`:**

- **SSIDs and passwords tracking** — when a user connects to a new upstream, the previous connection is preserved in `/etc/config/wireless` as a disabled STA interface, retaining its SSID and passphrase for future use.
- **Multi-radio scanning** — scans both radios and presents available networks to the user, making it easy to discover potential upstreams.
- **CLI for adding new upstreams** — `wifiscan.sh connect` creates a new STA interface, disables the old one, and commits the config so no upstream credentials are lost.
- **Background daemon** — monitors connectivity and automatically switches between known upstreams based on signal strength with a 12 dB hysteresis to prevent flapping.

The `GatewayManager` handles TollGate-to-TollGate peering. This PR handles connecting to generic upstream WiFi gateways for internet access — these are two distinct concerns.

**This shell implementation is a proof-of-concept.** Having two independent systems for managing upstream WiFi connections would be code smell. The intent is to port this logic into the `GatewayManager` and expose it to the user via the `tollgate` CLI, replacing `wifiscan.sh` and `upstream-daemon.sh` with native Go implementations. The shell scripts serve as a working reference for the desired behavior and a usable solution in the meantime.

### What the daemon does

- **Discovers known upstreams** — any `wifi-iface` with `mode='sta'` in `/etc/config/wireless` is a candidate. Disabled STAs are treated as known upstreams; the one with `disabled='0'` is active.
- **Signal optimization** — every 5 minutes, scans all radios and switches to a known upstream if it's at least **12 dB stronger** (hysteresis to prevent flapping). This is quite similar to what the wireless gateway manager already does. Arguably this feature of the wireless gateway manager should be on by default if it can only switch to interfaces that the user manually added to `/etc/config/wireless`.
- **Fast connectivity monitoring** — every 30 seconds, checks STA association + gateway reachability. If connectivity is lost for 2 consecutive checks (~60s), triggers an immediate emergency rescan and switches to the best available upstream.
- **Safe fallback** — if a switch times out waiting for DHCP, the previous upstream is automatically re-enabled.

### `wifiscan.sh` — interactive WiFi management tool

Ships as `/usr/bin/wifiscan.sh` with commands that will later map to `tollgate` CLI subcommands:

| Command | Description |
|---|---|
| `wifiscan.sh` | Scan and list available networks on both radios |
| `wifiscan.sh connect <SSID> [PASS]` | Connect to a network (creates a new STA section, disables the old one preserving it as a known candidate) |
| `wifiscan.sh list-upstream` | Show all STA configs with active/disabled status |
| `wifiscan.sh remove-upstream <SSID>` | Remove a disabled upstream from config |

### New files

| File | Purpose |
|---|---|
| `files/usr/bin/upstream-daemon.sh` | The daemon (procd-managed) |
| `files/etc/init.d/tollgate-upstream` | Init script (START=96) |
| `wifiscan.sh` | WiFi scanner/connect CLI tool |
| `Makefile` | Installs the above files into the package |

### Configurable via environment variables

| Variable | Default | Description |
|---|---|---|
| `UPSTREAM_SCAN_INTERVAL` | 300 | Full scan interval (seconds) |
| `UPSTREAM_FAST_CHECK` | 30 | Connectivity check interval (seconds) |
| `UPSTREAM_LOST_THRESHOLD` | 2 | Consecutive failures before emergency rescan |
| `UPSTREAM_HYSTERESIS_DB` | 12 | Minimum dB improvement to switch |
| `UPSTREAM_SIGNAL_FLOOR` | -85 | Signal below which reconnect is forced |
