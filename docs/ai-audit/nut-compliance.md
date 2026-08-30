# AI Audit: NUT Spec Compliance

## Purpose
Run this prompt with an AI agent to audit the TollGate codebase for
Cashu NUT specification compliance.

## Prerequisites
- greatspectations spec quotes in place (see `specquotes.toml`)
- NUT specs cloned: `git clone https://github.com/cashubtc/nuts.git nuts`
- Run `make/speccheck.sh` first to get the drift baseline

## Prompt

You are auditing the TollGate Cashu payment gateway for NUT specification
compliance. The codebase is at the current working directory.

### Step 1: Inventory
Read `specquotes.toml` and list all NUT spec quotes found in the source.
For each, note:
- Which NUT it references
- Which source file and function it's above
- Whether the implementation below matches the spec quote

### Step 2: Coverage gaps
Compare the implemented NUTs against the full NUT list (00-20+).
For each NUT that has NO spec quote:
- Is it implemented at all? (search for relevant code)
- Should it have a spec quote? (if the code touches the spec)
- List the gap with file:line reference

### Step 3: Compliance check
For each implemented NUT, verify:
- HTTP endpoint paths match the spec (e.g., `/v1/mint/quote/bolt11`)
- Request/response field names match exactly
- Error codes match the spec (e.g., NUT-02 code 10002)
- State machines match (UNPAID -> PAID -> ISSUED for mint quotes)

### Step 4: Report
Output a markdown table:

| NUT | Status | Gaps | File References |
|-----|--------|------|-----------------|
| 00  | Compliant | -- | src/tollwallet/tollwallet.go:117 |
| 03  | Partial | Missing output ordering | src/tollwallet/tollwallet.go:148 |
| 05  | Missing prefer_async | src/tollwallet/tollwallet.go:356 |

### Files to examine
- `src/tollwallet/tollwallet.go` -- Cashu wallet operations
- `src/merchant/merchant.go` -- Payment processing
- `src/main.go` -- HTTP endpoint handlers
- `specquotes.toml` -- Spec quote config
- `nuts/` -- NUT spec files (reference)

### Exit criteria
- Every NUT that the codebase touches has a spec quote
- Every spec quote has been verified against the actual spec text
- All compliance gaps are documented with file:line references
