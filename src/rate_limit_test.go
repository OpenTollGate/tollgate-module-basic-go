//go:build testenv

package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"
)

// The payment root handler's per-IP rate limit (10 RPM, burst 10) vs the test
// suites (#748): a test binary is one process whose suites share client IPs,
// so the budget fails them mid-run. The accommodation is the test-context
// bypass (see rateLimitTestBypass in main.go): on for the whole testenv-tagged
// binary, never compiled into a writer in the shipped one. These tests pin
// both halves — suites run unthrottled, and the production default still
// throttles when the bypass is off — plus the env override the deployed labs
// (cloud-lab compose, router-happy-path) already rely on.

// resetIPLimitersForTest empties the per-IP limiter cache so a case starts
// from a full, freshly-configured budget (the limiters are created lazily and
// read TOLLGATE_RATE_LIMIT_RPM only at creation), and again on cleanup so no
// case's residue throttles a later one.
func resetIPLimitersForTest(t *testing.T) {
	t.Helper()
	clear := func() {
		ipLimitersMu.Lock()
		ipLimiters = make(map[string]*rate.Limiter)
		ipLimitersMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func hammerRoot(t *testing.T, requests int, remoteAddr string) (passed, throttled int32) {
	t.Helper()

	var reached int32
	handler := RateLimitMiddleware(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reached, 1)
		w.WriteHeader(http.StatusOK)
	})
	recorder := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		handler(w, req)
		return w
	}

	var denied int32
	for i := 0; i < requests; i++ {
		if w := recorder(); w.Code == http.StatusTooManyRequests {
			atomic.AddInt32(&denied, 1)
			if got := w.Header().Get("Retry-After"); got == "" {
				t.Errorf("a 429 must carry Retry-After so suites can pace themselves (the router-happy-path contract), got none")
			}
		} else if w.Code != http.StatusOK {
			t.Fatalf("unexpected status %d from the wrapped handler", w.Code)
		}
	}
	return atomic.LoadInt32(&reached), atomic.LoadInt32(&denied)
}

// TestRateLimitTestModeRunsSuitesUnthrottled is the accommodation's headline:
// with the default testenv configuration, a burst four times the production
// per-minute budget from a single client IP reaches the handler every time.
func TestRateLimitTestModeRunsSuitesUnthrottled(t *testing.T) {
	resetIPLimitersForTest(t)

	if !rateLimitTestBypass {
		t.Fatal("the test-context bypass must be on by default under the testenv tag (000_test_env_testenv.go sets it)")
	}

	passed, throttled := hammerRoot(t, 40, "10.9.9.9:1234")
	if throttled != 0 || passed != 40 {
		t.Fatalf("the suites must run unthrottled in the test context: %d reached the handler, %d throttled (want 40/0)", passed, throttled)
	}
}

// TestRateLimitProductionDefaultStillThrottles pins what the shipped binary
// does: bypass off, ten requests pass, the eleventh is refused with the
// limiter's own semantics — and a different client IP still has its full
// budget, because the limit is per IP.
func TestRateLimitProductionDefaultStillThrottles(t *testing.T) {
	resetIPLimitersForTest(t)

	prev := rateLimitTestBypass
	rateLimitTestBypass = false
	t.Cleanup(func() { rateLimitTestBypass = prev })

	passed, throttled := hammerRoot(t, 12, "10.10.10.10:4321")
	if passed != 10 || throttled != 2 {
		t.Fatalf("the production default is 10 requests/minute per IP (burst 10): got %d passed / %d throttled, want 10/2", passed, throttled)
	}

	otherPassed, otherThrottled := hammerRoot(t, 3, "10.10.10.11:8765")
	if otherPassed != 3 || otherThrottled != 0 {
		t.Fatalf("the limit is per client IP — an untouched IP must keep its full budget: got %d/%d, want 3/0", otherPassed, otherThrottled)
	}
}

// TestRateLimitEnvOverrideStillServesDeployedLabs pins the accommodation the
// deployed labs already use (tests/cloud-lab compose sets it to 600; the
// router-happy-path harness documents it): with the bypass off and
// TOLLGATE_LIMIT_RPM raised, a suite-paced burst from one IP passes. The
// override must stay working — it is the only lever on a packaged router.
func TestRateLimitEnvOverrideStillServesDeployedLabs(t *testing.T) {
	resetIPLimitersForTest(t)

	prev := rateLimitTestBypass
	rateLimitTestBypass = false
	t.Cleanup(func() { rateLimitTestBypass = prev })
	t.Setenv("TOLLGATE_RATE_LIMIT_RPM", "600")

	// Fresh cache (the limiter reads the env only at creation), then a burst
	// far past the 10 RPM default but well inside the override.
	ipLimitersMu.Lock()
	ipLimiters = make(map[string]*rate.Limiter)
	ipLimitersMu.Unlock()

	passed, throttled := hammerRoot(t, 40, "10.11.12.13:9999")
	if throttled != 0 || passed != 40 {
		t.Fatalf("TOLLGATE_RATE_LIMIT_RPM=600 must let a 40-request burst through: got %d passed / %d throttled", passed, throttled)
	}
}
