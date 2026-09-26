package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// The client-identity contract, in executable form.
//
// The decision this file pins: every client-scoped endpoint of this API
// (:2121) is SOCKET-SCOPED. Identity is the address of the client at the other
// end of the request — resolved from the source IP through the dnsmasq lease
// file and the kernel ARP table — and a `mac` a caller sends, in the query
// string or in a JSON body, is a claim that never decides which session, quote,
// byte meter or gate the request touches.
//
// The other half of the contract is the part that cost real time on the bench:
// "ignored" must not mean "silently ignored". A tool that posts a token "for" a
// MAC it is not itself using gets a grant for its OWN socket address, and
// before this change nothing on the wire said so — the probe read the
// 21023/1022 answer, saw no session on the MAC it named, and concluded "the
// gate never opened". So:
//
//   - every client-scoped response names the identity the module used
//     (X-TollGate-Client-MAC), so a caller can compare it with its own socket
//     address instead of with the claim it sent; and
//   - when the caller asserted a different address, the response says which
//     claim was NOT honoured (X-TollGate-Mac-Claim-Ignored).
//
// identity_socket_test.go and identity_sentinel_test.go pin the other property
// (the claim never changes the subject of the request, and the unresolvable
// client never becomes 00:00:00:00:00:00). This file pins that the answer is
// legible.

// The two header names are pinned here as wire strings on purpose. A test that
// read them from the implementation constants would follow a rename silently,
// and the name is what a caller — curl, a harness, the portal's own JS —
// actually depends on.
const (
	wireHeaderClientMAC       = "X-TollGate-Client-MAC"
	wireHeaderMacClaimIgnored = "X-TollGate-Mac-Claim-Ignored"
)

// withScopedMerchant installs a fake merchant for one test and puts the
// package-level provider back afterwards. main.go's init() builds the real
// provider, and this file sorts before e2e_test.go, so a test that left its fake
// installed would decide what every later test's `GET /` answers with.
func withScopedMerchant(t *testing.T, m merchant.MerchantInterface) {
	t.Helper()

	previous := merchantProvider
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(m)}
	t.Cleanup(func() { merchantProvider = previous })
}

// Every client-scoped route answers with the client it was answered for — the
// socket's address, canonical — with no claim made at all.
func TestEveryClientScopedRouteNamesTheClientItAnsweredFor(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			withScopedMerchant(t, &identityMerchant{})
			resolvedClient(t, testClientIP, testClientMAC)

			w := httptest.NewRecorder()
			route.call(w, route.request(""))

			if got := w.Header().Get(wireHeaderClientMAC); got != testClientMAC {
				t.Fatalf("%s answered for %q (header %s), want the socket-resolved %q",
					route.name, got, wireHeaderClientMAC, testClientMAC)
			}
			if got := w.Header().Get(wireHeaderMacClaimIgnored); got != "" {
				t.Fatalf("%s reported %s=%q although the caller asserted nothing",
					route.name, wireHeaderMacClaimIgnored, got)
			}
		})
	}
}

// A claim naming another device is reported as ignored BY NAME, while the
// identity on the same response stays the socket's. This is the signal that
// turns "the gate never opened" into "the module granted this to 8c:16:…, not to
// the 02:11:… I named".
func TestAnAssertedMacIsReportedAsIgnoredAndNeverAsTheIdentity(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			withScopedMerchant(t, &identityMerchant{})
			resolvedClient(t, testClientIP, testClientMAC)

			w := httptest.NewRecorder()
			route.call(w, route.request(otherClientMAC))

			if got := w.Header().Get(wireHeaderMacClaimIgnored); got != otherClientMAC {
				t.Fatalf("%s: %s = %q, want the asserted %q (a claim that is dropped silently is how a "+
					"harness concludes the gate never opened)", route.name, wireHeaderMacClaimIgnored, got, otherClientMAC)
			}
			if got := w.Header().Get(wireHeaderClientMAC); got != testClientMAC {
				t.Fatalf("%s: %s = %q, want the socket-resolved %q", route.name, wireHeaderClientMAC, got, testClientMAC)
			}
			if strings.Contains(w.Body.String(), otherClientMAC) {
				t.Fatalf("%s put the asserted MAC in the body as if it were an identity: %s", route.name, w.Body.String())
			}
		})
	}
}

// The shipped portal sends back exactly what /whoami told it. That is a
// matching claim, not an ignored one — reporting it would make every honest
// portal look like a misconfigured probe.
func TestAMatchingClaimIsNotReportedAsIgnored(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			withScopedMerchant(t, &identityMerchant{})
			// The claim is the same device in a different casing, which is what
			// a portal that upper-cased its value would send.
			resolvedClient(t, testClientIP, strings.ToUpper(testClientMAC))

			w := httptest.NewRecorder()
			route.call(w, route.request(testClientMAC))

			if got := w.Header().Get(wireHeaderMacClaimIgnored); got != "" {
				t.Fatalf("%s reported a matching claim as ignored: %s = %q", route.name, wireHeaderMacClaimIgnored, got)
			}
			if got := w.Header().Get(wireHeaderClientMAC); got != testClientMAC {
				t.Fatalf("%s: %s = %q, want the canonical socket identity %q", route.name, wireHeaderClientMAC, got, testClientMAC)
			}
		})
	}
}

// A client the router cannot place is not named as anybody, and a claim it made
// on the way in is still reported — the one case where a harness must not read
// silence as agreement.
func TestUnresolvableClientIsNotNamedAndItsClaimIsStillReported(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			withScopedMerchant(t, &identityMerchant{})
			unresolvedClient(t)

			w := httptest.NewRecorder()
			route.call(w, route.request(otherClientMAC))

			if got := w.Header().Get(wireHeaderClientMAC); got != "" {
				t.Fatalf("%s named %q as the client, want no identity header for an unresolvable client",
					route.name, got)
			}
			if got := w.Header().Get(wireHeaderMacClaimIgnored); got != otherClientMAC {
				t.Fatalf("%s: %s = %q, want the asserted %q", route.name, wireHeaderMacClaimIgnored, got, otherClientMAC)
			}
		})
	}
}

// /balance is the endpoint the card measured: `?mac=<other>` and no parameter
// returned byte-identical bodies, so a probe could not tell whose balance it was
// reading. The body now names the client it is about, exactly as /session-state
// does — additive, and absent when the client cannot be resolved.
func TestBalanceNamesTheClientItsAnswerIsAbout(t *testing.T) {
	const testIP = "192.0.2.50"

	withScopedMerchant(t, &sessionStateMerchant{
		usage: "123456/600000",
		session: &merchant.CustomerSession{
			MacAddress: testClientMAC,
			StartTime:  1750000000,
			Metric:     "bytes",
			Allotment:  600000,
		},
	})
	resolvedClient(t, testIP, testClientMAC)

	req := httptest.NewRequest(http.MethodGet, "/balance?mac="+otherClientMAC, nil)
	req.RemoteAddr = testIP + ":4321"
	w := httptest.NewRecorder()

	HandleBalance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /balance returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	// Decoded as a map on purpose: this pins the JSON key on the wire, not a
	// field added to a struct in the same change as the test that reads it.
	body := decodedBody(t, w)
	if got, _ := body["mac"].(string); got != testClientMAC {
		t.Fatalf("GET /balance reported mac=%q, want the socket-resolved %q (the asserted %q must not appear as an identity)",
			got, testClientMAC, otherClientMAC)
	}
	if body["session_active"] != true {
		t.Fatalf("GET /balance reported session_active=%v for a client with a live session: %s", body["session_active"], w.Body.String())
	}
}

// The identity headers are for a page or a harness running cross-origin to the
// API (:2050/:2051 portal, or a test rig in a browser). Unexposed response
// headers are unreadable from script, which would leave the contract legible to
// curl alone — the exact situation that produced the false negative.
func TestCorsExposesTheClientIdentityHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.Header.Set("Origin", "http://192.168.1.1:2051")
	w := httptest.NewRecorder()

	CorsMiddleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })(w, req)

	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{wireHeaderClientMAC, wireHeaderMacClaimIgnored} {
		if !strings.Contains(exposed, want) {
			t.Fatalf("Access-Control-Expose-Headers = %q, does not expose %s — a page on the portal origin cannot read it",
				exposed, want)
		}
	}
}
