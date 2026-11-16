package wireless_gateway_manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the real HTTP client against local servers. No public endpoint,
// router command or system DNS resolver is used by these tests.
func TestProbeUpstreamInternetHTTPResponses(t *testing.T) {
	tests := []struct {
		name              string
		primaryFails      bool
		primaryStatus     int
		primaryTruncated  bool
		fallbackStatus    int
		fallbackBody      string
		redirect          bool
		truncated         bool
		want              ConnectivityResult
		wantFallbackCalls int
	}{
		{name: "primary 204", primaryStatus: 204, want: ConnectivityResult{true, "ok"}},
		{name: "Firefox fallback exact success", primaryFails: true, fallbackStatus: 200, fallbackBody: "success\n", want: ConnectivityResult{true, "ok"}, wantFallbackCalls: 1},
		{name: "Firefox portal page", primaryFails: true, fallbackStatus: 200, fallbackBody: "<html>Log in</html>", want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "Firefox success prefix is insufficient", primaryFails: true, fallbackStatus: 200, fallbackBody: "success\n<html>Log in</html>", want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "Firefox missing newline", primaryFails: true, fallbackStatus: 200, fallbackBody: "success", want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "Firefox wrong status", primaryFails: true, fallbackStatus: 204, want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "oversized fallback body", primaryFails: true, fallbackStatus: 200, fallbackBody: "success\n" + strings.Repeat("x", 8192), want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "truncated fallback body", primaryFails: true, fallbackStatus: 200, fallbackBody: "success\n", truncated: true, want: ConnectivityResult{false, "captive"}, wantFallbackCalls: 1},
		{name: "primary portal is terminal", primaryStatus: 200, fallbackStatus: 200, fallbackBody: "success\n", want: ConnectivityResult{false, "captive"}},
		{name: "truncated primary redirect is terminal", primaryStatus: 302, primaryTruncated: true, redirect: true, fallbackStatus: 200, fallbackBody: "success\n", want: ConnectivityResult{false, "captive"}},
		{name: "redirect to successful endpoint is still captive", primaryStatus: 302, redirect: true, want: ConnectivityResult{false, "captive"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRoute, originalDNS, originalTransport := defaultRouteFn, resolveHostFn, http.DefaultTransport
			t.Cleanup(func() {
				defaultRouteFn, resolveHostFn, http.DefaultTransport = originalRoute, originalDNS, originalTransport
			})
			defaultRouteFn = func() (string, error) { return "default via 192.0.2.1 dev test0", nil }
			resolveHostFn = func(string) error { return nil }

			var fallbackCalls, redirectCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/generate_204":
					if tt.primaryTruncated {
						w.Header().Set("Content-Length", "128")
					}
					if tt.redirect {
						w.Header().Set("Location", "/redirect-target")
					}
					w.WriteHeader(tt.primaryStatus)
					if tt.primaryTruncated {
						_, _ = fmt.Fprint(w, "login")
					}
				case "/success.txt":
					fallbackCalls.Add(1)
					if tt.truncated {
						w.Header().Set("Content-Length", "128")
					}
					w.WriteHeader(tt.fallbackStatus)
					_, _ = fmt.Fprint(w, tt.fallbackBody)
				case "/redirect-target":
					redirectCalls.Add(1)
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			transport := &http.Transport{
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					if tt.primaryFails && address == "connectivitycheck.gstatic.com:80" {
						return nil, errors.New("primary endpoint unavailable")
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				},
			}
			t.Cleanup(transport.CloseIdleConnections)
			http.DefaultTransport = transport

			if got := probeUpstreamInternet(); got != tt.want {
				t.Errorf("probe result = %+v, want %+v", got, tt.want)
			}
			if got := fallbackCalls.Load(); got != int32(tt.wantFallbackCalls) {
				t.Errorf("fallback requests = %d, want %d", got, tt.wantFallbackCalls)
			}
			if got := redirectCalls.Load(); got != 0 {
				t.Errorf("probe followed a captive redirect %d time(s)", got)
			}
		})
	}
}
