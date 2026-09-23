package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// The HTTP contract of the additive session-state surface, plus the regression
// pin on the endpoints it must not disturb.
//
// The captive portal that ships in the package parses `/usage` as `used/total`
// and treats `-1/-1` as "no session"; that behaviour is load-bearing for
// deployed portals, so `/usage` stays byte-compatible. The new `/session-state`
// endpoint answers the question `/usage` cannot: which kind of "no session" this
// is.

// sessionStateMerchant is a merchant fake with the session-state surface. It
// records the MAC each handler hands over, so the tests can assert the API
// boundary canonicalises it (#537).
type sessionStateMerchant struct {
	namedMerchant
	state    string
	usage    string
	session  *merchant.CustomerSession
	stateMAC string
}

func (m *sessionStateMerchant) GetUsage(macAddress string) (string, error) {
	return m.usage, nil
}

func (m *sessionStateMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return m.session, nil
}

func (m *sessionStateMerchant) GetSessionState(macAddress string) (merchant.SessionState, error) {
	m.stateMAC = macAddress
	return merchant.SessionState(m.state), nil
}

func useSessionStateMerchant(fake *sessionStateMerchant) {
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(fake)}
}

func decodedBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body %q is not a JSON object: %v", w.Body.String(), err)
	}
	return body
}

func keysOf(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GET /session-state?mac=… answers the machine-readable state for that MAC, and
// canonicalises the MAC spelling the same way every other endpoint does.
func TestSessionStateEndpointReportsTheThreeStates(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	cases := []struct {
		name  string
		state string
	}{
		{"first-time visitor", "none"},
		{"live session", "active"},
		{"session that ran out", "expired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sessionStateMerchant{state: tc.state}
			useSessionStateMerchant(fake)

			// Uppercase on purpose: the portal may copy the MAC out of a
			// preauth page that spells it in upper case.
			req := httptest.NewRequest(http.MethodGet, "/session-state?mac="+url.QueryEscape("8C:16:45:0D:6F:C5"), nil)
			req.RemoteAddr = "192.0.2.50:4321"
			w := httptest.NewRecorder()

			HandleSessionState(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /session-state returned %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			body := decodedBody(t, w)
			if got, want := keysOf(body), []string{"mac", "state", "status"}; fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("/session-state body keys = %v, want %v (body: %s)", got, want, w.Body.String())
			}
			if body["state"] != tc.state {
				t.Fatalf("state = %v, want %q (body: %s)", body["state"], tc.state, w.Body.String())
			}
			if body["mac"] != wantMAC {
				t.Fatalf("mac = %v, want %q", body["mac"], wantMAC)
			}
			if body["status"] != float64(1) {
				t.Fatalf("status = %v, want 1", body["status"])
			}
			if fake.stateMAC != wantMAC {
				t.Fatalf("handler passed mac %q to the merchant, want the canonical %q", fake.stateMAC, wantMAC)
			}
		})
	}
}

// A request without a `mac` parameter falls back to the request-derived client
// (DHCP lease / ARP). Off the router that lookup fails, and the endpoint must
// still answer "none" rather than 500 — the portal polls it while rendering.
func TestSessionStateEndpointWithoutMacIsNoneNotAnError(t *testing.T) {
	fake := &sessionStateMerchant{state: "expired"}
	useSessionStateMerchant(fake)

	req := httptest.NewRequest(http.MethodGet, "/session-state", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	HandleSessionState(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /session-state without mac returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body := decodedBody(t, w)
	if body["mac"] != "00:00:00:00:00:00" {
		t.Fatalf("mac = %v, want the documented fallback 00:00:00:00:00:00", body["mac"])
	}
	if body["state"] != "none" {
		t.Fatalf("state for an unidentifiable client = %v, want \"none\"", body["state"])
	}
}

func TestSessionStateEndpointRejectsNonGET(t *testing.T) {
	useSessionStateMerchant(&sessionStateMerchant{state: "none"})

	req := httptest.NewRequest(http.MethodPost, "/session-state?mac=8c:16:45:0d:6f:c5", strings.NewReader(""))
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	HandleSessionState(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /session-state returned %d, want 405", w.Code)
	}
}

// /usage is unchanged, byte for byte: the shipped portal parses `used/total` and
// `-1/-1`, and the state work is additive on purpose.
//
// /usage resolves its client from the request IP (it takes no `mac` parameter,
// unlike /whoami and /ln-invoice); off the router that lookup fails, and the
// documented answer is the bare "-1/-1" sentinel — not JSON, not an error —
// which is exactly what a portal on an expired lease relies on.
func TestUsageEndpointBytesAreUnchanged(t *testing.T) {
	useSessionStateMerchant(&sessionStateMerchant{usage: "123456/600000"})

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	HandleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /usage returned %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "-1/-1" {
		t.Fatalf("GET /usage body = %q, want %q", got, "-1/-1")
	}
	if strings.Contains(w.Body.String(), "{") {
		t.Fatalf("GET /usage body %q must not be JSON", w.Body.String())
	}
}

// /balance keeps its shape: `status`, `session_active`, and the session numbers
// when one exists. There is no state field here — the state lives on the new
// endpoint so a deployed portal's JSON parsing cannot shift under it.
//
// Like /usage, /balance resolves its client from the request IP (no `mac`
// parameter), so off the router the MAC lookup fails and the handler answers the
// documented "no session" body. That is the branch a portal renders most often
// (expired lease, dnsmasq restart) and the one pinned here; the session-bearing
// branch needs a real DHCP lease or ARP entry, which a unit test must not fake
// by writing the router's lease file.
func TestBalanceEndpointShapeIsUnchanged(t *testing.T) {
	useSessionStateMerchant(&sessionStateMerchant{usage: "123456/600000"})

	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	HandleBalance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /balance returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body := decodedBody(t, w)
	if body["session_active"] != false {
		t.Fatalf("session_active = %v, want false for an unresolvable client", body["session_active"])
	}
	if body["status"] != float64(1) {
		t.Fatalf("status = %v, want 1", body["status"])
	}
	for _, key := range []string{"usage", "allotment", "remaining"} {
		if body[key] != float64(0) {
			t.Fatalf("%s = %v, want 0 in the no-session body", key, body[key])
		}
	}
	if _, present := body["state"]; present {
		t.Fatalf("/balance grew a state field: %s — the state contract belongs to /session-state", w.Body.String())
	}
}
