package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// Identity handling at the HTTP boundary.
//
// This file pins the rule the module got wrong: 00:00:00:00:00:00 is the
// all-zero address dnsmasq and the kernel ARP table use for "no address at
// all". It is not a client. Five routes used to substitute it whenever the MAC
// lookup failed and then carry on, so every client the router could not
// identify collapsed into ONE shared identity: one session record, one byte
// meter, one lightning quote, one open gate — reachable by any other client
// whose lookup also failed (observed on the published pre15 artifact, where
// /whoami answers `mac=00:00:00:00:00:00`). On the money path the sentinel is
// worse than useless: the customer pays, the token is received, and the grant
// is attempted for an address that does not exist.
//
// The tests below are organised as two groups:
//
//   - the sentinel group (this file): an unresolvable client must never be
//     answered, and never handed to the merchant, as 00:00:00:00:00:00;
//   - the socket group (identity_socket_test.go): identity is resolved from the
//     request's source IP, and a client-asserted `mac` cannot override it.

const (
	// testClientIP / testClientMAC are the address pair the DHCP-lease fixture
	// in resolvedClient binds.
	testClientIP  = "192.0.2.50"
	testClientMAC = "8c:16:45:0d:6f:c5"

	// sentinelMAC is the address that means "unknown" and must never be treated
	// as an identity.
	sentinelMAC = "00:00:00:00:00:00"

	// deviceUnresolvedCode is the machine-readable code the portal can act on
	// when the router cannot identify the caller. It is additive next to the
	// existing `status`/`error` pair every portal already parses.
	deviceUnresolvedCode = "device-unresolved"
)

// identityMerchant records what each route handed to the merchant, and whether
// the money path was reached at all. It answers notice events with a real
// kind-21023 event so a handler's refusal can be checked by code, unlike
// namedMerchant's nil notice.
type identityMerchant struct {
	namedMerchant

	purchaseCalls   int
	purchaseMAC     string
	invoiceCalls    int
	invoiceMAC      string
	statusCalls     int
	statusMAC       string
	sessionStateM   string
	sessionStateHit bool
}

func (m *identityMerchant) PurchaseSession(cashuToken, macAddress string) (*nostr.Event, error) {
	m.purchaseCalls++
	m.purchaseMAC = macAddress
	return &nostr.Event{Kind: 1022}, nil
}

func (m *identityMerchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*merchant.LightningInvoice, error) {
	m.invoiceCalls++
	m.invoiceMAC = macAddress
	return &merchant.LightningInvoice{
		QuoteID: "quote-1",
		Invoice: "lnbc1",
		MintURL: mintURL,
		Amount:  amount,
		State:   "unpaid",
	}, nil
}

func (m *identityMerchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*merchant.LightningQuoteStatus, error) {
	m.statusCalls++
	m.statusMAC = macAddress
	return &merchant.LightningQuoteStatus{QuoteID: quoteID, State: "unpaid"}, nil
}

func (m *identityMerchant) GetSessionState(macAddress string) (merchant.SessionState, error) {
	m.sessionStateHit = true
	m.sessionStateM = macAddress
	return merchant.SessionStateNone, nil
}

func (m *identityMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return &nostr.Event{
		Kind:    21023,
		Content: message,
		Tags:    nostr.Tags{{"level", level}, {"code", code}},
	}, nil
}

func useIdentityMerchant(m *identityMerchant) {
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(m)}
}

// unresolvedClient points the MAC resolver at paths that do not exist, which is
// what an off-router lookup sees: no dnsmasq lease for the source IP and no ARP
// entry for it.
func unresolvedClient(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	useResolverPaths(t, filepath.Join(dir, "no-dhcp-leases"), filepath.Join(dir, "no-arp-table"))
}

// resolvedClient writes a dnsmasq-format lease binding ip to mac and points the
// resolver at it (see the dhcpLeasePath / arpTablePath seam in main.go). The ARP
// fallback is pointed at a path that does not exist so a regression in the lease
// lookup fails loudly instead of resolving whatever the host's ARP table holds.
func resolvedClient(t *testing.T, ip, mac string) {
	t.Helper()

	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	// dnsmasq lease format: <expiry> <mac> <ip> <hostname> <clientid>
	line := "1750000000 " + mac + " " + ip + " testclient 01:" + mac + "\n"
	if err := os.WriteFile(leases, []byte(line), 0o600); err != nil {
		t.Fatalf("writing the lease fixture: %v", err)
	}

	useResolverPaths(t, leases, filepath.Join(dir, "arp-absent"))
}

// identityRoute is one of the five request shapes that used to substitute the
// sentinel when the client could not be resolved.
type identityRoute struct {
	name string
	// needsIdentity marks a request that cannot be served without knowing which
	// client is asking: the two money routes (POST / and POST /ln-invoice) and
	// the quote-status poll, which authorises the read against the client's MAC.
	needsIdentity bool
	// request builds the request. assertedMAC, when non-empty, is sent as the
	// client's own claim (query parameter, or the JSON body field on
	// POST /ln-invoice); "" means the request carries no claim at all.
	request func(assertedMAC string) *http.Request
	call    func(w http.ResponseWriter, r *http.Request)
	// assertServed checks the response when the identity WAS resolvable, i.e.
	// the route must have used socketMAC, not the asserted value.
	assertServed func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string)
}

func identityRoutes() []identityRoute {
	return []identityRoute{
		{
			name:          "GET /whoami",
			needsIdentity: false,
			request: func(assertedMAC string) *http.Request {
				target := "/whoami"
				if assertedMAC != "" {
					target += "?mac=" + url.QueryEscape(assertedMAC)
				}
				req := httptest.NewRequest(http.MethodGet, target, nil)
				req.RemoteAddr = testClientIP + ":4321"
				return req
			},
			call: handler,
			assertServed: func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("GET /whoami returned %d, want 200 (body: %s)", w.Code, w.Body.String())
				}
				if got, want := w.Body.String(), "mac="+socketMAC; got != want {
					t.Fatalf("GET /whoami body = %q, want %q", got, want)
				}
			},
		},
		{
			name:          "POST / (cashu money path)",
			needsIdentity: true,
			request: func(assertedMAC string) *http.Request {
				target := "/"
				if assertedMAC != "" {
					target += "?mac=" + url.QueryEscape(assertedMAC)
				}
				req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("cashuAeyJ0b2tlbiI6W119"))
				req.RemoteAddr = testClientIP + ":4321"
				return req
			},
			call: HandleRootPost,
			assertServed: func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("POST / returned %d, want 200 (body: %s)", w.Code, w.Body.String())
				}
				if m.purchaseCalls != 1 {
					t.Fatalf("PurchaseSession calls = %d, want 1", m.purchaseCalls)
				}
				if m.purchaseMAC != socketMAC {
					t.Fatalf("POST / passed mac %q to the merchant, want the socket-resolved %q", m.purchaseMAC, socketMAC)
				}
			},
		},
		{
			name:          "GET /session-state",
			needsIdentity: false,
			request: func(assertedMAC string) *http.Request {
				target := "/session-state"
				if assertedMAC != "" {
					target += "?mac=" + url.QueryEscape(assertedMAC)
				}
				req := httptest.NewRequest(http.MethodGet, target, nil)
				req.RemoteAddr = testClientIP + ":4321"
				return req
			},
			call: HandleSessionState,
			assertServed: func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("GET /session-state returned %d, want 200 (body: %s)", w.Code, w.Body.String())
				}
				if !m.sessionStateHit || m.sessionStateM != socketMAC {
					t.Fatalf("GET /session-state asked the merchant about %q, want the socket-resolved %q", m.sessionStateM, socketMAC)
				}
			},
		},
		{
			name:          "POST /ln-invoice",
			needsIdentity: true,
			request: func(assertedMAC string) *http.Request {
				body := `{"amount":10,"mint_url":"https://mint.example.com"}`
				if assertedMAC != "" {
					body = `{"amount":10,"mint_url":"https://mint.example.com","mac":"` + assertedMAC + `"}`
				}
				req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = testClientIP + ":4321"
				return req
			},
			call: handleLightningInvoicePost,
			assertServed: func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("POST /ln-invoice returned %d, want 200 (body: %s)", w.Code, w.Body.String())
				}
				if m.invoiceCalls != 1 || m.invoiceMAC != socketMAC {
					t.Fatalf("POST /ln-invoice bound the quote to %q, want the socket-resolved %q", m.invoiceMAC, socketMAC)
				}
			},
		},
		{
			name:          "GET /ln-invoice",
			needsIdentity: true,
			request: func(assertedMAC string) *http.Request {
				target := "/ln-invoice?quote=quote-1"
				if assertedMAC != "" {
					target += "&mac=" + url.QueryEscape(assertedMAC)
				}
				req := httptest.NewRequest(http.MethodGet, target, nil)
				req.RemoteAddr = testClientIP + ":4321"
				return req
			},
			call: handleLightningInvoiceGet,
			assertServed: func(t *testing.T, w *httptest.ResponseRecorder, m *identityMerchant, socketMAC string) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("GET /ln-invoice returned %d, want 200 (body: %s)", w.Code, w.Body.String())
				}
				if m.statusCalls != 1 || m.statusMAC != socketMAC {
					t.Fatalf("GET /ln-invoice authorised against %q, want the socket-resolved %q", m.statusMAC, socketMAC)
				}
			},
		},
	}
}

// bodyMustNotContainSentinel is the invariant behind every assertion in this
// file: whatever the module answers an unidentifiable client, that answer must
// never claim 00:00:00:00:00:00 is the client's identity.
func bodyMustNotContainSentinel(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), sentinelMAC) {
		t.Fatalf("response claims the sentinel as an identity: %s", w.Body.String())
	}
}

// An unresolvable client must be refused on the routes that need an identity,
// and must never be answered as 00:00:00:00:00:00 anywhere.
func TestUnresolvableClientIsRefusedNotTurnedIntoTheSentinel(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			unresolvedClient(t)

			w := httptest.NewRecorder()
			route.call(w, route.request(""))

			bodyMustNotContainSentinel(t, w)

			if !route.needsIdentity {
				// Read-only routes keep answering: /whoami is an echo of the
				// caller's own identity and /session-state answers "none" for a
				// client it cannot place. Neither may invent the sentinel.
				return
			}

			if w.Code != http.StatusBadRequest {
				t.Fatalf("unidentified client got %d, want 400 with the distinct %s code (body: %s)",
					w.Code, deviceUnresolvedCode, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), deviceUnresolvedCode) {
				t.Fatalf("refusal body %q does not carry the machine-readable %s code",
					w.Body.String(), deviceUnresolvedCode)
			}
			if fake.purchaseCalls != 0 || fake.invoiceCalls != 0 || fake.statusCalls != 0 {
				t.Fatalf("merchant was consulted for an unidentified client: purchase=%d invoice=%d status=%d",
					fake.purchaseCalls, fake.invoiceCalls, fake.statusCalls)
			}
		})
	}
}

// The observed portal behaviour: the Lightning lane sends
// `?mac=00:00:00:00:00:00`. A client that asserts the sentinel — in the query
// string or in the JSON body — must not be able to create or adopt the shared
// identity either.
func TestClientAssertedSentinelIsNotAnIdentity(t *testing.T) {
	for _, route := range identityRoutes() {
		t.Run(route.name, func(t *testing.T) {
			fake := &identityMerchant{}
			useIdentityMerchant(fake)
			unresolvedClient(t)

			w := httptest.NewRecorder()
			route.call(w, route.request(sentinelMAC))

			bodyMustNotContainSentinel(t, w)

			if !route.needsIdentity {
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("client asserting the sentinel got %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), deviceUnresolvedCode) {
				t.Fatalf("refusal body %q does not carry the %s code", w.Body.String(), deviceUnresolvedCode)
			}
			if fake.purchaseCalls != 0 || fake.invoiceCalls != 0 || fake.statusCalls != 0 {
				t.Fatalf("merchant was consulted with the sentinel identity: purchase=%d invoice=%d status=%d",
					fake.purchaseCalls, fake.invoiceCalls, fake.statusCalls)
			}
		})
	}
}

// The money-path half, stated on its own because it is the one that costs a
// customer money: a cashu token submitted by a client the router cannot
// identify must be refused BEFORE Receive, so the token is never consumed and
// no session is ever created for 00:00:00:00:00:00.
func TestMoneyPathRefusesAnUnidentifiableClientBeforeCharging(t *testing.T) {
	fake := &identityMerchant{}
	useIdentityMerchant(fake)
	unresolvedClient(t)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("cashuAeyJ0b2tlbiI6W119"))
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	HandleRootPost(w, req)

	if fake.purchaseCalls != 0 {
		t.Fatalf("PurchaseSession was reached (%d times) for an unidentified client: the token would have been consumed",
			fake.purchaseCalls)
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST / for an unidentified client returned %d, want 400 (body: %s)", w.Code, w.Body.String())
	}

	var notice nostr.Event
	if err := json.Unmarshal(w.Body.Bytes(), &notice); err != nil {
		t.Fatalf("refusal body %q is not a notice event: %v", w.Body.String(), err)
	}
	if notice.Kind != 21023 {
		t.Fatalf("refusal event kind = %d, want 21023 (notice)", notice.Kind)
	}
	if got := noticeCodeOf(notice); got != deviceUnresolvedCode {
		t.Fatalf("notice code = %q, want %q", got, deviceUnresolvedCode)
	}
	if !strings.Contains(strings.ToLower(notice.Content), "device") {
		t.Fatalf("notice %q does not tell the customer what to do about their device", notice.Content)
	}
}

// /session-state is the one route that already refused to ask the merchant about
// the sentinel; it must keep answering "none", and must stop echoing the
// sentinel back as the client's MAC (the artifact-observed leak).
func TestSessionStateForUnresolvableClientIsNoneWithoutTheSentinel(t *testing.T) {
	fake := &identityMerchant{}
	useIdentityMerchant(fake)
	unresolvedClient(t)

	req := httptest.NewRequest(http.MethodGet, "/session-state", nil)
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	HandleSessionState(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /session-state for an unresolvable client returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body := decodedBody(t, w)
	if body["state"] != string(merchant.SessionStateNone) {
		t.Fatalf("state = %v, want %q", body["state"], merchant.SessionStateNone)
	}
	if mac, _ := body["mac"].(string); mac == sentinelMAC {
		t.Fatalf("/session-state echoed the sentinel as the client MAC: %s", w.Body.String())
	}
	if fake.sessionStateHit {
		t.Fatalf("merchant was asked about an unidentified client: %q", fake.sessionStateM)
	}
}

// /whoami is an echo, not a request that needs an identity: it must answer an
// empty MAC rather than the sentinel the pre15 artifact publishes.
func TestWhoamiForUnresolvableClientAnswersNoMAC(t *testing.T) {
	useIdentityMerchant(&identityMerchant{})
	unresolvedClient(t)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /whoami returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "mac=" {
		t.Fatalf("/whoami for an unresolvable client answered %q, want %q (never the sentinel %q)",
			got, "mac=", sentinelMAC)
	}
}

func noticeCodeOf(event nostr.Event) string {
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "code" {
			return tag[1]
		}
	}
	return ""
}
