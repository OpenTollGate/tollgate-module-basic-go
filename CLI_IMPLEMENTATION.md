# TollGate CLI Implementation

## Overview

This document describes the implementation of a CLI interface for the TollGate service, accessible via SSH using Unix domain sockets.

## Architecture

### Components

1. **CLI Server** (`src/cli/server.go`)
   - Listens on Unix socket `/var/run/tollgate.sock`
   - Handles CLI commands from client
   - Integrates with existing TollGate service modules

2. **CLI Client** (`src/cmd/tollgate-cli/main.go`)
   - Command-line binary built with Cobra
   - Communicates with service via Unix socket
   - Provides structured command interface

3. **Message Types** (`src/cli/types.go`)
   - JSON-based communication protocol
   - Request/response structures
   - Data types for wallet operations

## Commands Implemented

### Primary Command: `tollgate wallet drain cashu`

This command drains all wallet balances to Cashu tokens for each configured mint:

```bash
tollgate wallet drain cashu
```

**Expected Output:**
```
Wallet Drain Results:
====================
Total drained: 1500 sats

Token 1:
  Mint: https://mint1.example.com
  Balance: 1000 sats
  Token: cashuAeyJ0eXAiOiJub3... (full token string)

Token 2:
  Mint: https://mint2.example.com
  Balance: 500 sats
  Token: cashuBfyJ0eXAiOiJub3... (full token string)
```

### Additional Commands

- `tollgate status` - Show service status
- `tollgate version` - Show version information
- `tollgate wallet balance` - Show wallet balance
- `tollgate wallet info` - Show wallet information

## Installation & Usage

### Building the CLI

The CLI is automatically built and installed when building the main TollGate package:

```bash
# The main Makefile builds both the service and CLI
make package

# Or for development, build the CLI directly:
cd src/cmd/tollgate-cli
go build -o tollgate
```

### System Installation

When the TollGate package is installed on OpenWrt, the CLI is automatically available system-wide:

- **Service binary**: `/usr/bin/tollgate-basic` (the main service)
- **CLI binary**: `/usr/bin/tollgate` (the CLI tool)

### Usage Examples

Once installed, the CLI is available from anywhere on the system:

```bash
# Check service status (works from any directory)
tollgate status

# Drain wallet to Cashu tokens
tollgate wallet drain cashu

# Show wallet balance
tollgate wallet balance

# Get help
tollgate --help
tollgate wallet --help
```

## Implementation Details

### Server Integration

The CLI server is integrated into the main TollGate service in `src/main.go`:

```go
// Initialize CLI server
initCLIServer()
```

The server starts automatically when the TollGate service starts.

### Communication Protocol

- **Transport**: Unix domain socket (`/var/run/tollgate.sock`)
- **Format**: JSON messages
- **Security**: Local access only (perfect for SSH access)

### Message Flow

1. Client connects to Unix socket
2. Client sends JSON command message
3. Server processes command using existing modules
4. Server returns JSON response
5. Client displays formatted output

### Wallet Drain Implementation

The `wallet drain cashu` command:

1. Gets list of accepted mints from merchant
2. Checks balance for each mint
3. Creates payment tokens for full balance of each mint
4. Returns tokens as JSON response
5. Client formats and displays tokens

## Future Enhancements

### Planned: `tollgate wallet drain lightning [address]`

This command will drain wallet to a Lightning address:

```bash
tollgate wallet drain lightning user@getalby.com
```

## Error Handling

- Service not running: Clear error message
- Permission issues: Socket permission errors
- Invalid commands: Help text and suggestions
- Wallet errors: Detailed error messages from merchant module

## Security Considerations

- Unix socket provides local-only access
- No network exposure
- Inherits user permissions
- Socket file permissions: 0666

## Dependencies

- **Cobra**: CLI framework for command parsing
- **Logrus**: Logging (inherited from main service)
- **encoding/json**: Message serialization
- **net**: Unix socket communication

## File Structure

```
src/
├── cli/
│   ├── types.go      # Message types and data structures
│   ├── server.go     # Unix socket server implementation
│   └── go.mod        # CLI module dependencies
├── cmd/
│   └── tollgate-cli/
│       ├── main.go   # CLI client application
│       ├── go.mod    # Client dependencies
│       └── Makefile  # Build and install targets
└── main.go           # Integration point in main service
```

## Testing

The implementation provides:

1. **Unit testing** capability for server components
2. **Integration testing** via CLI commands
3. **Manual testing** with make targets

### Manual Testing Commands

```bash
# Build and test CLI
cd src/cmd/tollgate-cli
make dev-test

# Test with running service
tollgate status
tollgate wallet drain cashu
```

## Benefits

1. **SSH-friendly**: Perfect for remote server management
2. **Scriptable**: Can be used in shell scripts and automation
3. **Structured**: Consistent command interface
4. **Extensible**: Easy to add new commands
5. **Secure**: Local-only access via Unix sockets
6. **Integrated**: Direct access to service modules