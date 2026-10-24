package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// These tests pin the fix for the 2026-10-16 "Setup failed" incident (#953):
// registerAPIHandlers wires RateLimitMiddleware OUTSIDE requireStarted, so the
// per-IP limiter (10 req/min, burst 10) used to run before the startup gate.
// The installer polls 127.0.0.1:2121 every 3 s (~20 req/min) while it waits for
// the merchant, so the router rate-limited its own localhost installer: the
// probe got `429 {"error":"rate limit exceeded"}` instead of the readable
// `503 {"code":"starting"}` refusal, and a correct install was reported as a
// dead money path.
//
// They drive the route table registerAPIHandlers actually builds (not a
// hand-wired chain) at "/" — the route the installer probes — with the startup
// gate closed, which is exactly the incident's state: the API is up, the
// merchant is still being constructed.

// newRateLimitTestMux installs the real route table against a fresh, CLOSED
// startup gate and empty limiter buckets, and restores both afterwards.
//
// Both are process-wide state, so these tests are deliberately not parallel
// (the package has no t.Parallel anywhere) and every one of them goes through
// this helper rather than mutating the globals itself.
func newRateLimitTestMux(t *testing.T) *http.ServeMux {
	t.Helper()

	prevGate := apiStartup
	apiStartup = newStartupGate() // closed: the merchant is still being constructed
	resetIPLimiters()

	mux := http.NewServeMux()
	registerAPIHandlers(mux)

	t.Cleanup(func() {
		apiStartup = prevGate
		resetIPLimiters()
	})
	return mux
}

// resetIPLimiters empties the per-IP bucket map so a test starts from a full
// budget regardless of what ran before it in the package.
func resetIPLimiters() {
	ipLimitersMu.Lock()
	defer ipLimitersMu.Unlock()
	ipLimiters = make(map[string]*rate.Limiter)
}

// doRequest performs one request against the mux with the given RemoteAddr and
// no client timeout (every handler under test answers in-process).
func doRequest(t *testing.T, mux *http.ServeMux, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// requireStartingRefusal asserts the response is the startup gate's readable
// refusal — the answer the installer can actually act on.
func requireStartingRefusal(t *testing.T, rec *httptest.ResponseRecorder, context string) {
	t.Helper()

	require.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"%s: got %d (%q), want the explicit starting refusal %d — the limiter must not win over the startup gate for loopback",
		context, rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusServiceUnavailable)

	var refusal lightningInvoiceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal),
		"%s: body is not the JSON a portal/installer parses: %q", context, rec.Body.String())
	assert.Equal(t, codeStarting, refusal.Code, "%s: refusal code", context)
	assert.Equal(t, 0, refusal.Status, "%s: starting refusal must not look like a successful answer", context)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"),
		"%s: the starting refusal carries no Retry-After, so a caller cannot know when to come back", context)
}

// TestRateLimitMiddleware_LoopbackBurstGetsTheStartingRefusal is the incident
// regression test. The installer's real cadence is ~20 requests/min; this sends
// 25 back-to-back from 127.0.0.1, well past the 10-request burst. Every single
// one must be the readable starting refusal, and not one may be a 429.
//
// A mutant that drops the exemption (or keys it on the wrong thing) fails here
// on the first response past the burst.
func TestRateLimitMiddleware_LoopbackBurstGetsTheStartingRefusal(t *testing.T) {
	mux := newRateLimitTestMux(t)

	const requests = 25 // > burst (10) and > the installer's ~20/min

	for i := 0; i < requests; i++ {
		rec := doRequest(t, mux, "127.0.0.1:41234", nil)
		// The status assertion itself rules out 429: a rate-limited probe would
		// answer 429 here, which is what the incident's classifier read as
		// "unknown".
		requireStartingRefusal(t, rec, "loopback probe")
	}

	// The exemption must not leave the loopback caller's address in the bucket
	// map: an exempt path that still allocates state would let a local process
	// grow the map for free (and the map is capped, so that is a real cost).
	ipLimitersMu.Lock()
	buckets := len(ipLimiters)
	ipLimitersMu.Unlock()
	assert.Equal(t, 0, buckets, "the exempt loopback path allocated %d limiter bucket(s)", buckets)
}

// TestRateLimitMiddleware_IPv6LoopbackIsExempt covers ::1 — an installer or
// health probe that resolves localhost to IPv6 is the same caller, and a
// predicate that only compared the string "127.0.0.1" would let it through
// unexempted.
func TestRateLimitMiddleware_IPv6LoopbackIsExempt(t *testing.T) {
	mux := newRateLimitTestMux(t)

	for i := 0; i < 15; i++ {
		rec := doRequest(t, mux, "[::1]:41234", nil)
		requireStartingRefusal(t, rec, "IPv6 loopback probe")
	}
}

// TestRateLimitMiddleware_NonLoopbackStillRateLimited is the other half of the
// contract: the exemption must not disable the limiter. A LAN caller past the
// budget gets the 429, byte for byte the body the shipped code sent.
func TestRateLimitMiddleware_NonLoopbackStillRateLimited(t *testing.T) {
	mux := newRateLimitTestMux(t)

	const burst = 10 // rate.NewLimiter(rate.Every(time.Minute/rpm), rpm) with rpm=10

	// The first `burst` requests are allowed through the limiter and then
	// refused by the startup gate — proving the order is still
	// limiter-then-gate for a real client, and that the 429s below come from
	// the limiter and not from the gate.
	for i := 0; i < burst; i++ {
		rec := doRequest(t, mux, "192.168.1.50:41234", nil)
		requireStartingRefusal(t, rec, "LAN caller inside the budget")
	}

	rec := doRequest(t, mux, "192.168.1.50:41234", nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a non-loopback caller past the budget got %d (%q), want %d — the exemption must not disable the limiter",
		rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusTooManyRequests)
	assert.Equal(t, "6", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), "rate limit exceeded")

	// A different client still has its own budget: the limit is per-IP, so the
	// exhausted bucket must not be a global off switch.
	other := doRequest(t, mux, "192.168.1.51:41234", nil)
	requireStartingRefusal(t, other, "second LAN caller, own budget")
}

// TestRateLimitMiddleware_ForwardedHeaderCannotClaimTheExemption is the
// anti-bypass test, and it is the reason the exemption is decided on the
// CONNECTION rather than on getIP().
//
// getIP() honours X-Real-Ip / X-Forwarded-For from a loopback peer, because a
// proxy on the box forwards a real customer's address that way. Deciding trust
// from that value would be circular: any LAN caller could send
// `X-Real-Ip: 127.0.0.1`, be read as loopback for the purpose of the exemption,
// and mint itself an unlimited-rate bucket — the client-controlled-identity
// defect class tracked in #808. The header must therefore buy a non-loopback
// caller nothing.
func TestRateLimitMiddleware_ForwardedHeaderCannotClaimTheExemption(t *testing.T) {
	mux := newRateLimitTestMux(t)

	const burst = 10
	spoof := map[string]string{"X-Real-Ip": "127.0.0.1"}

	for i := 0; i < burst; i++ {
		rec := doRequest(t, mux, "192.168.1.50:41234", spoof)
		requireStartingRefusal(t, rec, "LAN caller with a spoofed X-Real-Ip inside the budget")
	}

	rec := doRequest(t, mux, "192.168.1.50:41234", spoof)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a LAN caller claimed the loopback exemption with X-Real-Ip: 127.0.0.1 and got %d (%q) past the budget, want %d",
		rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusTooManyRequests)

	// The same bypass through X-Forwarded-For must not work either.
	xff := map[string]string{"X-Forwarded-For": "::1"}
	rec = doRequest(t, mux, "192.168.1.50:41234", xff)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a LAN caller claimed the loopback exemption with X-Forwarded-For: ::1 and got %d (%q) past the budget, want %d",
		rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusTooManyRequests)
}

// TestRateLimitMiddleware_ForwardedHeaderCannotExhaustTheExemption is the
// mirror image: a REAL loopback caller that carries a forwarded header must
// stay exempt. The router's own plumbing sets these headers on some local
// requests (getIP reads them for a loopback peer on purpose), and a predicate
// that took the header's value as the caller's identity would hand the
// exemption to whichever address the header named — intermittently
// rate-limiting the installer again, which is the bug being fixed.
//
// Each case sends 15 requests (> burst 10) through ONE named address, so the
// pre-fix code fails it: the header's address gets its own exhausted bucket
// while the connection is loopback.
func TestRateLimitMiddleware_ForwardedHeaderCannotExhaustTheExemption(t *testing.T) {
	const requests = 15 // > burst (10), so the header-named bucket is exhausted

	for name, headers := range map[string]map[string]string{
		"X-Real-Ip: a LAN customer address":        {"X-Real-Ip": "192.168.1.50"},
		"X-Forwarded-For: a LAN customer chain":    {"X-Forwarded-For": "203.0.113.9, 192.168.1.50"},
		"X-Real-Ip: loopback, set by the caller":   {"X-Real-Ip": "127.0.0.1"},
		"X-Forwarded-For: IPv6 loopback and a LAN": {"X-Forwarded-For": "::1"},
	} {
		t.Run(name, func(t *testing.T) {
			mux := newRateLimitTestMux(t)

			for i := 0; i < requests; i++ {
				rec := doRequest(t, mux, "127.0.0.1:41234", headers)
				requireStartingRefusal(t, rec, "loopback caller with a forwarded header")
			}
		})
	}
}
