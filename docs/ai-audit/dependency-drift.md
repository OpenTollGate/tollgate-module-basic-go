# AI Audit: Dependency & Drift Check

## Purpose
Run this prompt with an AI agent to audit dependency health and drift.

## Prompt

You are auditing dependency health and drift in a multi-module Go workspace.

### Step 1: Dependency sync
Run `python3 tests/contract/check-deps-sync.py` and report any drift.
For each drifted dependency:
- What is the highest version in use?
- Which modules are behind?
- Are there known CVEs in the older versions?

### Step 2: Spec drift
Run `make/speccheck.sh` and report:
- Any spec quotes that no longer match the NUT spec text
- Coverage gaps (NUTs implemented but without spec quotes)

### Step 3: Vulnerability scan
For each direct dependency, check if there are known CVEs:
- `govulncheck ./...` if available
- Cross-reference with Dependabot alerts
- Check golang.org/x/crypto, x/net, x/sys versions specifically

### Report format
    DEPENDENCY DRIFT: [count] dependencies with version drift
    SPEC DRIFT: [count] spec quotes out of sync
    VULNERABILITIES: [count] known CVEs in current dependency tree
    HIGHEST RISK: <dependency name and version with most CVEs>
