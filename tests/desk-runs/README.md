# Desk runs — user-shaped hardware test records

A **desk run** is a test executed the way a user would experience the
product: a real router on an operator's desk, real install or upgrade,
real portal, real payment — not the labgrid lanes and not the offline
suites. This directory holds their records, in a fixed shape, so a
reviewer can trust what was tested, on what, and what came of it.

The habit this directory enforces (the "review club" rule):

> Every run DECLARES its device and environment (sanitized), STATES what
> ran, REPORTS each result as PASS / FAIL / NIT, and turns every NIT into
> a filed issue. A nit without an issue is a nit we decided to forget.

## Why sanitized records

Run reports quote logs and commands verbatim, and routers under test sit
on real networks with real credentials. Before committing a report:

- **Never** include an SSID, a WiFi passphrase, a Nostr private key, an
  identities.json body, or a Cashu token string (spent test tokens are
  worthless, but the habit of pasting them is not).
- Declare networks by role ("upstream WiFi STA, home network — redacted",
  "guest LAN 192.168.1.0/24"), not by name.
- Wallet value states are fine to state ("balance 0 sats, all mints
  test-class"); key material is not.

## The record format

Copy `template.md` (or scaffold with `./new-run.sh <device-slug>`):

1. **Environment, declared** — device model, target/arch, OpenWrt version,
   TollGate version(s) before/after with their origins (published artifact
   tag, or locally built with the exact git SHA + ldflags labeling), uplink
   shape, mint class, harness.
2. **Safety gates** — what was backed up before anything destructive, the
   value-scan verdict, where the backup lives (with hashes).
3. **What ran** — ordered steps with the essential commands (sanitized).
4. **Results** — each assertion PASS / FAIL / NIT with the evidence line.
5. **Findings** — every defect or oddity, each linked to its issue
   (`./file-nit.sh` creates them in the house shape).
6. **Artifacts** — paths and sha256s (local or committed evidence).
7. **Not tested, and why** — the honest list; a run that claims everything
   is a run nobody trusts.

## Tooling

- `new-run.sh <device-slug>` — scaffold a record pre-filled with date,
  the tree's git SHA, and the sanitization checklist.
- `file-nit.sh <run-record> <title> <evidence-file>` — open an issue on
  the repo carrying the run's environment declaration plus the evidence,
  so every filed defect names where it was seen.
- `scripts/desk/router-ssh.py`, `scripts/desk/router-scp.py` — pty-based
  helpers for routers whose root login is password-empty (bench devices):
  isolated known-hosts, non-interactive. Generic; no repo state inside.

## Precedents and neighbors

- `tests/vm-campaign/` — committed evidence logs for the VM lane
  (the pattern this directory follows for hardware desks).
- `tests/happy-path/` — the published-artifact regression suite (offline,
  stubbed ndsctl): desk runs are its hardware-side complement, not a
  replacement.
- `docs/rc-tester-guide.md` — the installer/upgrade flow desk runs walk.
