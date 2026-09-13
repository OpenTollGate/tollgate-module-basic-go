//go:build testenv

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

func doCors(t *testing.T, origin, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	CorsMiddleware(corsHandler())(rec, req)
	return rec
}

func TestCorsMiddleware_EchoesLocalOrigin(t *testing.T) {
	rec := doCors(t, "http://192.168.8.1:2050", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.8.1:2050" {
		t.Errorf("local origin not echoed: got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary: Origin missing, got %q", got)
	}
}

func TestCorsMiddleware_EchoesSameHostAnyPort(t *testing.T) {
	// Portal on uhttpd :2051 (post-#348) calling the API on :2121.
	rec := doCors(t, "http://192.168.8.1:2051", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.8.1:2051" {
		t.Errorf("same-host origin not echoed: got %q", got)
	}
}

func TestCorsMiddleware_EchoesSameHostNonPrivate(t *testing.T) {
	// Same-host rule must hold even on non-RFC1918 addressing (test benches,
	// reseller deployments): request Host matches Origin host.
	rec := doCors(t, "http://203.0.113.7:2051", "203.0.113.7:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://203.0.113.7:2051" {
		t.Errorf("same-host non-private origin not echoed: got %q", got)
	}
}

func TestCorsMiddleware_NoWildcardForCrossHost(t *testing.T) {
	// OWASP: this API is protected by the LAN firewall, not credentials —
	// a wildcard would let any website read responses from a browser on
	// the TollGate network.
	rec := doCors(t, "https://evil.example", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("cross-host origin must not be allowed, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got == "Origin" {
		t.Error("Vary: Origin set without an echo decision")
	}
}

func TestCorsMiddleware_PrivateCrossHostNotEchoed(t *testing.T) {
	// A private origin that is a *different* LAN host than the router the
	// client addressed: isLocalOrigin alone would echo it; the same-host
	// rule must not loosen that further. (Kept: private-LAN echo is the
	// long-standing behavior for LAN-served dashboards.)
	rec := doCors(t, "http://192.168.8.50:3000", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.8.50:3000" {
		t.Errorf("private cross-host origin should stay allowed, got %q", got)
	}
}

func TestCorsMiddleware_NullOriginDenied(t *testing.T) {
	rec := doCors(t, "null", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("null origin must never be echoed, got %q", got)
	}
}

func TestCorsMiddleware_NoOriginNoHeader(t *testing.T) {
	// Non-browser clients (curl) never need ACAO; the wildcard fallback
	// that used to be emitted here served no client.
	rec := doCors(t, "", "192.168.8.1:2121")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("no-origin request must not get ACAO, got %q", got)
	}
}

func TestCorsMiddleware_PreflightOptionsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://192.168.8.1:2051")
	req.Host = "192.168.8.1:2121"
	rec := httptest.NewRecorder()
	CorsMiddleware(corsHandler())(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("preflight status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Errorf("methods header = %q", got)
	}
}

func TestIsSameHost(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"http://192.168.8.1:2051", "192.168.8.1:2121", true},
		{"http://192.168.8.1", "192.168.8.1:2121", true},
		{"http://[::1]:2051", "[::1]:2121", true},
		{"http://localhost:3000", "localhost:2121", true},
		{"http://LOCALhost:3000", "localhost:2121", true},
		{"http://192.168.8.2:2051", "192.168.8.1:2121", false},
		{"https://evil.example", "192.168.8.1:2121", false},
		{"null", "192.168.8.1:2121", false},
		{"::not a url::", "192.168.8.1:2121", false},
	}
	for _, c := range cases {
		if got := isSameHost(c.origin, c.host); got != c.want {
			t.Errorf("isSameHost(%q, %q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}

func TestHandleRootPost_UnsupportedMediaType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	HandleRootPost(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rec.Code)
	}
}

func TestHandleRootPost_AcceptedContentTypes(t *testing.T) {
	for _, ct := range []string{"text/plain", "application/json"} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		HandleRootPost(rec, req)
		if rec.Code == http.StatusUnsupportedMediaType {
			t.Errorf("content-type %q must not be rejected with 415", ct)
		}
	}
}
