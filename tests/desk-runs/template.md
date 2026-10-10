# Desk run <YYYY-MM-DD> — <device-slug> — <one-line scenario>

<!-- Scaffolded by tests/desk-runs/new-run.sh. Sanitize before commit:
     no SSIDs, no passphrases, no keys, no token strings. -->

## 1. Environment, declared

- Device: <model (target/arch)>
- OS: <OpenWrt version, provenance>
- TollGate: <version before → after; origin of the bytes: published tag /
  locally built at git <sha> with ldflags <label>>
- Uplink: <WAN cable / WiFi STA (network redacted) / none>
- Mints: <test-class mint URL(s); value class stated>
- Harness: <how the operator reached the device; tools used>

## 2. Safety gates (before anything destructive)

- Backup: <what was copied out, where it lives, sha256 file>
- Value scan: <tokens found? wallet balance? identities? verdict>
- Rollback: <what makes the change reversible>

## 3. What ran

<ordered steps, essential commands only, sanitized>

## 4. Results

| # | Assertion | Verdict | Evidence |
|---|---|---|---|
| 1 | <e.g. payment grants a session> | PASS/FAIL/NIT | <log line / event field> |

## 5. Findings (each → an issue)

- <finding>: #<issue>

## 6. Artifacts

- <path> — <sha256 or "committed evidence">

## 7. Not tested, and why

- <gap> — <reason>
