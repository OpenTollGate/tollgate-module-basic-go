#!/usr/bin/env python3
"""Build the cross-family review prompt for OpenTollGate PR #959 from disk.

Why a script (SKILL pitfall 22): assembling a long prompt inline in a shell
argument gets truncated by the harness's own output cap, so an "exhaustive
inventory" silently ships partial. Build it from disk, then re-read its size.

Why inlined (pitfall 22): the oneshot harness has NO tools. Any instruction to
"read file X" comes back as tool-call markup, not a review. Every excerpt the
reviewer needs is pasted verbatim below.

Why narrow (locked-window recipe 2026-09-29): the router's `ours` window is
locked at weekly 100 %, so only small prompts are admitted. Each question
carries the smallest excerpt that makes it decidable, and demands a closed
VERDICT line rather than free-form analysis.
"""
import subprocess
import sys
from pathlib import Path

REPO = Path("/home/c08r4d0r/worktrees/tg-vendor-ie")
BASE = "upstream/main"


def diff_of(path: str) -> str:
    return subprocess.run(
        ["git", "diff", f"{BASE}...HEAD", "--", path],
        cwd=REPO, capture_output=True, text=True,
    ).stdout


def show(path: str, start: int, end: int) -> str:
    """Print a line range of the file as it stands at HEAD (1-indexed)."""
    lines = (REPO / path).read_text().splitlines()
    return "\n".join(lines[start - 1:end])


parts: list[str] = []

parts.append("""You are a cold cross-family reviewer. You have NO tools — everything you may
use is inlined below. Do not attempt to read files or run commands.

CONTEXT
TollGate (OpenTollGate/tollgate-module-basic-go) turns an OpenWrt router into a
Cashu-paid gateway. A neighbour advertises itself in an 802.11 vendor-specific
information element (IE), which the scan parser reads to identify a candidate.
PR #959 wires the producer for pricing fields that were declared in four data
models but hard-zeroed at the scan boundary.

AUTHORITY MODEL (violation = BLOCKER):
- The vendor IE is UNSIGNED; OUI identification only selects a candidate for a
  signed probe.
- The signed kind-10021 advertisement on :2121 is the AUTHORITY for price. Zero
  pricing in the IE means "not advertised", NEVER "free".
- Identification must survive with pricing TLVs absent or dropped.

GIVEN (measured 2026-10-16, GL-MT6000): `iwinfo scan` prints NO information
elements; `iw dev X scan` prints them DECODED as named fields and prints a
vendor element as `Vendor specific: <colon-hex>`; iw STRIPS the element ID
(0xDD) and length byte from that body.

RULES:
R1 IE layout is [0xDD, length, body...]; length counts BODY bytes, single byte.
R2 Element <= 255 bytes; identification (pubkey) outranks pricing, so pricing
   is dropped before the pubkey or the cap.
R3 No vendor element => NOT IsTollGate.
R4 Placeholder SSID ("unknown", "(hidden)") must not become selectable.
""")

parts.append("""
=== EXCERPT A — scanner.go, the vendor-element line + re-framing ===
""")
parts.append("```go")
parts.append(show("src/wireless_gateway_manager/scanner.go", 362, 382))
parts.append("```")

parts.append("""
=== EXCERPT B — scanner.go, appendScanEntry (admission) + buildNetworkInfo ===
""")
parts.append("```go")
parts.append(show("src/wireless_gateway_manager/scanner.go", 411, 480))
parts.append("```")

parts.append("""
=== EXCERPT C — scanner.go, mergeScanResults (folds the iw view into iwinfo) ===
""")
parts.append("```go")
parts.append(show("src/wireless_gateway_manager/scanner.go", 203, 248))
parts.append("```")

parts.append("""
=== EXCERPT D — vendor_element_manager.go, pricing TLVs encode + parse ===
""")
parts.append("```go")
parts.append(show("src/wireless_gateway_manager/vendor_element_manager.go", 18, 32))
parts.append("...")
parts.append(show("src/wireless_gateway_manager/vendor_element_manager.go", 80, 100))
parts.append("...")
parts.append(show("src/wireless_gateway_manager/vendor_element_manager.go", 155, 170))
parts.append("```")

parts.append("""
=== EXCERPT E — main.go, the flag wiring ===
""")
parts.append("```go")
parts.append(show("src/main.go", 586, 596))
parts.append("```")

parts.append("""
=== TEST INVENTORY (do not review in detail) ===
scanner_vendor_ie_test.go: iw-format parse, foreign OUI ignored, no-pricing-TLV
still identifies, no element => not TollGate, hidden-SSID refusal, placement.
scanner_merge_test.go: merge enrich/keep/gap-fill/identity, iw-failure nil.
vendor_ie_pricing_test.go: round-trip incl. 65535, pricing dropped not overflow.

=== CLAIMED VERIFICATION ===
Live scan 28736 B => 12 networks, 0 TollGates (correct: no neighbour on this
build). Real scan + 1 injected element => 16 networks, 1 TollGate with
price_per_step=7, step_size=1000 recovered exactly.

=== ANSWER EXACTLY THESE FIVE CLOSED QUESTIONS, THEN STOP ===

Q1 (re-framing). iw strips 0xDD and the length byte, so the code re-adds them
as `0xDD, byte(len(b))`. Is that correct under R1 for every body length the
producer can emit, including >255 bytes? If a body exceeded 255 the length byte
would wrap — state whether the code can reach that.

Q2 (admission). `appendScanEntry` refuses SSIDs "", "(hidden)" and "unknown".
Under R3/R4, can this drop a network that SHOULD have been admitted? Name any
legitimate SSID that would be lost.

Q3 (merge). Is `mergeScanResults` sound? Specifically: can it lose a record,
duplicate one, or attribute one BSS's vendor element to another? Note the
element-derived fields are taken from the second (iw) view.

Q4 (pricing loss). Under R2, does the encoder drop pricing BEFORE it can
overflow the element or displace the pubkey? Is the `len(body)+4 > 255` guard
the right boundary, or off by a byte?

Q5 (authority). Does anything in the code let an UNSIGNED vendor element set a
price that a caller could treat as authoritative, or present a zero price as
free? Quote the line if so.

COULD_NOT_VERIFY: list anything you could not decide from these excerpts.

OUTPUT CONTRACT — obey exactly:
reviewer_model: deepseek-flash
Q1: <YES|NO|CANNOT_DETERMINE> - <one line>
Q2: ...
Q3: ...
Q4: ...
Q5: ...
COULD_NOT_VERIFY: <one line, or NONE>
VERDICT: <APPROVED | REQUEST_CHANGES> - <counts by severity>
Reason in at most 6 sentences total. Do NOT pad with analysis.
""")

prompt = "\n".join(p for p in parts if p)
Path("/tmp/pr959-review-prompt.txt").write_text(prompt)
print(f"prompt bytes: {len(prompt)}")
print(f"lines: {prompt.count(chr(10))}")
