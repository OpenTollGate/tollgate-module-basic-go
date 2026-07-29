# AI Audit: NUT Spec Compliance

## Purpose
Run this prompt with an AI agent to audit for Cashu NUT specification compliance.

## Prerequisites
- greatspectations spec quotes in place (see specquotes.toml)
- NUT specs cloned: git clone https://github.com/cashubtc/nuts.git nuts
- Run make/speccheck.sh first to get the drift baseline

## Prompt

You are auditing the TollGate Cashu payment gateway for NUT specification
compliance. The codebase is at the current working directory.

### Step 1: Inventory
Read specquotes.toml and list all NUT spec quotes found in the source.
For each, note which NUT it references, which file/function, and whether
the implementation matches.

### Step 2: Coverage gaps
Compare implemented NUTs against NUT 00-20+. For each NUT with NO spec
quote: is it implemented? Should it have a quote? List with file:line.

### Step 3: Compliance check
For each implemented NUT, verify: HTTP paths match spec, field names match
exactly, error codes match, state machines match.

### Step 4: Report
Output a markdown table: NUT | Status | Gaps | File References

### Files to examine
- src/tollwallet/tollwallet.go
- src/merchant/merchant.go
- src/main.go
- specquotes.toml
- nuts/
