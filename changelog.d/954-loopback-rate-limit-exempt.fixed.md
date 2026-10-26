- **The router no longer rate-limits its own localhost installer, so the
  readable `starting` refusal wins again.** `registerAPIHandlers` wires
  `RateLimitMiddleware` outside `requireStarted`, so the per-IP limiter
  (10 req/min, burst 10) ran before the startup gate: while the merchant was
  being constructed the installer's 3 s poll of `127.0.0.1:2121` (~20 req/min)
  tripped it, and the probe received `429 {"error":"rate limit exceeded"}`
  instead of `503 {"code":"starting"}` — a correct install was reported as
  "Setup failed" on 2026-10-16. Loopback callers are now exempt from the
  limiter; every real client's budget is unchanged, the route ordering is
  unchanged, and the exemption is decided from the CONNECTION, never from
  `X-Real-Ip`/`X-Forwarded-For` — a header a caller controls must not be able
  to mint an unlimited-rate bucket (the trust boundary of #808's loopback XFF
  finding). Regression tests drive the real route table with the gate closed:
  a loopback burst of 25 probes gets 25 starting refusals, `::1` is exempt, a
  LAN caller past its budget still gets the 429, and neither a spoofed
  forwarded header nor a forwarded header on a genuinely-local caller changes
  the decision (#953; installer-side workaround shipped in tollgate-installer
  rc33)
  ([#954](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/954)).
