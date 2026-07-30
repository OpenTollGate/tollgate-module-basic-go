# AI Audit: Cashu Security Review

## Purpose
Run this prompt with an AI agent to audit for Cashu-specific vulnerabilities.

## Prompt

You are performing a security audit of a Cashu payment gateway.

### Checks

#### Token Validation
- [ ] Tokens validated before processing?
- [ ] Prefix checked (cashuA/cashuB)?
- [ ] Lengths bounded?
- [ ] DLEQ verification when present?
- [ ] Correct keyset per proof?

#### Double-Spend Prevention
- [ ] Proofs marked pending before swap/melt?
- [ ] Deleted after successful operation?
- [ ] Race window between select and delete?
- [ ] "Proof already spent" handled?

#### Input Sanitization
- [ ] HTTP bodies size-limited?
- [ ] Mint URLs normalized?
- [ ] Error responses sanitized?

#### DoS Protection
- [ ] Rate limiter on POST?
- [ ] HTTP timeouts on mint connections?
- [ ] Response bodies size-limited?

### Report format
    SEVERITY: [CRITICAL/HIGH/MEDIUM/LOW]
    FINDING: description
    LOCATION: file:line
    FIX: recommendation

### Files
- src/main.go, src/tollwallet/tollwallet.go, src/merchant/merchant.go
