# AI Audit: Dependency & Drift Check

## Purpose
Run this prompt with an AI agent to audit dependency health and drift.

## Prompt

### Step 1: Dependency sync
Run python3 tests/contract/check-deps-sync.py. Report drift, highest
versions, modules behind, known CVEs.

### Step 2: Spec drift
Run make/speccheck.sh. Report quotes out of sync, coverage gaps.

### Step 3: Vulnerability scan
Run govulncheck if available. Cross-reference Dependabot alerts.
Check x/crypto, x/net, x/sys specifically.

### Report format
    DEPENDENCY DRIFT: count
    SPEC DRIFT: count
    VULNERABILITIES: count
    HIGHEST RISK: dependency with most CVEs
