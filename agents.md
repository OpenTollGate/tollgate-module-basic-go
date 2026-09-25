# TollGate Project - Agent-Specific Knowledge Base

## Project Overview

**TollGate** is an OpenWRT-based router payment gateway that transforms routers into internet access vending machines using Cashu e-cash payments and Nostr protocol. Users pay with satoshis to get time-limited internet access.

### Core Technologies
- **Cashu**: E-cash token system for payments
- **Nostr**: Decentralized communication protocol (NIP-01, NIP-94)
- **Bitcoin Lightning**: Automated payouts to operators
- **OpenWRT**: Router operating system
- **Go 1.24.2**: Primary programming language

### Key Features
- Modular architecture with 13+ independent modules
- Cashu token-based payments for internet access
- Automatic Lightning Network payouts
- Network gateway failover capabilities (crowsnest/wireless_gateway_manager)
- Auto-update system via Nostr NIP-94 events
- Configurable pricing with profit sharing

---

## Project Architecture

### Module Structure

The project uses a **modular Go architecture** with each module being an independent Go package:

```
src/
├── main.go                    # Entry point, HTTP server, Nostr event handling
├── merchant/                  # Payment processing, pricing, payouts
├── valve/                     # Network access control via ndsctl (NoDogSplash)
├── janitor/                   # Auto-update system via Nostr NIP-94
├── bragging/                  # Optional payment announcements on Nostr
├── tollwallet/                # Cashu token operations
├── lightning/                 # Lightning Network integration
├── config_manager/            # Configuration management with migration
├── wireless_gateway_manager/   # WiFi gateway management and failover
├── crowsnest/                # Network monitoring and TollGate discovery
├── chandler/                 # Pricing, data/time usage tracking
├── cli/                      # Command-line tool (tollgate-cli)
├── tollgate_protocol/         # Protocol validation
└── utils/                    # Common utilities
```

### Entry Point: `src/main.go`

**Responsibilities:**
- HTTP server on ports 80 (captive portal) and 2121 (TollGate API)
- Nostr payment event processing (kind 21000)
- Advertisement serving (kind 10021 discovery events)
- Coordination between merchant, valve, janitor, bragging modules

**Key Endpoints:**
- `GET /` → Returns TollGate advertisement (Nostr event kind 10021)
- `POST /` → Accepts Nostr payment events (kind 21000)
- `GET /whoami` → Returns client MAC address
- `GET /x` → Test endpoint

**Payment Flow:**
1. Client receives advertisement from TollGate (kind 10021)
2. Client creates Cashu token
3. Client sends Nostr payment event (kind 21000) with:
   - MAC address in `device-identifier` tag
   - Cashu token in `payment` tag
4. TollGate validates and processes payment via merchant module
5. Merchant authorizes MAC via valve module
6. Client gets internet access for specified duration

---

## Core Modules Deep Dive

### 1. Merchant Module (`src/merchant/`)

**Role:** Financial brain of TollGate

**Responsibilities:**
- Payment processing and validation
- Calculate internet time from payment amount
- Manage pricing and conversions (cashu → minutes/bytes)
- Schedule and process Lightning payouts
- Create network advertisements

**Key Operations:**
- `PurchaseSession(token, macAddress)` → Validates payment, authorizes access
- `GetAdvertisement()` → Generates Nostr kind 10021 event
- `StartPayoutRoutine()` → Background Lightning payout scheduling

**Configuration Integration:**
- `price_per_step`: Base rate per metric unit
- `accepted_mints`: List of Cashu mints accepted
- `profit_share`: Split payouts across multiple Lightning addresses
- `metric`: Flexible metric (milliseconds, bytes, etc.)
- `step_size`: Unit of measurement per price step

---

### 2. Valve Module (`src/valve/`)

**Role:** Network access gatekeeper

**Responsibilities:**
- Open/close network access using `ndsctl` (NoDogSplash)
- Authorize/deauthorize MAC addresses
- Manage access timers with automatic expiration

**Key Functions:**
- `OpenGate(macAddress, durationSeconds)` → Authorize for specified time
- `OpenGateUntil(macAddress, timestamp)` → Authorize until specific time
- Automatically deauthorizes when timer expires

**Integration:**
- Uses `ndsctl auth <mac>` to allow access
- Uses `ndsctl deauth <mac>` to revoke access
- Manages timer-based expiration with goroutines
- Thread-safe with mutex protection

---

### 3. Janitor Module (`src/janitor/`)

**Role:** Auto-update system

**Responsibilities:**
- Listen for NIP-94 events on Nostr relays
- Download updated package files
- Verify package integrity
- Handle architecture-specific updates
- Install updates on the router

**Update Mechanism:**
- Monitors Nostr for NIP-94 events with update URLs
- Downloads `.ipk` packages to `/tmp/`
- Uses `opkg install` to update
- Handles MIPS/ARM architectures separately

**Configuration:**
- Requires valid `currentInstallationID` in config
- Listens on configured Nostr relays
- Uses TollGate private key for authentication

---

### 4. Wireless Gateway Manager (`src/wireless_gateway_manager/`)

**Role:** WiFi gateway connection and failover

**Responsibilities:**
- Scan for available wireless networks
- Connect to upstream gateway routers
- Manage WiFi station interfaces (wifinet0, wifinet1)
- Extract and score vendor elements for gateway selection

**Key Interfaces:**
- `ConnectorInterface`: Connection operations (Connect, Disconnect, Reconnect)
- `ScannerInterface`: Wireless network scanning
- `NetworkMonitorInterface`: Connectivity monitoring
- `VendorElementProcessorInterface`: Vendor element processing

**Use Case:**
- Client routers can automatically connect to TollGate gateway routers
- Supports both 2.4GHz and 5GHz bands
- Gateway selection based on vendor element scoring

---

### 5. Crowsnest Module (`src/crowsnest/`)

**Role:** Network monitoring and TollGate discovery

**Responsibilities:**
- Monitor network interface status changes
- Detect loss of internet connectivity
- Probe for TollGate gateways on connected interfaces
- Coordinate with Chandler for pricing decisions

**Key Interfaces:**
- `NetworkMonitor`: Monitor interface events
- `TollGateProber`: Probe for TollGate advertisements
- `DiscoveryTracker`: Track discovery attempts and results

**Integration:**
- Works with Chandler module for price-based gateway selection
- Context-aware probing with timeouts
- Background goroutine monitoring

---

### 6. Chandler Module (`src/chandler/`)

**Role:** Pricing and usage tracking

**Responsibilities:**
- Track data usage across sessions
- Track time usage for sessions
- Calculate pricing based on configured metrics
- Support multiple pricing models (per-minute, per-byte)

**Key Features:**
- `DataUsageTracker`: Monitor bytes transferred per MAC
- `TimeUsageTracker`: Monitor session duration
- Flexible pricing configuration

---

### 7. Bragging Module (`src/bragging/`)

**Role:** Optional payment announcements

**Responsibilities:**
- Post successful payments to Nostr relays
- Configurable announcement fields
- Use TollGate's identity for signing

**Configuration:**
```json
{
  "bragging": {
    "enabled": true,
    "fields": ["amount", "duration"]
  }
}
```

**Announcement Content:**
- Payment amount
- Session duration
- MAC address (optional)
- Timestamp

---

### 8. Config Manager (`src/config_manager/`)

**Role:** Configuration management with migrations

**Responsibilities:**
- Load/save JSON configuration files
- Handle configuration migrations between versions
- Manage installation-specific configs
- Provide typed configuration access

**Config File Locations:**
- Main config: `/etc/tollgate/config.json`
- Install config: `/etc/tollgate/install_config.json`

**Supported Migrations:**
- v0.0.1 → v0.0.2: Initial migration
- v0.0.2 → v0.0.3: Price structure changes
- v0.0.3 → v0.0.4: Metric/step_size additions

---

## Testing Infrastructure

### Current Testing Setup

**Testing Technologies:**
- **pytest**: Python test framework
- **cdk-cli**: Cashu Development Kit for token operations
- **nak**: Nostr CLI tool
- **sshpass**: SSH automation for router management
- **nmcli**: NetworkManager CLI for WiFi operations

### Test Organization

#### Unit Tests (Go)
- Located alongside source files (`*_test.go`)
- Run with: `go test .`
- Test configuration via `TOLLGATE_TEST_CONFIG_DIR` environment variable
- Example: `src/valve/valve_test.go`, `src/utils/utils_test.go`

#### Integration Tests (Python/pytest)

**Location:** `tests/` directory

**Key Test Files:**

1. **`conftest.py`** - Test fixtures and setup
   - `ecash_wallet`: Temporary wallet with funded tokens
   - `tollgate_networks`: Discover TollGate SSIDs
   - `connect_to_tollgate_network`: WiFi connection management
   - `install_packages_on_tollgates`: Install `.ipk` on routers
   - `install_images_on_tollgates`: Flash firmware images
   - `get_router_ip()`: Dynamic router IP discovery

2. **`test_network_configuration.py`** - Gateway configuration
   - Configure routers as WiFi clients (station mode)
   - Setup wwan interfaces
   - Configure wireless station interfaces (2.4GHz/5GHz)
   - Restart network services and verify internet connectivity
   - Wait for routers to come back online after reboots

3. **`test_ecash_payment.py`** - End-to-end payment flow
   - Connect to TollGate network
   - Fetch discovery event (kind 10021)
   - Generate Cashu tokens from test mint
   - Create Nostr payment event (kind 21000)
   - Send payment to TollGate
   - Verify internet connectivity after payment

4. **`test_teardown.py`** - Firmware flashing
   - Install new firmware images on all routers
   - Flash routers with `.bin` images
   - Post-test image flashing

5. **`test_install_packages.py`** - Package installation
   - Download and install `.ipk` packages
   - Verify package installation

6. **`test_install_images.py`** - Image transfer
   - Copy firmware images to routers
   - Prepare for flashing

7. **`test_copy_images.py`** - Image management
   - Copy firmware between locations

### Test Execution

**Run all tests:**
```bash
cd tests
pytest
```

**Run specific test:**
```bash
pytest test_network_configuration.py -v
```

**Run with specific fixtures:**
```bash
pytest test_ecash_payment.py::test_pay_tollgate_and_verify_connectivity -v
```

### Test Environment

**Required Tools:**
- `cdk-cli`: Cashu wallet and token operations
- `nak`: Nostr event signing/creation
- `sshpass`: Password-based SSH authentication
- `nmcli`: WiFi management (NetworkManager)
- `pytest`: Test runner

**Test Network:**
- Test Mint: `https://nofees.testnut.cashu.space`
- TollGate SSIDs: Start with "TollGate-" prefix
- Router Password: `c03rad0r123` (test environment)
- Network Interface: `wlp59s0` (default, configurable)

### Testing Limitations (Current)

1. **Manual Router Discovery**: Tests rely on WiFi scanning via `nmcli`
2. **Hardware Dependent**: Requires physical OpenWRT routers
3. **Network Dependent**: Requires test network isolation
4. **Sequential Execution**: Tests connect/disconnect WiFi repeatedly
5. **No Parallel Testing**: Router conflicts prevent parallel execution
6. **Slow Execution**: Network operations take significant time
7. **Manual Cleanup**: Post-test router flashing manual
8. **Limited Mocking**: No router simulation, only physical devices

---

## Build System

### OpenWRT Package Build (`Makefile`)

**Package Name:** `tollgate-wrt`

**Key Build Targets:**
- `Build/Prepare`: Prepare build directory
- `Build/Configure`: Empty (no config needed)
- `Build/Compile`: Build Go binary with cross-compilation
- `Package/$(PKG_NAME)/install`: Install files to filesystem

**Build Commands:**
```makefile
# Build main binary
go build -o $(PKG_NAME) -trimpath -ldflags="-s -w"

# Optional: Compress with UPX if available
upx --brute $(PKG_NAME)
```

**Cross-Compilation:**
- `GOARCH`: Target architecture (mips, mipsle, arm, etc.)
- `GOOS`: Target OS (linux)
- `GOMIPS`: MIPS variant (softfloat/hardfloat)

**Install Locations:**
- Binary: `/usr/bin/tollgate-wrt`
- CLI Tool: `/usr/bin/tollgate`
- Init Script: `/etc/init.d/tollgate-wrt`
- Config: `/etc/tollgate/config.json`
- Captive Portal: `/etc/tollgate/tollgate-captive-portal-site/`

### Dependencies

**Runtime Dependencies:**
- `nodogsplash`: Captive portal for network access control
- `luci`: OpenWRT web interface
- `jq`: JSON processing utilities

**Build Dependencies:**
- `golang/host`: Go toolchain for cross-compilation
- `go.mod` dependencies (see below)

### Go Module Structure

**Main Module:** `github.com/OpenTollGate/tollgate-module-basic-go`

**Key Sub-Modules:**
```
github.com/OpenTollGate/tollgate-module-basic-go/src/merchant
github.com/OpenTollGate/tollgate-module-basic-go/src/valve
github.com/OpenTollGate/tollgate-module-basic-go/src/janitor
github.com/OpenTollGate/tollgate-module-basic-go/src/bragging
github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet
github.com/OpenTollGate/tollgate-module-basic-go/src/lightning
github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager
github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager
github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest
github.com/OpenTollGate/tollgate-module-basic-go/src/chandler
github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol
github.com/OpenTollGate/tollgate-module-basic-go/src/utils
github.com/OpenTollGate/tollgate-module-basic-go/src/cli
```

**External Dependencies:**
- `github.com/nbd-wtf/go-nostr`: Nostr protocol implementation
- (Additional dependencies in each module's `go.mod`)

---

## Network Architecture

### TollGate Gateway Mode

**Configuration:**
- WAN interface: Connected to internet
- LAN interface: DHCP server for clients
- WiFi AP: Broadcasting "TollGate-{random}" SSID
- Captive Portal: NoDogSplash + TollGate service

**Data Flow:**
```
Client Device → TollGate AP → Captive Portal → Payment Request
Client Device → Nostr Payment → TollGate HTTP API → Payment Validation
TollGate → Valve → NoDogSplash → Authorize Client MAC
Client Device → Internet Access → Usage Tracking → Automatic Deauth
```

### TollGate Client Mode

**Configuration:**
- WAN interface: No internet (or disconnected)
- WiFi Station: Connects to upstream TollGate gateway
- Crowsnest: Monitors connectivity, scans for gateways
- Chandler: Selects best gateway based on pricing

**Data Flow:**
```
TollGate Client → Offline Detection → Crowsnest Scan
Crowsnest → Gateway Discovery → Vendor Element Scoring
Chandler → Price Comparison → Gateway Selection
WiFi Manager → Connect to Gateway → Internet Access
```

---

## Configuration Files

### Main Config: `/etc/tollgate/config.json`

```json
{
  "tollgate_private_key": "nsec1...",
  "current_installation_id": "uuid-here",
  "metric": "milliseconds",
  "step_size": 20000,
  "price_per_minute": 1,
  "accepted_mints": [
    {
      "url": "https://mint.example.com/Bitcoin",
      "min_balance": 100,
      "balance_tolerance_percent": 10,
      "payout_interval_seconds": 3600,
      "min_payout_amount": 1000
    }
  ],
  "profit_share": [
    {
      "factor": 0.79,
      "lightning_address": "your-address@lightning.provider"
    },
    {
      "factor": 0.21,
      "lightning_address": "tollgate@minibits.cash"
    }
  ],
  "bragging": {
    "enabled": true,
    "fields": ["amount", "duration"]
  }
}
```

### Install Config: `/etc/tollgate/install_config.json`

```json
{
  "IPAddressRandomized": true,
  "FirstLoginCompleted": false
}
```

---

## Key Protocols

### Nostr Event Types Used

1. **Kind 10021**: TollGate Advertisement
   - Published by TollGate gateway
   - Contains pricing information
   - Tags: `metric`, `step_size`, `price_per_step`

2. **Kind 21000**: Payment Event
   - Sent by client to pay for access
   - Tags: `device-identifier` (MAC), `payment` (Cashu token)

3. **Kind 1022**: Session Event
   - Returned by TollGate on successful payment
   - Tags: `p` (customer pubkey), `device-identifier`, `allotment`, `metric`

4. **Kind 21023**: Notice Event
   - Returned on payment failure
   - Tags: `reason` (error message)

5. **Kind 94**: Parameterized Replaceable Event (NIP-94)
   - Used for auto-update notifications
   - Contains update URL and package info

### Cashu Protocol

- **Mint**: `https://nofees.testnut.cashu.space` (testnet)
- **Token Format**: Base64-encoded Cashu token
- **Payment Flow**: Client mints tokens → Sends to TollGate → TollGate verifies → Authorizes access

---

## Documentation Structure

### Design Documents

- `src/HLDD.md`: High-level design for main.go
- `src/LLDD.md`: Low-level design for main.go
- `src/Testing_Plan_Network_Management.md`: Testing plan for client mode
- `src/HLDD_main_network_management.md`: Network management HLDD

### Module-Specific Docs

Each module has its own HLDD/LLDD:
- `src/config_manager/HLDD.md` & `LLDD.md`
- `src/janitor/HLDD.md` & `LLDD.md`

### Additional Docs

- `src/integrating_modules.md`: Module integration guide
- `src/relayPool.md`: Nostr relay pool documentation
- `docs/quick-start-guide.md`: Quick start guide
- `docs/releasenotes/`: Version release notes

---

## Automation Opportunities for Physical Router Testing

### Current Pain Points

1. **Manual Router Management**: Requires physical interaction
2. **Network Setup**: Manual WiFi connections via `nmcli`
3. **Slow Test Execution**: Network operations are slow
4. **Limited Parallelism**: Router conflicts prevent parallel testing
5. **No Hardware Simulation**: Cannot test without physical routers
6. **Manual Firmware Flashing**: Time-consuming router reboots
7. **Fragile WiFi Management**: Connection drops, retries needed

### Potential Automation Approaches

#### 1. Container-Based Router Simulation
- **Tools**: QEMU, Docker with OpenWRT images
- **Benefits**: No physical hardware needed, fast test execution
- **Challenges**: WiFi simulation, NoDogSplash behavior in containers

#### 2. Automated Test Lab Management
- **Tools**: Ansible, SSH, power management (IoT plugs)
- **Benefits**: Full hardware testing, automated power cycles
- **Challenges**: Hardware cost, physical space requirements

#### 3. Virtual Network Topology
- **Tools**: Network namespaces, virtual bridges
- **Benefits**: Realistic network behavior without physical routers
- **Challenges**: Limited NoDogSplash compatibility

#### 4. Cloud-Based Router Testing
- **Tools**: OpenWRT on cloud VMs, VPS providers
- **Benefits**: Scalable, accessible remotely, CI/CD integration
- **Challenges**: No WiFi hardware, limited captive portal testing

#### 5. Mock API Testing
- **Tools**: httptest, mock servers in Go
- **Benefits**: Fast, reliable, no hardware needed
- **Challenges**: Limited integration testing, no real network behavior

#### 6. Hybrid Approach (Recommended)
- **Unit Tests**: Mock API, fast feedback
- **Integration Tests**: Virtual routers (QEMU) for core logic
- **E2E Tests**: Physical routers on schedule (nightly)
- **CI/CD**: Automated for unit/integration, manual for E2E

### Key Automation Requirements

1. **Router Discovery**: Automated scanning and identification
2. **Configuration Management**: Apply configs without manual SSH
3. **Network Control**: Power cycle routers programmatically
4. **Test Orchestration**: Parallel test execution with router pools
5. **Result Collection**: Centralized test reporting
6. **Failure Recovery**: Automatic router reset on test failures

---

## Known Issues and Limitations

1. **Merge Conflicts**: Git merge markers present in some files (HEAD/upx)
2. **Router Password Hardcoded**: Test passwords in Python files
3. **WiFi Interface Hardcoded**: `wlp59s0` in conftest.py
4. **No Test Timeout Handling**: Some tests can hang indefinitely
5. **Limited Error Reporting**: Test failures can be cryptic
6. **Manual Router IP Discovery**: Uses `ip route` parsing
7. **No Router Health Checks**: Assumes routers are working
8. **Network Race Conditions**: Tests can fail due to timing issues

---

## Agent-Specific Recommendations

### For Codebase Exploration
- Focus on module interfaces (`*_interface.go` files)
- Read HLDD/LLDD docs for module understanding
- Check `main.go` for module integration patterns
- Examine `go.mod` files for dependencies

### For Testing
- Start with unit tests (Go) for fast feedback
- Use virtual routers for integration testing when possible
- Run physical router tests sparingly (slow)
- Fix hardcoded test values (interface, password)
- Add test timeouts to prevent hangs

### For Architecture Planning
- Consider separating HTTP endpoints from business logic
- Add middleware for authentication/rate limiting
- Implement proper error handling structures
- Add structured logging throughout
- Consider database for session persistence

### For Automation
- Prioritize container-based router simulation
- Add CI/CD for unit and integration tests
- Schedule E2E physical router tests nightly
- Implement test result aggregation and reporting
- Add automated router health checks

---

## Quick Reference Commands

### Development
```bash
# Run unit tests
cd src && go test ./...

# Run specific test
cd src/valve && go test -v -run TestOpenGate

# Format code
go fmt ./...

# Build for local testing
go build -o tollgate-wrt main.go
```

### Router Operations
```bash
# SSH to router
ssh root@192.168.9.1

# View logs
logread -f

# Restart TollGate
/etc/init.d/tollgate-wrt restart

# View config
cat /etc/tollgate/config.json

# Check running processes
ps | grep tollgate
```

### Testing
```bash
# Run all tests
cd tests && pytest

# Run specific test with verbose
pytest test_network_configuration.py::test_configure_all_routers -v

# Run with custom interface
INTERFACE=wlp0s0 pytest test_ecash_payment.py
```

---

## Version Information

- **Go Version**: 1.24.2
- **OpenWRT Support**: MIPS (softfloat/hardfloat), ARM, x86_64
- **Latest Release**: v0.0.4 (from release notes)
- **Protocol Version**: TollGate Protocol (see `tollgate_protocol/`)

---

## License

GNU General Public License v3.0

---

*Last Updated: 2025-01-19*
