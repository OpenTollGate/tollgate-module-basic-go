# TIP-07 Remote Control Testing Guide

## Overview

This is a **step-by-step testing guide** for the TIP-07 remote control feature. Follow these steps in order to verify that remote management of TollGate devices works correctly.

**What you'll test:**
- Sending commands to remote TollGate devices over Nostr relays
- Querying device status (uptime, memory, version)
- Remotely rebooting devices
- Security features (authorization, replay protection)

**Time required:** 30-60 minutes

## Prerequisites - What You Need

Before starting, ensure you have:

- [ ] **Two TollGate devices** with this branch's build installed (or one device + development machine)
- [ ] **SSH access** to both devices
- [ ] **Nostr keypair** for your controller (generate if needed - see Step 1)
- [ ] **Internet connection** on both devices (for relay access)
- [ ] **Basic knowledge** of SSH and command line

## Step 0: Generate Your Controller Keypair

If you don't already have a Nostr keypair, generate one now.

**Option A: Using nak (recommended)**
```bash
# Install nak if needed
go install github.com/fiatjaf/nak@latest

# Generate keypair
nak key generate
```

**Option B: Using the example script**
```bash
cd examples
go run send_command.go -help
# The script can generate keys for you
```

**Save your keys securely:**
- Private key (nsec): Keep this secret! You'll use it to send commands
- Public key (npub): Share this - it goes in device config

Example:
```
nsec1abc123...  ← Your PRIVATE controller key (keep secret!)
npub1xyz789...  ← Your PUBLIC controller key (goes in config)
```

## Step 1: Setup Target Device (Device A)

This is the TollGate you want to **control remotely**.

**1.1 - SSH into Device A:**
```bash
ssh root@192.168.1.1  # Use your device's IP
```

**1.2 - Get Device A's public key:**
```bash
cat /etc/tollgate/config.json | grep -A 2 '"identity"'
```

You should see something like:
```json
"identity": {
  "pubkey": "npub1abc123..."
}
```

**Write down Device A's pubkey:** `npub1abc123...` ← You'll need this later

**1.3 - Get your Controller's public key:**

This is the npub you generated in Step 0, OR if you're using Device B as the controller, get Device B's pubkey the same way.

**1.4 - Configure Device A to accept remote commands:**

Edit the config file:
```bash
vi /etc/tollgate/config.json
```

Add or modify the `control` section:
```json
{
  "identity": { ... },
  "control": {
    "enabled": true,
    "device_id": "tollgate-test-1",
    "allowed_pubkeys": [
      "npub1xyz789..."
    ],
    "command_timeout_sec": 300,
    "relays": [
      "wss://relay.damus.io"
    ]
  }
}
```

**Replace `npub1xyz789...` with YOUR controller's public key** from Step 0!

**1.5 - Restart TollGate service:**
```bash
/etc/init.d/tollgate restart
```

**1.6 - Verify Commander started:**
```bash
logread | grep Commander
```

You should see:
```
Commander: Starting TIP-07 remote control listener for device tollgate-test-1
Commander: Authorized controllers: [npub1xyz789...]
Commander: Connected to relay wss://relay.damus.io
```

✅ **Device A is now ready to receive commands**

---

## Step 2: Setup Controller (Device B or Your Machine)

This is where you'll **send commands from**.

**Option A: Using another TollGate device (Device B)**

Just make sure Device B has the software installed and running. Device B will use its own identity automatically.

**Option B: Using your development machine**

You'll need to use the example script (see Alternative Method at the end).

**For this guide, we'll use Device B (another TollGate).**

**2.1 - SSH into Device B:**
```bash
ssh root@192.168.1.2  # Use Device B's IP
```

**2.2 - Verify Device B's identity:**
```bash
cat /etc/tollgate/config.json | grep pubkey
```

Make sure this pubkey matches what you put in Device A's `allowed_pubkeys` list!

✅ **Device B is ready to send commands**

---

## Step 3: TEST 1 - Query Device Status

**Goal:** Get uptime and status from Device A

**3.1 - On Device B, run:**
```bash
tollgate control status npub1abc123...
```

Replace `npub1abc123...` with Device A's pubkey from Step 1.2

**3.2 - Expected output:**
```json
{
  "status": "ok",
  "data": {
    "version": "0.0.4",
    "uptime_sec": 3642,
    "memory_free_kb": 48392,
    "device_id": "tollgate-test-1"
  }
}
```

**3.3 - Verify the results:**
- [ ] Response received within ~5-10 seconds
- [ ] `uptime_sec` is reasonable (check with `uptime` on Device A)
- [ ] `memory_free_kb` shows available memory
- [ ] `device_id` matches Device A's config

**If it doesn't work, see Troubleshooting section at the end.**

✅ **TEST 1 PASSED** - Device A responded to status query

---

## Step 4: TEST 2 - Query Status with Timeout

**Goal:** Test the timeout parameter

**4.1 - On Device B, run with short timeout:**
```bash
tollgate control status npub1abc123... --timeout 5
```

**4.2 - On Device B, run with longer timeout:**
```bash
tollgate control status npub1abc123... --timeout 45
```

**4.3 - Verify:**
- [ ] Command still works with different timeouts
- [ ] Timeout errors are clear if device doesn't respond in time

✅ **TEST 2 PASSED** - Timeout parameter works

---

## Step 5: TEST 3 - Remote Reboot (CAREFUL!)

**Goal:** Remotely reboot Device A

⚠️ **WARNING: This will actually reboot Device A!**

**5.1 - On Device B, schedule reboot with 60 second delay:**
```bash
tollgate control reboot npub1abc123... --args '{"delay_sec":60}'
```

**5.2 - Expected immediate response:**
```json
{
  "status": "ok",
  "message": "Reboot scheduled in 60 seconds"
}
```

**5.3 - On Device A, verify the scheduled reboot:**
```bash
logread | grep "REBOOT command"
```

You should see:
```
Commander: REBOOT command accepted, scheduling reboot in 60 seconds...
```

**5.4 - Wait 60 seconds and verify Device A reboots**

**5.5 - After Device A comes back online, verify Commander restarted:**
```bash
ssh root@192.168.1.1
logread | grep "Commander: Starting"
```

**5.6 - Verify:**
- [ ] Response received before reboot
- [ ] Device A rebooted after 60 seconds
- [ ] Device A came back online
- [ ] Commander service auto-started

✅ **TEST 3 PASSED** - Remote reboot works

---

## Step 6: TEST 4 - Authorization Test

**Goal:** Verify unauthorized devices can't send commands

**6.1 - On Device B, query Device A (should work):**
```bash
tollgate control status npub1abc123...
```

This should work (Device B is authorized).

**6.2 - On Device A, remove Device B from allowed list:**

Edit `/etc/tollgate/config.json` and remove Device B's pubkey from `allowed_pubkeys`, then:
```bash
/etc/init.d/tollgate restart
```

**6.3 - On Device B, try again:**
```bash
tollgate control status npub1abc123... --timeout 15
```

**Expected:** Timeout with no response (command silently rejected)

**6.4 - On Device A, check logs:**
```bash
logread | grep "unauthorized pubkey"
```

You should see:
```
Commander: Command validation failed: unauthorized pubkey: npub1...
```

**6.5 - Restore authorization:**

Add Device B's pubkey back to `allowed_pubkeys` and restart Device A.

**6.6 - Verify:**
- [ ] Authorized device can send commands
- [ ] Unauthorized device gets no response
- [ ] Device A logs the rejection

✅ **TEST 4 PASSED** - Authorization works

---

## Step 7: TEST 5 - Multi-Device Control

**Goal:** Control multiple devices from one controller

**Requirements:** You need a third device (Device C) for this test.

**7.1 - Setup Device C same as Device A (Step 1)**

Make sure Device C:
- Has `control.enabled: true`
- Has Device B's pubkey in `allowed_pubkeys`
- Has a unique `device_id` like "tollgate-test-2"

**7.2 - From Device B, query both devices:**

Query Device A:
```bash
tollgate control status npub1deviceA...
```

Query Device C:
```bash
tollgate control status npub1deviceC...
```

**7.3 - Verify:**
- [ ] Both devices respond independently
- [ ] Device IDs are different
- [ ] Uptimes are different

✅ **TEST 5 PASSED** - Multi-device control works

---

## Step 8: Monitoring and Verification

**8.1 - Check Device A's command ledger:**
```bash
cat /tmp/tollgate/commander_ledger.json | jq .
```

You should see a history of processed commands:
```json
{
  "abc123...": {
    "event_id": "abc123...",
    "processed_at": 1699632000,
    "command": "status"
  }
}
```

**8.2 - View real-time logs:**
```bash
logread -f | grep Commander
```

Run some commands from Device B and watch Device A's logs in real-time.

✅ **Monitoring verified**

---

```json
{
  "control": {
    "enabled": true,
    "device_id": "tollgate-alpha",
    "allowed_pubkeys": [
      "npub1yourcontrollerpubkey...",
      "npub1anotherauthorizedkey..."
    ],
    "command_timeout_sec": 300,
    "relays": [
      "wss://relay.damus.io",
      "wss://nos.lol"
    ]
  }
}
```

**Configuration Fields:**
- `enabled`: Set to `true` to activate remote control
- `device_id`: Unique identifier for this TollGate (optional, used for multi-device setups)
- `allowed_pubkeys`: List of Nostr public keys authorized to send commands (in npub format)
- `command_timeout_sec`: Maximum age of commands (default: 300 seconds / 5 minutes)
- `relays`: List of Nostr relays to monitor for commands

### Restart TollGate Service

After configuration changes:
```bash
/etc/init.d/tollgate restart
```

The commander will start automatically if `control.enabled` is `true`.

