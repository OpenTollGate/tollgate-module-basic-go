package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// macCaptureMerchant records the MAC address each HTTP handler hands to the
// merchant, so a handler test can assert the API boundary normalises it before
// it reaches a session or quote lookup.
type macCaptureMerchant struct {
	namedMerchant
	purchaseMAC string
	invoiceMAC  string
	statusMAC   string
}

func (m *macCaptureMerchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	m.purchaseMAC = macAddress
	return &nostr.Event{Kind: 1022}, nil
}

func (m *macCaptureMerchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*merchant.LightningInvoice, error) {
	m.invoiceMAC = macAddress
	return &merchant.LightningInvoice{
		QuoteID: "quote-1",
		Invoice: "lnbc1",
		MintURL: mintURL,
		Amount:  amount,
		State:   "unpaid",
	}, nil
}

func (m *macCaptureMerchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*merchant.LightningQuoteStatus, error) {
	m.statusMAC = macAddress
	return &merchant.LightningQuoteStatus{QuoteID: quoteID, State: "unpaid"}, nil
}

func useMacCaptureMerchant(fake *macCaptureMerchant) {
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(fake)}
}

// Every endpoint that accepts a `mac` query parameter must resolve the three
// spellings of one address to the same canonical (lowercase) value, because
// sessions and lightning quotes are keyed by that string. Hardware measurement
// on the beta router: GET /ln-invoice?quote=…&mac=<LOWERCASE> → 200, the same
// request with mac=<UPPERCASE> → 404 {"error":"failed to fetch invoice status"}.
func TestMacQueryParameterIsCaseInsensitive(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	cases := []struct {
		name string
		mac  string
	}{
		{"lowercase", "8c:16:45:0d:6f:c5"},
		{"uppercase", "8C:16:45:0D:6F:C5"},
		{"mixed-case", "8C:16:45:0d:6F:C5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := url.QueryEscape(tc.mac)

			t.Run("GET /whoami answers the canonical mac", func(t *testing.T) {
				useMacCaptureMerchant(&macCaptureMerchant{})
				req := httptest.NewRequest(http.MethodGet, "/whoami?mac="+q, nil)
				req.RemoteAddr = "192.0.2.50:4321"
				w := httptest.NewRecorder()

				handler(w, req)

				if got, want := w.Body.String(), "mac="+wantMAC; got != want {
					t.Fatalf("/whoami?mac=%s returned %q, want %q", tc.mac, got, want)
				}
			})

			t.Run("GET /ln-invoice uses the canonical mac", func(t *testing.T) {
				fake := &macCaptureMerchant{}
				useMacCaptureMerchant(fake)
				req := httptest.NewRequest(http.MethodGet, "/ln-invoice?quote=quote-1&mac="+q, nil)
				req.RemoteAddr = "192.0.2.50:4321"
				w := httptest.NewRecorder()

				handleLightningInvoiceGet(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("GET /ln-invoice?quote=quote-1&mac=%s returned %d, want 200 (body: %s)", tc.mac, w.Code, w.Body.String())
				}
				if fake.statusMAC != wantMAC {
					t.Fatalf("GET /ln-invoice passed mac %q to the merchant, want %q", fake.statusMAC, wantMAC)
				}
			})

			t.Run("POST / uses the canonical mac", func(t *testing.T) {
				fake := &macCaptureMerchant{}
				useMacCaptureMerchant(fake)
				req := httptest.NewRequest(http.MethodPost, "/?mac="+q, strings.NewReader("cashuAeyJ0b2tlbiI6W119"))
				req.RemoteAddr = "192.0.2.50:4321"
				w := httptest.NewRecorder()

				HandleRootPost(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("POST /?mac=%s returned %d, want 200 (body: %s)", tc.mac, w.Code, w.Body.String())
				}
				if fake.purchaseMAC != wantMAC {
					t.Fatalf("POST / passed mac %q to the merchant, want %q", fake.purchaseMAC, wantMAC)
				}
			})
		})
	}
}

// The invoice-create endpoint takes its MAC from the JSON body rather than a
// query parameter; it must normalise too, otherwise the quote is stored under a
// different key than the status poll that follows it.
func TestLnInvoicePostBodyMacIsCaseInsensitive(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	cases := []struct {
		name string
		mac  string
	}{
		{"lowercase", "8c:16:45:0d:6f:c5"},
		{"uppercase", "8C:16:45:0D:6F:C5"},
		{"mixed-case", "8C:16:45:0d:6F:C5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &macCaptureMerchant{}
			useMacCaptureMerchant(fake)
			body := fmt.Sprintf(`{"amount":10,"mint_url":"https://mint.example.com","mac":%q}`, tc.mac)
			req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "192.0.2.50:4321"
			w := httptest.NewRecorder()

			handleLightningInvoicePost(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("POST /ln-invoice with mac %q returned %d, want 200 (body: %s)", tc.mac, w.Code, w.Body.String())
			}
			if fake.invoiceMAC != wantMAC {
				t.Fatalf("POST /ln-invoice passed mac %q to the merchant, want %q", fake.invoiceMAC, wantMAC)
			}
		})
	}
}

// Percent-encoded uppercase is the exact shape that returned 404 on hardware
// while the percent-encoded lowercase form returned 200.
func TestMacQueryParameterPercentEncodedUppercase(t *testing.T) {
	const wantMAC = "8c:16:45:0d:6f:c5"

	fake := &macCaptureMerchant{}
	useMacCaptureMerchant(fake)
	req := httptest.NewRequest(http.MethodGet, "/ln-invoice?quote=quote-1&mac=8C%3A16%3A45%3A0D%3A6F%3AC5", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	handleLightningInvoiceGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /ln-invoice with percent-encoded uppercase mac returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if fake.statusMAC != wantMAC {
		t.Fatalf("GET /ln-invoice passed mac %q to the merchant, want %q", fake.statusMAC, wantMAC)
	}
}

// Invalid and absent mac handling must not change: an absent mac still falls
// back to the request-derived client, and an invalid mac is still rejected by
// the merchant (not by the normaliser).
func TestMacNormalisationPreservesAbsentAndInvalidHandling(t *testing.T) {
	fake := &macCaptureMerchant{}
	useMacCaptureMerchant(fake)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.RemoteAddr = "192.0.2.50:4321"
	w := httptest.NewRecorder()

	handler(w, req)

	if got, want := w.Body.String(), "mac=00:00:00:00:00:00"; got != want {
		t.Fatalf("/whoami without mac returned %q, want the fallback %q", got, want)
	}

	useMacCaptureMerchant(fake)
	req = httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(`{"amount":10,"mint_url":"https://mint.example.com","mac":"not-a-mac"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.50:4321"
	w = httptest.NewRecorder()

	handleLightningInvoicePost(w, req)

	if fake.invoiceMAC != "not-a-mac" {
		t.Fatalf("invalid mac %q was rewritten to %q", "not-a-mac", fake.invoiceMAC)
	}
}
