# Wireless Gateway Manager

Manages upstream Wi-Fi connectivity for tollgate routers running OpenWrt.

## Architecture

```
main.go
  ├── Connector (UCI/STA management)
  ├── Scanner (iw scan operations)
  ├── UpstreamManager (daemon loop)
  ├── VendorElementProcessor (TollGate vendor IEs)
  └── CLIServer (Unix socket CLI)
```

All components share a single `Connector` and `Scanner` instance created at startup.

### Components

- **Connector** (`connector.go`) — Manages OpenWrt UCI configuration for STA interfaces, DHCP, radio setup, upstream switching, and stale STA cleanup.
- **Scanner** (`scanner.go`) — Scans Wi-Fi radios via `iw`, parses results, detects encryption, finds best radio for a given SSID.
- **UpstreamManager** (`upstream_manager.go`) — Background daemon that monitors connectivity, scans for better upstreams, switches automatically on signal degradation or connectivity loss.
- **VendorElementProcessor** (`vendor_element_manager.go`) — Encodes/decodes TollGate vendor-specific IEs (802.11 IE type 221, OUI `21:21:21`). Carries gateway metadata: version, flags (reseller, internet, open), mint URL, pubkey. Gated behind `vendor_ie_discovery` config flag.
- **CLIServer** (`cli/server.go`) — Unix domain socket server exposing `tollgate upstream *` CLI commands.

## Configuration

All upstream Wi-Fi settings are surfaced in `/etc/tollgate/config.json` under the `upstream_wifi` key:

```json
{
  "upstream_wifi": {
    "scan_interval_seconds": 300,
    "fast_check_seconds": 30,
    "lost_threshold": 2,
    "hysteresis_db": 12,
    "signal_floor": -85,
    "blacklist_ttl_minutes": 60,
    "emergency_penalty": 20,
    "max_consecutive_failures": 3,
    "switch_cooldown_minutes": 10,
    "startup_grace_seconds": 90,
    "post_switch_wait_seconds": 5,
    "dhcp_timeout_seconds": 180,
    "manual_pause_seconds": 120
  }
}
```

| Field | Default | Description |
|---|---|---|
| `scan_interval_seconds` | 300 | Full scan cycle interval |
| `fast_check_seconds` | 30 | Connectivity check interval |
| `lost_threshold` | 2 | Consecutive failures before emergency scan |
| `hysteresis_db` | 12 | Minimum signal improvement (dBm) to trigger switch |
| `signal_floor` | -85 | Switch if active signal drops below this |
| `blacklist_ttl_minutes` | 60 | How long to blacklist a non-functional SSID |
| `emergency_penalty` | 20 | Penalty applied to TollGate SSIDs during emergency scans |
| `max_consecutive_failures` | 3 | Switch failures before circuit breaker cooldown |
| `switch_cooldown_minutes` | 10 | Circuit breaker cooldown duration |
| `startup_grace_seconds` | 90 | Defer automatic scans and switches at startup; active login windows keep checking |
| `post_switch_wait_seconds` | 5 | Wait after switch before verifying connectivity |
| `dhcp_timeout_seconds` | 180 | Timeout for DHCP after switching upstream |
| `manual_pause_seconds` | 120 | Pause duration after manual CLI connect |

## CLI Commands

```
tollgate upstream scan              # Scan for upstream networks (includes peer metadata)
tollgate upstream connect <SSID> [PASS]  # Connect to upstream / peer
tollgate upstream list              # List configured STA sections
tollgate upstream remove <SSID>     # Remove a disabled STA
tollgate upstream known             # Known TollGate peers from scan history
tollgate upstream status            # Live peer session, spend and allotment
tollgate upstream disconnect        # Stop peering; fall back to a known good non-TollGate upstream
tollgate status                     # Service status (includes NetworkOK)
```

## Reseller Mode

When `reseller_mode` is enabled in config, the daemon automatically discovers and connects to open `TollGate-*` SSIDs alongside known fallback networks (the shipped captive SSID carries a leading `!` sort decoration, `!TollGate-*`, which the recognizer treats as optional — the bare form still matches). Non-TollGate SSIDs are only considered if a disabled STA section already exists for them.

The scan also carries the peer metadata from the TollGate vendor IE: reseller capability, internet advertised, open network, mint URL and pubkey. `upstream status` exposes the live session's allotment, usage, spend, price, mint and bootstrap phase to the admin Peering page.

`upstream disconnect` is the explicit "stop peering" verb. It selects the best known, non-blacklisted, non-TollGate upstream, switches to it, verifies connectivity, and only reports success when that check passes. If every fallback fails, it restores the peer and returns an error instead of leaving the router offline.

## Recovery after internet loss

Emergency scans can try a known saved upstream even when its signal is weaker than the active connection. Signal hysteresis still applies to routine optimization. Candidate filtering, BSSID blacklists, reseller policy and the circuit breaker remain in force; an unknown private network is never enrolled automatically.

After a handover, the existing layered connectivity probe retries failures over a 30-second convergence window, using `post_switch_wait_seconds` between attempts. An in-flight probe keeps its own timeout and can finish after that window. A later handover or manual join supersedes the old verification, so it cannot blacklist the new connection or publish stale connectivity metadata.

Manual plain/streaming joins acquire an explicit writer lease before enabling radios and hold it through scanning, STA creation and the full DHCP wait, even when that operation exceeds `manual_pause_seconds`. Remove and stop-peering fallback use the same lease. Concurrent manual writers serialize. Automatic switch decisions recheck pause and generation while holding that lease through the connector call; startup nudges, candidate creation, radio enabling and cleanup also decline without queueing when it is busy. A completed manual operation invalidates scans and connectivity observations from before it. Declined work does not count as a failed connection or trigger cooldown. The existing timed pause is retained after a join. A successful manual join clears the failure entry for the selected SSID/BSSID only.

`BeginManualOperation`/`EndManualOperation` remain nested pause-only scopes; they do not grant writer ownership. Radio-mutating callers use `BeginManualWriter` and defer its `End`. The lease coordinates this manager and its CLI handlers, not external root/UCI writers. `LoopTicks` reports loop progress without using a radio mutation as a heartbeat. These source-level concurrency tests do not qualify RF, DHCP handover or deployed-router recovery; those remain PRTA/physical acceptance gates.

## Venue portal login window

An associated venue Wi-Fi uplink that returns an unexpected HTTP probe response
gets one two-minute opportunity for manual portal login. Startup, periodic checks
and post-switch verification share the same deadline. Repeated captive responses,
newly learned BSSIDs and transient route/DNS/HTTP errors do not extend it. During
that window the manager keeps probing, including during startup grace, and declines
automatic radio nudges, cleanup, scanning, switching and failure blacklisting.
The uplink is still reported unhealthy until the Internet probe actually succeeds.

Verified Internet access clears the window and the loop's loss count. Losing the
association resumes ordinary recovery immediately. Manual joins, disconnect and
stop-peering retain priority; a new connection generation invalidates old callbacks.
The existing TollGate gateway-recognition exemption remains in place.

At expiry the manager requests a saved-network-only fallback, even in reseller
mode and even if the alternative has a weaker signal. It does not enroll unknown
TollGates from this timeout or force a disconnect when no saved alternative exists.
Startup grace, manual pauses, cooldown, the next control tick and candidate
availability may defer the actual handover beyond two minutes. Structured logs
report window start, successful Internet verification, expiry and deferred fallback.

An unexpected HTTP response is only suspected captivity: a false positive can cause
one bounded delay. This adds no portal credential storage, automatic login,
frontend or public configuration field. The internal `PortalLoginWindow` constructor
override uses two minutes for zero or negative values. Router/venue behavior still
requires artifact-bound PRTA acceptance; source tests alone do not qualify it.

## Automatic portal login (opt-in)

`upstream_wifi.portal_login_enabled` enables one bounded, automatic submission of
the venue's own form. `portal_login_mode` is `click_through` (submit the page's
hidden fields and accept button) or `form` (also fill the configured
username/password fields). `portal_login_extra` carries urlencoded fields for a
guest form that wants a name or room but no password. Credentials are never
logged.

The login is **bound to the uplink that owns it**. Every request, redirect and
DNS lookup uses the target netdev (`SO_BINDTODEVICE`), the configured venue
resolver and the existing `SO_MARK`, so a second STA does not inherit the
default route of the first. Redirects and form actions may leave the page host
only when the destination is listed in `portal_login_allow_hosts` (exact host,
or `host:port`). The page host is always allowed; credentials never go
elsewhere. One cookie jar carries the session across fetch, redirect and submit.

### Per-uplink recipes

`upstream_wifi.portal_login_recipes` is a list keyed by netdev. It exists for a
bond's second STA, which the single-STA manager does not own:

```json
{
  "upstream_wifi": {
    "portal_login_enabled": true,
    "portal_login_recipes": [
      {
        "interface": "phy1-sta0",
        "ssid": "Venue Guest",
        "mode": "click_through",
        "url": "https://signin.example.com/guest",
        "dns": "10.32.64.1",
        "allow_hosts": ["signin.example.com"],
        "extra": "firstname=TollGate&surname=Operator",
        "budget_seconds": 7200,
        "budget_bytes": 2000000000
      }
    ]
  }
}
```

Each recipe gets one attempt per association/lease generation, then a bounded
retry (three attempts with 30-second steps). When the cap is reached, or when
`budget_seconds`/`budget_bytes` is exceeded after a successful login, the uplink
is **exhausted** and is not submitted again. A new lease resets the attempt
state; the exhausted state does not reset on a timer.

This is deliberately **not** an identity-rotation feature. The client does not
change a MAC address, regenerate the node key, or invent a new user to obtain
another free session. An exhausted session is left for the operator (or the
venue's own paid/renewal flow). See the operator guide's client-identity
section for why MAC/key rotation is not a supported access mechanism.

Router/venue behavior still requires artifact-bound PRTA acceptance; source
tests alone do not qualify it.

## Circuit Breaker

After `max_consecutive_failures` consecutive switch failures, the circuit breaker enters a cooldown period (`switch_cooldown_minutes`). During cooldown, scan cycles are skipped to avoid rapid reconnection attempts.

## StaSTA Cleanup

On startup and before each scan cycle, `CleanupStaleSTAs()` removes STA network interfaces whose underlying wireless section no longer exists, preventing UCI pollution from previous connections.
