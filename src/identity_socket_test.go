package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Socket-derived identity.
//
// A MAC address is not confidential: it is the source address of every frame
// the client transmits, it is visible to anyone on the air, and any client can
// put any value in a `mac` query parameter or in the `mac` field of the
// POST /ln-invoice body. Both were taken as identity:
//
//   - the portal resolves its address once per page load from /whoami and then
//     echoes it back on the Lightning lane (`?mac=` on POST /ln-invoice, `&mac=`
//     on the status poll), so a value cached before a MAC rotation, a stale
//     value, or a hand-crafted one decided which session, which byte meter and
//     which quote the request touched — and, on the money path, which device the
//     grant was applied to;
//   - the shipped portal's Lightning lane was observed sending
//     `?mac=00:00:00:00:00:00`.
//
// Identity is therefore resolved from the SOCKET — the request's source IP, via
// the dnsmasq lease file and the kernel ARP table, the same input the client
// cannot choose — and the client's claim is ignored. These tests pin that on
// every route that used to take it, using the injectable lease seam in main.go
// so the resolution path itself (not a stub) is exercised.

// otherClientMAC is a valid address the caller may name. It is deliberately a
// different device from the one the socket resolves to.
const otherClientMAC = "aa:bb:cc:dd:ee:ff"

// A client-named MAC must not become the identity on any route, in either
// direction: the merchant must be handed the socket-resolved address, and the
// claim must not appear in the response either.
func TestClientAssertedMACCannotOverrideTheSocketIdentity(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			resolvedClient(t, testClientIP, testClientMAC)

			w := httptest.NewRecorder()
			route.call(w, route.request(otherClientMAC))

			if strings.Contains(w.Body.String(), otherClientMAC) {
				t.Fatalf("response echoes the client-asserted MAC as the identity: %s", w.Body.String())
			}
			route.assertServed(t, w, fake, testClientMAC)
		})
	}
}

// The specific value the shipped portal's Lightning lane sends. Naming the
// sentinel must not reach the merchant (Part A semantics) and must not stop a
// resolvable client from being identified by its socket address (Part B).
func TestAssertedSentinelCannotSuppressAResolvableClient(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			resolvedClient(t, testClientIP, testClientMAC)

			w := httptest.NewRecorder()
			route.call(w, route.request(sentinelMAC))

			bodyMustNotContainSentinel(t, w)
			route.assertServed(t, w, fake, testClientMAC)
		})
	}
}

// The same rule with no claim at all, which is what a client that never read
// /whoami sends: identity still comes from the socket.
func TestIdentityIsResolvedFromTheSocketWithoutAnyClaim(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			resolvedClient(t, testClientIP, testClientMAC)

			w := httptest.NewRecorder()
			route.call(w, route.request(""))

			route.assertServed(t, w, fake, testClientMAC)
		})
	}
}

// The socket-resolved address is canonicalised before it is used as a key:
// sessions, byte baselines and lightning quotes are all keyed by the string, and
// dnsmasq may hold it in any casing. Every route must hand the merchant the
// lowercase form so one device is one key (the bug the case-insensitivity work
// fixed, now applied to the socket source rather than to a client claim).
func TestSocketIdentityIsCanonicalisedOnEveryRoute(t *testing.T) {
	const leasedUpper = "8C:16:45:0D:6F:C5"

	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			resolvedClient(t, testClientIP, leasedUpper)

			w := httptest.NewRecorder()
			route.call(w, route.request(""))

			route.assertServed(t, w, fake, testClientMAC)
		})
	}
}
