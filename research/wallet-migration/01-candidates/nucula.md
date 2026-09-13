# Candidate: nucula (`zeugmaster/nucula`)

## What it is (verified from the README)

> A Cashu ecash wallet for the **ESP32-C3** with **NFC tap-to-pay**.

Feature claims: store ecash from multiple mints; receive over NFC; mint new
tokens via Lightning invoices; melt to pay Lightning invoices; tokens received
offline are stashed and redeemed automatically when WiFi returns.

Reference hardware: **Seeed XIAO ESP32-C3**; peripherals on one I2C bus —
PN7160 NFC controller (card emulation), SSD1309 128x64 OLED, PCF8574 keypad
expander. Pins/addresses in `main/board.h`; NCI control pins in
`components/pn7160/include/nci.h`. Build: **ESP-IDF v5.x**; `main/wifi_config.h`
holds credentials (edited from an example).

## Assessment so far

| Dimension | Finding |
|---|---|
| Runtime target | ESP32-C3 firmware. **Not** a Linux/OpenWrt service. |
| Language | C++ (CMake/ESP-IDF build) |
| Licence | **No LICENSE file in the repo** (verified via the GitHub contents API). Absent a licence there is no grant of rights — cannot be vendored or linked into TollGate without the author's explicit permission. |
| Maturity | Personal project, 6 stars, last push 2026-07-20. Bus factor 1. |
| Security posture | No published audit found; embedded wallet holding real ecash keys on-device. |

## What it could be relevant to

If TollGate wants an **end-user tap-to-pay device** (board-side wallet), nucula
is a candidate *implementation* to study or fork — but that is a different
product decision from "replace the router's wallet library", and it inherits the
licence question plus ESP-IDF toolchain and hardware dependencies (NFC + OLED +
keypad, all I2C).

## Open questions (owner: consultant A, see TASKS.md T2)

- [ ] NUT coverage actually implemented vs claimed (cite code, not README)
- [ ] Licence intent: ask the author; until answered, treat as all-rights-reserved
- [ ] Proof storage on flash: format, wear implications, crash consistency
- [ ] Key handling: where the seed lives, whether it is encrypted at rest
- [ ] Real memory footprint (IRAM/DRAM/flash) from a build, and whether an
      ESP32-C3 (400 KB SRAM class) is actually comfortable with multi-mint ecash
- [ ] Portability of the *logic* (not the firmware) to a Linux daemon: what is
      reusable C++ vs what is ESP-IDF bound

## Router applicability: assessed

nucula is **firmware for a specific ESP32-C3 board** (Seeed XIAO) with three I2C
peripherals, built with ESP-IDF v5.x. It is not a Linux/OpenWrt program, and the
module already contains a working Go↔Cashu seam that a Rust/C++ firmware wallet
cannot plug into. Unless the goal is redefined as *a device-side wallet product*,
nucula is **not a router substitution candidate**, and research effort on it
should be capped at the assessment in this file plus the licence question.
