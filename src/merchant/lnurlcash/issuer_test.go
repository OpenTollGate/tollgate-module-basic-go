package lnurlcash

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The withdrawRequest is untrusted input: it is fetched from a host the note
// named, and it then dictates where the k1 bearer secret is sent. Its shape is
// validated before the secret exists anywhere near it.
func TestFetchWithdrawRequestParsesLUD03(t *testing.T) {
	var gotPath string
	srv := newSameHostEndpoint(t, func(w http.ResponseWriter, r *http.Request, self string) {
		gotPath = r.URL.Path
		writeJSON(t, w, map[string]any{
			"tag":                "withdrawRequest",
			"callback":           self + "/withdraw/cb",
			"k1":                 "deadbeef",
			"maxWithdrawable":    250000, // millisats
			"minWithdrawable":    1000,
			"defaultDescription": "a note",
		})
	})

	c := newTestClient(t, srv)
	wr, err := c.FetchWithdrawRequest(context.Background(), srv.URL+"/withdraw")
	if err != nil {
		t.Fatalf("FetchWithdrawRequest: %v", err)
	}
	if gotPath != "/withdraw" {
		t.Fatalf("GET path = %q, want /withdraw", gotPath)
	}
	if wr.K1 != "deadbeef" {
		t.Fatalf("k1 = %q", wr.K1)
	}
	if wr.Callback.String() != srv.URL+"/withdraw/cb" {
		t.Fatalf("callback = %q", wr.Callback)
	}
	if wr.MaxWithdrawableMSat != 250000 {
		t.Fatalf("maxWithdrawable = %d msat, want 250000", wr.MaxWithdrawableMSat)
	}
}

func TestFetchWithdrawRequestRefusesUntrustedShapes(t *testing.T) {
	tests := []struct {
		name      string
		body      map[string]any
		wantInErr string
	}{
		{
			name:      "wrong tag",
			body:      map[string]any{"tag": "payRequest", "callback": "SELF/cb", "k1": "a"},
			wantInErr: "withdrawRequest",
		},
		{
			name:      "no k1",
			body:      map[string]any{"tag": "withdrawRequest", "callback": "SELF/cb"},
			wantInErr: "k1",
		},
		{
			name:      "no callback",
			body:      map[string]any{"tag": "withdrawRequest", "k1": "a"},
			wantInErr: "callback",
		},
		{
			name:      "callback on another host than the note",
			body:      map[string]any{"tag": "withdrawRequest", "callback": "https://attacker.example/cb", "k1": "a"},
			wantInErr: "host",
		},
		{
			name: "callback is plain http",
			// A non-loopback name over plain http: the scheme rule fires before the
			// host rule, which is the order that matters (never resolve a name we
			// already know we will refuse).
			body:      map[string]any{"tag": "withdrawRequest", "callback": "http://issuer.example/cb", "k1": "a"},
			wantInErr: "https",
		},
		{
			name:      "callback has userinfo",
			body:      map[string]any{"tag": "withdrawRequest", "callback": "SELF/cb", "k1": "a", "userinfo": true},
			wantInErr: "userinfo",
		},
		{
			name:      "zero maxWithdrawable cannot be priced",
			body:      map[string]any{"tag": "withdrawRequest", "callback": "SELF/cb", "k1": "a", "maxWithdrawable": 0},
			wantInErr: "maxWithdrawable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSameHostEndpoint(t, func(w http.ResponseWriter, r *http.Request, self string) {
				body := make(map[string]any, len(tt.body))
				for k, v := range tt.body {
					body[k] = v
				}
				if _, ok := body["userinfo"]; ok {
					delete(body, "userinfo")
					body["callback"] = strings.Replace(self, "http://", "http://user:pw@", 1) + "/cb"
				}
				if cb, ok := body["callback"].(string); ok && strings.HasPrefix(cb, "SELF") {
					body["callback"] = self + strings.TrimPrefix(cb, "SELF")
				}
				writeJSON(t, w, body)
			})
			c := newTestClient(t, srv)
			_, err := c.FetchWithdrawRequest(context.Background(), srv.URL+"/withdraw")
			if err == nil {
				t.Fatal("accepted an untrusted withdrawRequest")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantInErr)
			}
		})
	}
}

// A 302 is the leak the shared lightning client has: net/http follows it and
// carries the request — including the query string with k1 — to whatever host
// the redirect names. The client must refuse redirects outright.
func TestClientRefusesRedirects(t *testing.T) {
	var reached string
	elsewhere := newTestEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.String()
		writeJSON(t, w, map[string]any{})
	})
	issuer := newTestEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal?k1=deadbeef", http.StatusFound)
	})

	c := newTestClient(t, issuer)
	_, err := c.FetchWithdrawRequest(context.Background(), issuer.URL+"/withdraw?k1=deadbeef")
	if err == nil {
		t.Fatal("followed a redirect; the k1 secret would be handed to the redirect target")
	}
	if reached != "" {
		t.Fatalf("the redirect target was reached (%s) — the bearer secret leaked", reached)
	}
	if strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("error leaks the k1 secret: %v", err)
	}
}

// The response body is attacker-controlled. A huge one must be cut off, not
// buffered into the router's memory.
func TestFetchWithdrawRequestBoundsTheBody(t *testing.T) {
	srv := newTestEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := strings.Repeat("a", 64<<10)
		w.Write([]byte(`{"tag":"withdrawRequest","callback":"SELF/cb","k1":"`))
		for i := 0; i < 64; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	})
	c := newTestClient(t, srv)
	_, err := c.FetchWithdrawRequest(context.Background(), srv.URL+"/withdraw")
	if err == nil {
		t.Fatal("buffered an unbounded body")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want a size refusal", err)
	}
}

// The redemption itself: the issuer answers {"status":"OK"}. Anything else is a
// refusal, and the reason is surfaced to the caller (never the k1).
func TestRequestRedemptionSendsK1AndInvoice(t *testing.T) {
	var gotQuery url.Values
	cb := newTestEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		writeJSON(t, w, map[string]any{"status": "OK"})
	})
	c := newTestClient(t, cb)
	cbURL, _ := url.Parse(cb.URL + "/cb")

	res, err := c.RequestRedemption(context.Background(), cbURL, "k1secret", "lnbc1invoice")
	if err != nil {
		t.Fatalf("RequestRedemption: %v", err)
	}
	if !res.OK {
		t.Fatal("OK answer was not treated as a completed redemption")
	}
	if gotQuery.Get("k1") != "k1secret" {
		t.Fatalf("k1 sent = %q", gotQuery.Get("k1"))
	}
	if gotQuery.Get("pr") != "lnbc1invoice" {
		t.Fatalf("pr sent = %q", gotQuery.Get("pr"))
	}
}

func TestRequestRedemptionSurfacesRefusal(t *testing.T) {
	cb := newTestEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"status": "ERROR", "reason": "this note has already been withdrawn"})
	})
	c := newTestClient(t, cb)
	cbURL, _ := url.Parse(cb.URL + "/cb")

	res, err := c.RequestRedemption(context.Background(), cbURL, "k1secret", "lnbc1invoice")
	if err != nil {
		t.Fatalf("RequestRedemption: %v", err)
	}
	if res.OK {
		t.Fatal("an ERROR answer was treated as a completed redemption")
	}
	if !strings.Contains(res.Reason, "already been withdrawn") {
		t.Fatalf("reason = %q, want the issuer's refusal text", res.Reason)
	}
	if strings.Contains(res.Reason, "k1secret") {
		t.Fatal("the refusal carries the k1 secret")
	}
}

// A callback answer that is neither a clear OK nor a clear ERROR is ambiguous:
// the issuer may have paid. It must never be read as a success.
func TestRequestRedemptionClassifiesAmbiguity(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantOK   bool
		wantAmbi bool
	}{
		{
			name:     "5xx",
			handler:  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			wantAmbi: true,
		},
		{
			name: "unparseable body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("<html>gateway</html>"))
			},
			wantAmbi: true,
		},
		{
			name: "unknown status value",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{"status": "MAYBE"})
			},
			wantAmbi: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := newTestEndpoint(t, tt.handler)
			c := newTestClient(t, cb)
			cbURL, _ := url.Parse(cb.URL + "/cb")

			res, err := c.RequestRedemption(context.Background(), cbURL, "k1secret", "lnbc1invoice")
			if err != nil {
				t.Fatalf("RequestRedemption returned a hard error rather than an ambiguous outcome: %v", err)
			}
			if res.OK != tt.wantOK {
				t.Fatalf("OK = %v, want %v", res.OK, tt.wantOK)
			}
			if res.Ambiguous != tt.wantAmbi {
				t.Fatalf("Ambiguous = %v, want %v", res.Ambiguous, tt.wantAmbi)
			}
		})
	}
}

// The dial guard blocks the address classes a note could steer the gate at: a
// router's own admin surface, NoDogSplash, the LAN, cloud metadata, and CGNAT
// (100.64/10 — which Go's IsPrivate does NOT cover, so a naive reuse of the
// existing lightning guard would let it through).
func TestSSRFGuardBlocksInternalAddressClasses(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{name: "loopback", ip: "127.0.0.1", blocked: true},
		{name: "loopback v6", ip: "::1", blocked: true},
		{name: "unspecified", ip: "0.0.0.0", blocked: true},
		{name: "rfc1918 10/8", ip: "10.1.2.3", blocked: true},
		{name: "rfc1918 172.16/12", ip: "172.16.5.5", blocked: true},
		{name: "rfc1918 192.168/16", ip: "192.168.1.1", blocked: true},
		{name: "link-local / cloud metadata", ip: "169.254.169.254", blocked: true},
		{name: "CGNAT 100.64/10", ip: "100.64.0.1", blocked: true},
		{name: "CGNAT upper bound", ip: "100.127.255.254", blocked: true},
		{name: "IPv6 unique local", ip: "fd00::1", blocked: true},
		{name: "IPv6 link-local", ip: "fe80::1", blocked: true},
		{name: "multicast", ip: "224.0.0.1", blocked: true},
		{name: "documentation range is public enough to be dialled", ip: "203.0.113.1", blocked: false},
		{name: "just outside CGNAT", ip: "100.128.0.1", blocked: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("bad fixture IP %q", tt.ip)
			}
			got := blockedAddress(ip)
			if got != tt.blocked {
				t.Fatalf("blockedAddress(%s) = %v, want %v", tt.ip, got, tt.blocked)
			}
		})
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// newTestEndpoint starts an httptest server whose plain-http loopback URL the
// test client is allowed to dial. That allowance is the explicit test seam
// (AllowInsecureLoopbackForTests): production refuses plain http and refuses to
// dial a private address, which is exactly what a test server is.
func newTestEndpoint(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// newSameHostEndpoint serves a withdrawRequest whose callback points back at the
// same server, which is the only callback host the host rule allows. The handler
// receives the server's own base URL.
func newSameHostEndpoint(t *testing.T, h func(w http.ResponseWriter, r *http.Request, self string)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, srv.URL)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(ClientOptions{
		AllowInsecureLoopbackForTests: true,
		Timeout:                       testTimeout,
	})
}
