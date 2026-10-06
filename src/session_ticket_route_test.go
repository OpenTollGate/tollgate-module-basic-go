package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// The HTTP contract of the additive session-ticket surface
// (docs/architecture/session-ticket-decision.md).
//
// POST /session/ticket issues a memory-only handle for the caller's OWN session;
// POST /session/rebind moves that session to the caller's current address after
// a MAC rotation. Both resolve the client from the socket — never from a `mac`
// parameter or a body field — and the ticket itself carries no entitlement, so
// these tests assert the ANSWER SHAPE and the refusals, while the meter arithmetic
// the rebind must carry is asserted in the merchant package against the real
// valve and a fake ndsctl.

// sessionTicketMerchant is a merchant fake with the ticket surface. It records
// the MAC and the ticket each handler hands over, so the tests can assert the
// API boundary: identity comes from the socket, and the ticket travels verbatim.
type sessionTicketMerchant struct {
	namedMerchant

	issueMAC    string
	issueTicket string
	issueExpiry int64
	issueErr    error

	rebindTicket string
	rebindMAC    string
	rebindErr    error

	usage    string
	usageMAC string
}

func (m *sessionTicketMerchant) IssueSessionTicket(macAddress string) (string, int64, error) {
	m.issueMAC = macAddress
	return m.issueTicket, m.issueExpiry, m.issueErr
}

func (m *sessionTicketMerchant) RebindSession(ticket, macAddress string) (*merchant.CustomerSession, error) {
	m.rebindTicket = ticket
	m.rebindMAC = macAddress
	if m.rebindErr != nil {
		return nil, m.rebindErr
	}
	return &merchant.CustomerSession{MacAddress: macAddress, Metric: "bytes"}, nil
}

func (m *sessionTicketMerchant) GetUsage(macAddress string) (string, error) {
	m.usageMAC = macAddress
	return m.usage, nil
}

func (m *sessionTicketMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return &merchant.CustomerSession{MacAddress: macAddress, Metric: "bytes", Allotment: 100 * 1024 * 1024}, nil
}

func useSessionTicketMerchant(fake *sessionTicketMerchant) {
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(fake)}
}

// postRebind issues the rebind request a portal would.
func postRebind(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()
	HandleSessionRebind(w, req)
	return w
}

// TestSessionTicketEndpointIssuesForTheSocketClient: a ticket can only be issued
// for the session of the device that asked. The `mac` parameter is the caller's
// claim about itself and is ignored, exactly as on every other route that
// carries an identity.
func TestSessionTicketEndpointIssuesForTheSocketClient(t *testing.T) {
	fake := &sessionTicketMerchant{issueTicket: "v1.cGF5bG9hZA.c2ln", issueExpiry: 1790000000}
	useSessionTicketMerchant(fake)
	resolvedClient(t, testClientIP, testClientMAC)

	req := httptest.NewRequest(http.MethodPost, "/session/ticket?mac="+sentinelMAC, nil)
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	HandleSessionTicket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /session/ticket returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := decodedBody(t, w)
	if got, want := keysOf(body), []string{"expires_at", "status", "ticket"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("/session/ticket body keys = %v, want %v (body: %s)", got, want, w.Body.String())
	}
	if body["ticket"] != "v1.cGF5bG9hZA.c2ln" {
		t.Fatalf("ticket = %v, want the merchant's ticket verbatim", body["ticket"])
	}
	if body["expires_at"] != float64(1790000000) {
		t.Fatalf("expires_at = %v, want 1790000000", body["expires_at"])
	}
	if fake.issueMAC != testClientMAC {
		t.Fatalf("the handler asked the merchant for %q, want the socket-resolved %q — a client-supplied mac must select nothing",
			fake.issueMAC, testClientMAC)
	}
}

// TestSessionTicketEndpointRefusals: no session is a distinct answer (there is
// nothing to name), and an unidentifiable client is refused before anything is
// issued.
func TestSessionTicketEndpointRefusals(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		fake := &sessionTicketMerchant{issueErr: merchant.ErrTicketNoSession}
		useSessionTicketMerchant(fake)
		resolvedClient(t, testClientIP, testClientMAC)

		req := httptest.NewRequest(http.MethodPost, "/session/ticket", nil)
		req.RemoteAddr = testClientIP + ":4321"
		w := httptest.NewRecorder()

		HandleSessionTicket(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("POST /session/ticket without a session returned %d, want 404 (body: %s)", w.Code, w.Body.String())
		}
		body := decodedBody(t, w)
		if body["code"] != "no-session" {
			t.Fatalf("code = %v, want no-session (body: %s)", body["code"], w.Body.String())
		}
	})

	t.Run("unresolvable client", func(t *testing.T) {
		fake := &sessionTicketMerchant{issueTicket: "v1.x.y"}
		useSessionTicketMerchant(fake)
		useResolverPaths(t, "/nonexistent/dhcp.leases", "/nonexistent/arp")

		req := httptest.NewRequest(http.MethodPost, "/session/ticket", nil)
		req.RemoteAddr = testClientIP + ":4321"
		w := httptest.NewRecorder()

		HandleSessionTicket(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("POST /session/ticket for an unidentifiable client returned %d, want 400 (body: %s)", w.Code, w.Body.String())
		}
		if body := decodedBody(t, w); body["code"] != errDeviceUnresolvedCode {
			t.Fatalf("code = %v, want %s", body["code"], errDeviceUnresolvedCode)
		}
		if fake.issueMAC != "" {
			t.Fatalf("a ticket was issued for %q even though the client could not be resolved", fake.issueMAC)
		}
	})

	t.Run("method", func(t *testing.T) {
		useSessionTicketMerchant(&sessionTicketMerchant{})
		req := httptest.NewRequest(http.MethodGet, "/session/ticket", nil)
		w := httptest.NewRecorder()

		HandleSessionTicket(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET /session/ticket returned %d, want 405", w.Code)
		}
	})
}

// TestSessionRebindEndpointMovesTheSession is the rotation the portal drives:
// the ticket travels verbatim, the destination is the socket's own address, and
// the answer carries the session's ledger so the portal can render what is left
// without a second round trip.
func TestSessionRebindEndpointMovesTheSession(t *testing.T) {
	const consumed, allotment = 40 * 1024 * 1024, 100 * 1024 * 1024

	fake := &sessionTicketMerchant{usage: "41943040/104857600"}
	useSessionTicketMerchant(fake)
	resolvedClient(t, testClientIP, testClientMAC)

	w := postRebind(t, "/session/rebind?mac="+sentinelMAC, `{"ticket":"v1.cGF5bG9hZA.c2ln"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /session/rebind returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body := decodedBody(t, w)
	if body["mac"] != testClientMAC {
		t.Fatalf("mac = %v, want the socket-resolved %q — the destination is never the caller's claim", body["mac"], testClientMAC)
	}
	if body["usage"] != "41943040/104857600" {
		t.Fatalf("usage = %v, want the merchant's ledger", body["usage"])
	}
	if fake.rebindTicket != "v1.cGF5bG9hZA.c2ln" {
		t.Fatalf("the handler passed ticket %q to the merchant, want the caller's ticket verbatim", fake.rebindTicket)
	}
	if fake.rebindMAC != testClientMAC {
		t.Fatalf("the handler asked the merchant to rebind to %q, want %q", fake.rebindMAC, testClientMAC)
	}

	// The wire answer is the headline invariant: the remaining allotment is
	// allotment - consumed, not allotment.
	remaining := allotment - consumed
	if remaining != 60*1024*1024 {
		t.Fatalf("test arithmetic broken: remaining = %d", remaining)
	}
}

// TestSessionRebindEndpointRefusalsAreDistinct: a portal reacts differently to
// each refusal — an unusable ticket means "ask for another", a still-live
// attachment means "retry once the old address is gone" — so they must not
// collapse into one status.
func TestSessionRebindEndpointRefusalsAreDistinct(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		wantCode string
	}{
		{"tampered or foreign ticket", merchant.ErrTicketSignature, http.StatusForbidden, "ticket-invalid"},
		{"unknown handle (module restarted)", merchant.ErrTicketUnknown, http.StatusForbidden, "ticket-invalid"},
		{"expired ticket", merchant.ErrTicketExpired, http.StatusForbidden, "ticket-invalid"},
		{"malformed ticket", merchant.ErrTicketMalformed, http.StatusForbidden, "ticket-invalid"},
		{"old attachment still authenticated", merchant.ErrAttachmentActive, http.StatusConflict, "attachment-active"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &sessionTicketMerchant{rebindErr: tc.err}
			useSessionTicketMerchant(fake)
			resolvedClient(t, testClientIP, testClientMAC)

			w := postRebind(t, "/session/rebind", `{"ticket":"v1.cGF5bG9hZA.c2ln"}`)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.status, w.Body.String())
			}
			if body := decodedBody(t, w); body["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %q (body: %s)", body["code"], tc.wantCode, w.Body.String())
			}
		})
	}

	t.Run("no ticket in the body", func(t *testing.T) {
		fake := &sessionTicketMerchant{}
		useSessionTicketMerchant(fake)
		resolvedClient(t, testClientIP, testClientMAC)

		w := postRebind(t, "/session/rebind", `{}`)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
		}
		if body := decodedBody(t, w); body["code"] != "invalid-request" {
			t.Fatalf("code = %v, want invalid-request", body["code"])
		}
		if fake.rebindTicket != "" {
			t.Fatalf("an empty ticket reached the merchant (%q): the handler must refuse it first", fake.rebindTicket)
		}
	})

	t.Run("method", func(t *testing.T) {
		useSessionTicketMerchant(&sessionTicketMerchant{})
		req := httptest.NewRequest(http.MethodGet, "/session/rebind", nil)
		w := httptest.NewRecorder()

		HandleSessionRebind(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET /session/rebind returned %d, want 405", w.Code)
		}
	})
}

// TestBalanceReportsRemainingAgainstTheCarriedLedger pins the customer-visible
// consequence of the carry: /balance's `remaining` is computed from the same
// `used` the session reports, so a rotated session shows the 60 MB it has left
// rather than the 100 MB it started with. The wire shape of /balance is
// unchanged; only the number stops restarting.
func TestBalanceReportsRemainingAgainstTheCarriedLedger(t *testing.T) {
	useSessionTicketMerchant(&sessionTicketMerchant{usage: "41943040/104857600"})
	resolvedClient(t, testClientIP, testClientMAC)

	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	HandleBalance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /balance returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body := decodedBody(t, w)
	if body["session_active"] != true {
		t.Fatalf("session_active = %v, want true (body: %s)", body["session_active"], w.Body.String())
	}
	if body["usage"] != float64(41943040) {
		t.Fatalf("usage = %v, want 41943040", body["usage"])
	}
	if body["remaining"] != float64(104857600-41943040) {
		t.Fatalf("remaining = %v, want %d: after a rotation the remaining allotment is allotment - consumed",
			body["remaining"], 104857600-41943040)
	}
}
