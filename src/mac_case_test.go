package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// MAC canonicalisation and the client's own claim.
//
// Sessions, byte-meter baselines and lightning quotes are all keyed by the MAC
// string, so one device must resolve to ONE key whatever spelling the source
// uses. Until this change the value that reached the merchant was the caller's
// own claim — a `mac` query parameter, or the `mac` field of the POST
// /ln-invoice JSON body — canonicalised on the way in. That made the string the
// portal happened to send authoritative: a stale value cached before a MAC
// rotation, a hand-crafted one, or (observed on the shipped portal's Lightning
// lane) `00:00:00:00:00:00` decided which session, meter and quote the request
// touched.
//
// The value now comes from the socket (see identity_socket_test.go for the
// authority proof); this file pins the two properties that survive that change:
// the socket-resolved address is canonicalised on every route, and a
// client-asserted address is ignored in every shape a client can send it —
// query parameter, JSON body field, invalid format, percent-encoded casing.

// Hardware measurement on the beta router: GET /ln-invoice?quote=…&mac=<LOWERCASE>
// → 200, the same request with mac=<UPPERCASE> → 404
// {"error":"failed to fetch invoice status"}, because the quote was stored under
// one key and looked up under another. The three spellings of one address must
// resolve to the same key; the source of that address is now the lease file, and
// dnsmasq's spelling of it is not something the module controls.
func TestSocketMACIsCanonicalisedOnEveryRoute(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	cases := []struct {
		name string
		mac  string
	}{
		{"lowercase lease", "8c:16:45:0d:6f:c5"},
		{"uppercase lease", "8C:16:45:0D:6F:C5"},
		{"mixed-case lease", "8C:16:45:0d:6F:C5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("GET /whoami answers the canonical mac", func(t *testing.T) {
				useIdentityMerchant(&identityMerchant{})
				resolvedClient(t, testClientIP, tc.mac)
				req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
				req.RemoteAddr = testClientIP + ":4321"
				w := httptest.NewRecorder()

				handler(w, req)

				if got, want := w.Body.String(), "mac="+wantMAC; got != want {
					t.Fatalf("/whoami with a %s in the lease returned %q, want %q", tc.name, got, want)
				}
			})

			t.Run("GET /ln-invoice uses the canonical mac", func(t *testing.T) {
				fake := &identityMerchant{}
				useIdentityMerchant(fake)
				resolvedClient(t, testClientIP, tc.mac)
				req := httptest.NewRequest(http.MethodGet, "/ln-invoice?quote=quote-1", nil)
				req.RemoteAddr = testClientIP + ":4321"
				w := httptest.NewRecorder()

				handleLightningInvoiceGet(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("GET /ln-invoice with a %s in the lease returned %d, want 200 (body: %s)",
						tc.name, w.Code, w.Body.String())
				}
				if fake.statusMAC != wantMAC {
					t.Fatalf("GET /ln-invoice passed mac %q to the merchant, want %q", fake.statusMAC, wantMAC)
				}
			})

			t.Run("POST / uses the canonical mac", func(t *testing.T) {
				fake := &identityMerchant{}
				useIdentityMerchant(fake)
				resolvedClient(t, testClientIP, tc.mac)
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("cashuA***"))
				req.RemoteAddr = testClientIP + ":4321"
				w := httptest.NewRecorder()

				HandleRootPost(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("POST / with a %s in the lease returned %d, want 200 (body: %s)",
						tc.name, w.Code, w.Body.String())
				}
				if fake.purchaseMAC != wantMAC {
					t.Fatalf("POST / passed mac %q to the merchant, want %q", fake.purchaseMAC, wantMAC)
				}
			})

			t.Run("GET /session-state uses the canonical mac", func(t *testing.T) {
				fake := &identityMerchant{}
				useIdentityMerchant(fake)
				resolvedClient(t, testClientIP, tc.mac)
				req := httptest.NewRequest(http.MethodGet, "/session-state", nil)
				req.RemoteAddr = testClientIP + ":4321"
				w := httptest.NewRecorder()

				HandleSessionState(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("GET /session-state with a %s in the lease returned %d, want 200 (body: %s)",
						tc.name, w.Code, w.Body.String())
				}
				if fake.sessionStateM != wantMAC {
					t.Fatalf("GET /session-state passed mac %q to the merchant, want %q", fake.sessionStateM, wantMAC)
				}
			})
		})
	}
}

// The invoice-create endpoint takes a `mac` field in the JSON body. It is
// accepted on the wire and ignored: the quote is bound to the socket-resolved
// address, in any casing the client names.
func TestLnInvoicePostBodyMacIsIgnored(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	cases := []struct {
		name string
		mac  string
	}{
		{"lowercase", "aa:bb:cc:dd:ee:ff"},
		{"uppercase", "AA:BB:CC:DD:EE:FF"},
		{"mixed-case", "Aa:Bb:Cc:Dd:Ee:Ff"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			resolvedClient(t, testClientIP, wantMAC)

			body := fmt.Sprintf(`{"amount":10,"mint_url":"https://mint.example.com","mac":%q}`, tc.mac)
			req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = testClientIP + ":4321"
			w := httptest.NewRecorder()

			handleLightningInvoicePost(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("POST /ln-invoice with a client-named mac %q returned %d, want 200 (body: %s)",
					tc.mac, w.Code, w.Body.String())
			}
			if fake.invoiceMAC != wantMAC {
				t.Fatalf("POST /ln-invoice bound the quote to %q, want the socket-resolved %q",
					fake.invoiceMAC, wantMAC)
			}
		})
	}
}

// Percent-encoded uppercase in the query string is the exact shape that returned
// 404 on hardware, i.e. the shape a caller-supplied value can reach the handler
// in. It is ignored like every other client assertion.
func TestMacQueryParameterPercentEncodedIsIgnored(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	fake := &identityMerchant{}
	useIdentityMerchant(fake)
	resolvedClient(t, testClientIP, wantMAC)

	req := httptest.NewRequest(http.MethodGet, "/ln-invoice?quote=quote-1&mac=8C%3A16%3A45%3A0D%3A6F%3AC5", nil)
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	handleLightningInvoiceGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /ln-invoice with a percent-encoded client mac returned %d, want 200 (body: %s)",
			w.Code, w.Body.String())
	}
	if fake.statusMAC != wantMAC {
		t.Fatalf("GET /ln-invoice authorised against %q, want the socket-resolved %q", fake.statusMAC, wantMAC)
	}
}

// Absent and invalid claims must not change the outcome: identity comes from the
// socket either way, and a value that is not an address is never handed to the
// merchant.
func TestIdentityResolutionPreservesAbsentAndInvalidHandling(t *testing.T) {
	const socketMAC = "8c:16:45:0d:6f:c5"

	t.Run("absent claim still resolves the socket client", func(t *testing.T) {
		fake := &identityMerchant{}
		useIdentityMerchant(fake)
		resolvedClient(t, testClientIP, socketMAC)

		req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
		req.RemoteAddr = testClientIP + ":4321"
		w := httptest.NewRecorder()

		handler(w, req)

		if got, want := w.Body.String(), "mac="+socketMAC; got != want {
			t.Fatalf("/whoami without a claim returned %q, want the socket-resolved %q", got, want)
		}
	})

	t.Run("invalid claim never reaches the merchant", func(t *testing.T) {
		fake := &identityMerchant{}
		useIdentityMerchant(fake)
		resolvedClient(t, testClientIP, socketMAC)

		req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(
			`{"amount":10,"mint_url":"https://mint.example.com","mac":"not-a-mac"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = testClientIP + ":4321"
		w := httptest.NewRecorder()

		handleLightningInvoicePost(w, req)

		if fake.invoiceMAC != socketMAC {
			t.Fatalf("POST /ln-invoice passed %q to the merchant, want the socket-resolved %q",
				fake.invoiceMAC, socketMAC)
		}
		if fake.invoiceMAC == "not-a-mac" {
			t.Fatalf("the client's invalid claim became the identity")
		}
	})

	t.Run("invalid claim is not echoed into the query-string path either", func(t *testing.T) {
		fake := &identityMerchant{}
		useIdentityMerchant(fake)
		resolvedClient(t, testClientIP, socketMAC)

		req := httptest.NewRequest(http.MethodGet, "/session-state?mac="+url.QueryEscape("not-a-mac"), nil)
		req.RemoteAddr = testClientIP + ":4321"
		w := httptest.NewRecorder()

		HandleSessionState(w, req)

		if fake.sessionStateM != socketMAC {
			t.Fatalf("GET /session-state asked the merchant about %q, want the socket-resolved %q",
				fake.sessionStateM, socketMAC)
		}
	})
}
