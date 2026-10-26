# Follow-up (POST-RELEASE): loopback rate-limit vs the startup gate

Status: **DEFERRED — after the current release.** Do not land this before the
0.6.0 line ships (operator decision 2026-10-16).

## The bug

The installer's health probe and the module's own rate limiter collide, and the
collision makes a slow-but-fine install look like a dead money path.

Route wiring (`src/main.go`, `registerAPIHandlers`):

```go
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    RateLimitMiddleware(CorsMiddleware(requireStarted(HandleRoot)))(w, r)
})
```

`RateLimitMiddleware` runs **before** `requireStarted`, and its default is
**10 requests/min, burst 10, per IP, with no loopback exemption**
(`src/main.go:43-71`):

```go
rpm := 10
if v := os.Getenv("TOLLGATE_RATE_LIMIT_RPM"); v != "" { ... }
limiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm)
...
if !getIPLimiter(ip).Allow() {
    w.Header().Set("Retry-After", "6")
    http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
    return
}
```

The TollGate **installer** polls `http://127.0.0.1:2121/` every 3 s while it
waits for the merchant to come up (~20 req/min). That exceeds 10/min, so the
router rate-limits its **own localhost installer**: the probe receives
`429 {"error":"rate limit exceeded"}`, which is neither the advertisement nor the
readable `503 {"code":"starting"}` refusal from `src/startup_gate.go`.

## The failure it caused (measured)

On 2026-10-16, an operator install with the router's upstream down produced:

```
Setup failed
TollGate API is UP on :2121 but returned neither a pricing advertisement nor a
starting refusal (an empty or unreadable body — the money path is not answering).
... WARN Money path is up but the merchant is still initializing: refused with an
    explicit starting state ... stage="initializing the merchant (mint probes + wallet)"
... WARN Rate limit exceeded ip=127.0.0.1   (repeated every ~5 s)
```

The merchant was correctly in its `starting` state the whole time (the install
had succeeded). The `RateLimitMiddleware` won over `requireStarted`, so some
probes got the 429 body instead of the `starting` refusal, and the installer's
classifier read that as "unknown".

The **installer side** of this was fixed in `tollgate-installer` rc33 (it now
treats a rate-limited/empty probe as retryable and only fails when the merchant
was never once seen starting). That fix is a workaround, not the root cause.

## The fix (do this after the release)

Make the readable `starting` refusal win over the limiter for the router's own
loopback caller. Preferred, smallest change: **exempt loopback (`127.0.0.1` /
`::1`) from `RateLimitMiddleware`** — the local installer and the router's own
health probes are not the untrusted clients this limiter exists to bound.

```go
func RateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ip := getIP(r)
        if ip == "127.0.0.1" || ip == "::1" { // the router's own probes/installer
            next(w, r)
            return
        }
        if !getIPLimiter(ip).Allow() { ... }
        next(w, r)
    }
}
```

Alternatives considered:
- Reorder to `requireStarted(...)` **outside** `RateLimitMiddleware`, so a
  starting gate always answers the readable refusal. More invasive (touches
  ordering for every gated route); the loopback exemption is narrower.
- Raise the default RPM. Rejected: it weakens the limiter for real clients and
  does not remove the collision.

## Tests to add with the fix

- A loopback request during the starting window gets the `503 {"code":"starting"}`
  body even under a burst that would trip the limiter for a non-loopback IP.
- A non-loopback IP still gets `429 {"error":"rate limit exceeded"}` past the
  budget (the limiter is not disabled).

## Why it is deferred

No new module RC is wanted in the current release line (operator decision). The
installer rc33 workaround makes the shipped combination behave correctly, so the
root-cause fix can land deliberately afterwards rather than re-cutting a module RC
now.
