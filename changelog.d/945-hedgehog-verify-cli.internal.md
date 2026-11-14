- **Add `cmd/hedgehog-verify`, the on-device spend verifier.** Cross-compiles
  cleanly for the router's `linux/arm64` and strips to ~5 MB. It takes its request
  as JSON on stdin or from a file, never on the command line, because the router's
  serial console wraps lines at roughly 80 characters — a verifier driven by argv
  would be unusable over serial.

  Exit codes are part of the contract: `0` valid, `1` not valid, `2` malformed
  input. A lane can distinguish "the spend failed" from "the input was bad"
  without parsing JSON. `expect_failure` inverts the sense so a negative test
  reads as a pass.

  Tested **as a binary**, not only as a library, in both directions: the completed
  2-of-2 spend verifies, and the payer's half alone does not — the latter asserted
  twice, once via `expect_failure` and once without it, so the flag cannot be what
  produces the result.
