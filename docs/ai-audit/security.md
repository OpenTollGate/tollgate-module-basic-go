# AI Audit: Cashu Security Review

## Purpose
Run this prompt with an AI agent to audit for Cashu-specific vulnerabilities.

## Prompt

You are performing a security audit of a Cashu payment gateway.

### Attack Surface
1. HTTP API (port 2121) -- accepts Cashu tokens from untrusted clients
2. Mint communication -- HTTP client to external Cashu mints
3. Proof storage -- BoltDB with ecash proofs (money)
4. Lightning payments -- melt operations to external mints

### Checks

#### 1. Token Validation
- [ ] Are all Cashu tokens validated before processing?
- [ ] Is the token prefix checked (cashuA/cashuB)?
- [ ] Are token lengths bounded (no empty/oversized tokens)?
- [ ] Is DLEQ verification performed when proofs include DLEQ?
- [ ] Are proofs checked against the correct keyset (not just active)?

#### 2. Double-Spend Prevention
- [ ] Are proofs marked as pending BEFORE the swap/melt?
- [ ] Are proofs deleted from available AFTER successful operation?
- [ ] Is there a race condition window between select and delete?
- [ ] Does the wallet handle "proof already spent" errors from mint?

#### 3. Input Sanitization
- [ ] Are HTTP request bodies size-limited (io.LimitReader)?
- [ ] Are mint URLs normalized (trailing slashes)?
- [ ] Are error responses sanitized (no internal details leaked)?

#### 4. Denial of Service
- [ ] Is there a rate limiter on POST endpoints?
- [ ] Are HTTP client timeouts set on mint connections?
- [ ] Are response bodies from mints size-limited?

### Report format
For each finding:
    SEVERITY: [CRITICAL/HIGH/MEDIUM/LOW]
    FINDING: <description>
    LOCATION: <file:line>
    FIX: <recommended fix>

### Files to examine
- src/main.go -- HTTP handlers
- src/tollwallet/tollwallet.go -- Wallet operations
- src/merchant/merchant.go -- Payment flow
- src/merchant/lightning.go -- Lightning operations
