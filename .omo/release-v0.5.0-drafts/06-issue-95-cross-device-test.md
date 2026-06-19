<!--
Target: New issue on https://github.com/OpenTollGate/physical-router-test-automation
Title: "Test: cross-device QR scanner behavior matrix (regression-guard tollgate-module-basic-go#95)"
Labels: test, captive-portal
-->

## Why

`tollgate-module-basic-go#95` asks for the QR scanner to work in the captive
portal. Reading the code shows this is constrained by an architecture
tradeoff: the captive portal splash is served over HTTP via NoDogSplash
(camera API requires secure context), while the LuCI admin SPA is served over
HTTPS via uhttpd (camera works). Before settling on a direction (accept
constraint, dual-mode portal, or real-cert HTTPS), we need **actual
device-matrix evidence** of current behavior, not just spec-reading.

Some platforms (e.g. some WebKit builds) treat captive portal pages served
from RFC1918 IPs as a de-facto secure context. We should know whether that
applies to the platforms our users actually have.

## Goal

Produce a one-page device × behavior matrix that records, for each of:
{ captive portal mini-browser, normal browser hitting the HTTPS admin URL },
on each of: { iOS, iPadOS, Android, macOS, Windows, Linux }, whether the QR
scanner (a) renders, (b) prompts for camera permission, (c) successfully
decodes a test Cashu token.

This is evidence-gathering, not pass/fail.

## Scope

### Test setup

Use the tag-readiness two-router fleet (alpha + beta GL-MT3000 on OpenWrt
24.10.4, both at the pinned commit under test). Beta is the captive portal
router; alpha can serve as the upstream.

Pre-conditions:
- Beta has `tollgate ssl apply --yes` run so HTTPS is enabled on uhttpd for
  the LuCI admin URL (`https://tollgate.lan/` or similar).
- Beta's captive portal splash is reachable on the standard NoDogSplash HTTP
  intercept (`http://<gateway-ip>/` or whichever IP the OS captive portal
  mini-browser opens).
- One real test Cashu token minted at `nofee.testnut.cashu.space`, encoded as
  a QR (use `qrencode -t PNG -o token.png "cashu..."` locally) — displayed on
  a second device's screen for the camera to read.

### Matrix

For each **device** × **entrypoint**:

| Device | OS version | Browser | Entry point | QR widget renders? | Camera permission prompt? | Decodes token? | Notes |
|---|---|---|---|---|---|---|---|
| iPhone | iOS 17 | Captive Web Portal | `http://192.168.244.1/` (captive) | ? | ? | ? | |
| iPhone | iOS 17 | Safari | `https://tollgate.lan/` (admin) | ? | ? | ? | |
| iPad | iPadOS 17 | Captive Web Portal | captive | ? | ? | ? | |
| Pixel | Android 14 | Captive portal login | captive | ? | ? | ? | |
| Pixel | Android 14 | Chrome | `https://tollgate.lan/` | ? | ? | ? | |
| MacBook | macOS 14 | Safari (captive popup) | captive | ? | ? | ? | |
| MacBook | macOS 14 | Safari | `https://tollgate.lan/` | ? | ? | ? | |
| ThinkPad | Windows 11 | Edge (captive popup) | captive | ? | ? | ? | |
| ThinkPad | Windows 11 | Edge | `https://tollgate.lan/` | ? | ? | ? | |
| NUC | Ubuntu 24.04 | Firefox | captive (NetworkManager popup) | ? | ? | ? | |
| NUC | Ubuntu 24.04 | Firefox | `https://tollgate.lan/` | ? | ? | ? | |

### Per-cell protocol

1. Connect the device to beta's LAN (or trigger the captive portal by
   associating to the `TollGate-*` SSID).
2. For captive entries: let the OS open the captive portal mini-browser
   automatically — do **not** manually navigate to the URL with a regular
   browser.
3. For admin entries: open the listed URL in the regular browser, accept the
   self-signed cert warning if prompted, navigate to the page that has the QR
   scanner.
4. Tap the QR scan control. Record:
   - **Renders**: does the scanner widget appear at all?
   - **Permission prompt**: does the OS / browser ask for camera permission?
   - **Decodes**: hold the test QR in front of the camera. Within ~10 s, does
     the token make it into the input field?
5. Capture the JS console output for any failures
   (`getUserMedia is not a function`, `Permission denied`,
   `NotSupportedError`, etc.).

### Output

- One markdown table per device class under
  `docs/findings/qr-scanner-matrix-<commit>.md`.
- Screenshots where possible (no sensitive values; #116 covers masking).
- A companion comment on `tollgate-module-basic-go#95` linking the matrix
  with a one-paragraph verdict on which of the three architecture options the
  evidence supports.

## Out of scope

- Designing or implementing the dual-mode portal or real-cert HTTPS flow —
  that's a code change on `tollgate-module-basic-go`, tracked separately.
- Masking sensitive values in screenshots — that's #116.
- Performance / load testing of the QR scanner.

## Acceptance

- One filled-in matrix for at least iOS, Android, macOS, and one Linux.
- Companion comment on #95 with the verdict.
- The test methodology committed under `docs/findings/` so future runs after
  architecture changes can be diffed.
