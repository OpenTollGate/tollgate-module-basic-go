# CONTEXT: TollGate Remote Command and Response Module Design

You are extending the **TollGate** router software, which already uses Nostr for communication and module updates.
The goal is to implement a **remote command and response subsystem** that lets authorized controllers send commands
(such as `uptime` and `reboot`) to specific routers using Nostr events. Routers respond with signed Nostr events.

This file defines the entire design and requirements.

---

## 1. Objectives

- Add Nostr-based remote command support to TollGate routers.
- Implement two core commands:
  - `uptime` → query and return system uptime.
  - `reboot` → safely reboot router, with confirmation messages.
- Ensure commands can target specific routers (via Nostr pubkeys).
- Implement signed response events for each command.
- Prevent command replay and reboot loops.
- Follow existing TollGate config and module architecture conventions (like Janitor).

---

## 2. Architectural Overview

Each TollGate router already has:
- `tollgate_private_key` and `tollgate_pubkey` for signing and verifying events.
- A modular architecture: each module subscribes to Nostr relays for relevant events.

### Command Flow

1. **Controller → Router**
   - Controller publishes a signed Nostr event (kind = `5000`).
   - Event has tag `["p", "router-pubkey"]` to target a specific router.

2. **Router**
   - Subscribes to events where `#p` equals its pubkey.
   - Validates sender pubkey against its `allowed_pubkeys` list.
   - Checks nonce/event ID freshness (no replays).
   - Executes requested command.

3. **Router → Controller**
   - Router publishes a signed **response event** (kind = `5100`).
   - Includes `["in_reply_to", original_event_id]`, `["p", "controller-pubkey"]`.
   - Response contains command results or reboot notice.

---

## 3. Event Format

### Command Event Example (Controller → Router)
```json
{
  "kind": 5000,
  "pubkey": "npub-controller",
  "tags": [
    ["p", "npub-router-A1"],
    ["cmd", "uptime"],
    ["device_id", "router-A1"]
  ],
  "content": "{"cmd":"uptime","nonce":"1234","issued_at":1731294000}"
}
```

### Response Event Example (Router → Controller)
```json
{
  "kind": 5100,
  "pubkey": "npub-router-A1",
  "tags": [
    ["p", "npub-controller"],
    ["in_reply_to", "nostr-event-id-of-command"],
    ["cmd", "uptime"],
    ["device_id", "router-A1"]
  ],
  "content": "{"uptime_sec":12345,"status":"ok","timestamp":1731294012}"
}
```

---

## 4. Configuration Additions

Extend `/etc/tollgate/config.json` with a `control` block:

```json
{
  "tollgate_private_key": "YOUR_PRIVATE_KEY",
  "tollgate_pubkey": "YOUR_PUBLIC_KEY",
  "control": {
    "enabled": true,
    "allowed_pubkeys": [
      "npub-controller-1",
      "npub-controller-2"
    ],
    "relay_urls": [
      "wss://relay1.example.com",
      "wss://relay2.example.com"
    ],
    "command_kinds": {
      "command": 5000,
      "response": 5100
    },
    "device_id": "router-A1"
  }
}
```

---

## 5. Implementation Tasks

### a. Command Listener
- New Go module (e.g. `control_listener.go`).
- Subscribe to command events on configured relays:
  ```go
  filter := nostr.Filter{
      Kinds: []int{5000},
      Tags: map[string][]string{"p": {cfg.TollgatePubKey}},
  }
  ```
- Verify:
  - Sender pubkey ∈ `allowed_pubkeys`.
  - Event ID/nonce not already executed.
  - Command is within freshness window (≤5 min old).
- Execute handler for `cmd`.

### b. Command Handlers

| Command | Behavior | Response Timing |
|----------|-----------|----------------|
| `uptime` | Read uptime and send result | After execution |
| `reboot` | Send “rebooting” response, sync logs, call `reboot` | Before and optionally after reboot |

### c. Persistent Ledger (Prevent Replays)
Maintain `/var/lib/tollgate/commands.json`:

```json
{
  "executed_event_ids": ["event123", "event456"],
  "last_reboot_time": 1731293000
}
```

Logic:
- Skip commands whose IDs are already in `executed_event_ids`.
- Skip commands older than 5 minutes.
- Skip reboot if last reboot < 10 minutes ago.
- Append new event ID after execution.

### d. Response Publisher

Helper function:

```go
func respondToCommand(evt nostr.Event, content string) error {
    resp := nostr.Event{
        PubKey: cfg.TollgatePubKey,
        CreatedAt: nostr.Timestamp(time.Now().Unix()),
        Kind: cfg.Control.CommandKinds.Response,
        Tags: nostr.Tags{
            {"cmd", extractCmd(evt)},
            {"p", controllerPubKey(evt)},
            {"in_reply_to", evt.ID},
            {"device_id", cfg.Control.DeviceID},
        },
        Content: content,
    }
    resp.Sign(cfg.TollgatePrivKey)
    return relay.Publish(ctx, resp)
}
```

---

## 6. Example Workflows

### Uptime Command
1. Controller sends `cmd=uptime` → router.
2. Router executes uptime query.
3. Router sends back signed response with uptime seconds.

### Reboot Command
1. Controller sends `cmd=reboot` → router.
2. Router publishes “rebooting” response with current uptime.
3. Router reboots system.
4. After boot, router publishes “rebooted” confirmation with timestamp and version.

---

## 7. Security Rules

- Verify all command signatures using Nostr verification.
- Enforce allowlist of controller pubkeys.
- Use nonces or event IDs for replay protection.
- Reject expired commands (> 5 min old).
- Log every command execution.
- Use private or controlled relays for command events.

---

## 8. Future Extensions

- Add encrypted payloads (e.g., NIP-04 or AEAD wrapper).
- Add richer `status` command and periodic reporting.
- Add multi-controller approval flow for sensitive actions.

---

## 9. Deliverables for Implementation

Claude Sonnet 4.5 should produce:
1. A new Go source file (e.g. `modules/control_listener.go`) implementing:
   - Event subscription
   - Validation
   - Command dispatch
   - Response publishing
   - Ledger persistence
2. Updated config structs to include the new `control` block.
3. Unit tests for:
   - Handling valid/invalid commands
   - Replay prevention
   - Reboot debounce
   - Response event creation

Implementation must follow idiomatic Go conventions and match TollGate’s existing module structure.

---

**End of Context**
