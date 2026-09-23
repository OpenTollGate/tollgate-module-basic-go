package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// The `/ln-invoice` backpressure contract.
//
// POST /ln-invoice is unauthenticated and does work that costs the router real
// resources (a mint round trip, durable state, a monitor goroutine), while GET
// /ln-invoice is the status poll a paying customer sits in front of. The two
// therefore need different treatment: the POST is quota'd per socket-derived
// client, the GET is not quota'd at all. Both halves are asserted here, because
// a quota that also slows the poll loop would be a regression in the purchase
// flow it is supposed to protect.

// useQuoteQuotaFixture makes the socket-derived client identity resolvable for a
// test request: getMacAddress looks the source IP up in the DHCP lease file
// named by the dhcpLeasePath seam (see main.go), so the fixture writes one lease
// line and points the ARP fallback at a path that does not exist.
func useQuoteQuotaFixture(t *testing.T, ip, mac string) {
	t.Helper()

	dir := t.TempDir()
	leases := filepath.Join(dir, "dhcp.leases")
	line := fmt.Sprintf("1700000000 %s %s phone 01:02:03:04:05:06\n", mac, ip)
	if err := os.WriteFile(leases, []byte(line), 0o600); err != nil {
		t.Fatalf("write lease fixture: %v", err)
	}
	useResolverPaths(t, leases, filepath.Join(dir, "arp-absent"))
}

// backpressureMerchant answers invoice requests and status polls without a
// wallet, and counts each call so a test can prove that a refused request never
// reached the merchant (and so never reached the mint).
type backpressureMerchant struct {
	namedMerchant
	invoices int
	statuses int
}

func (m *backpressureMerchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*merchant.LightningInvoice, error) {
	m.invoices++
	return &merchant.LightningInvoice{
		QuoteID: "quote-1",
		Invoice: "lnbc1stub",
		MintURL: mintURL,
		Amount:  amount,
		State:   "unpaid",
	}, nil
}

func (m *backpressureMerchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*merchant.LightningQuoteStatus, error) {
	m.statuses++
	return &merchant.LightningQuoteStatus{QuoteID: quoteID, State: "unpaid"}, nil
}

func useBackpressureMerchant(fake *backpressureMerchant) {
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(fake)}
}

// lnInvoicePost performs a quote creation the way the shipped portal does: a POST
// with a body-supplied `mac` (which the caller controls and therefore cannot be
// trusted as an identity) from a fixed socket address.
func lnInvoicePost(ip, bodyMAC, mintURL string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"amount":1,"mint_url":%q,"mac":%q}`, mintURL, bodyMAC)
	req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
	req.RemoteAddr = ip + ":41234"
	w := httptest.NewRecorder()
	CorsMiddleware(handleLNInvoiceRoute)(w, req)
	return w
}

func lnInvoicePoll(ip, quoteID, mac string) *httptest.ResponseRecorder {
	q := url.Values{"quote": {quoteID}, "mac": {mac}}
	req := httptest.NewRequest(http.MethodGet, "/ln-invoice?"+q.Encode(), nil)
	req.RemoteAddr = ip + ":41234"
	w := httptest.NewRecorder()
	CorsMiddleware(handleLNInvoiceRoute)(w, req)
	return w
}

type lnInvoiceErrorBody struct {
	Status     int    `json:"status"`
	Error      string `json:"error"`
	Code       string `json:"code"`
	RetryAfter int    `json:"retry_after"`
}

func decodeLnInvoiceError(t *testing.T, w *httptest.ResponseRecorder) lnInvoiceErrorBody {
	t.Helper()

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body lnInvoiceErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", w.Body.String(), err)
	}
	return body
}

// A flood of quote creations is refused with a parseable 429 while the status
// poll loop the same customer is using stays unlimited — and the rotating `mac`
// in the flood's bodies buys no fresh quota, because the quota key comes from the
// socket (DHCP/ARP), never from the request.
func TestLnInvoicePostQuotaRefusesFloodWhileStatusPollStaysUnlimited(t *testing.T) {
	const (
		ip  = "192.168.7.9"
		mac = "aa:bb:cc:11:22:33"
	)
	useQuoteQuotaFixture(t, ip, mac)

	fake := &backpressureMerchant{}
	useBackpressureMerchant(fake)

	// A customer needs exactly one quote per purchase; the burst allows three,
	// so the first three creations must succeed.
	for i := 1; i <= 3; i++ {
		if w := lnInvoicePost(ip, mac, "https://mint.example"); w.Code != http.StatusOK {
			t.Fatalf("POST %d: status = %d, want 200 (body %s)", i, w.Code, w.Body.String())
		}
	}

	// The flood: every request asserts a different `mac`, which must not mint a
	// fresh bucket (it is not the identity the server derives).
	for i := 4; i <= 8; i++ {
		rotatingMAC := fmt.Sprintf("aa:bb:cc:11:22:%02x", i)
		w := lnInvoicePost(ip, rotatingMAC, "https://mint.example")
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("flood POST %d (rotating body mac): status = %d, want 429 (body %s)", i, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got == "" {
			t.Errorf("flood POST %d: missing Retry-After header", i)
		}
		body := decodeLnInvoiceError(t, w)
		if body.Status != 0 || body.Error == "" {
			t.Errorf("flood POST %d: body = %s, want status 0 plus a human-readable error", i, w.Body.String())
		}
		if body.Code != "quote-rate-limited" {
			t.Errorf("flood POST %d: code = %q, want %q", i, body.Code, "quote-rate-limited")
		}
	}

	// The paying customer's poll loop is untouched by the quota: twelve polls in
	// a row (the portal polls every 1–2 s while a payment settles) must all
	// answer.
	before := fake.statuses
	for i := 1; i <= 12; i++ {
		if w := lnInvoicePoll(ip, "quote-1", mac); w.Code != http.StatusOK {
			t.Fatalf("status poll %d: status = %d, want 200 (body %s)", i, w.Code, w.Body.String())
		}
	}
	if got := fake.statuses - before; got != 12 {
		t.Fatalf("status polls reaching the merchant = %d, want 12 (the GET poll loop must stay unlimited)", got)
	}

	// Three quote creations were served, and the flood stopped at the edge
	// instead of reaching the mint.
	if fake.invoices != 3 {
		t.Fatalf("invoice requests reaching the merchant = %d, want 3 (refused requests must not reach the mint)", fake.invoices)
	}
}

// A body larger than the bound is refused with 413 before it is parsed. The
// bound lives in the POST handler, so the handler is driven directly here (the
// route wrapper only adds the quota in front of it).
func TestLnInvoiceRejectsOversizedBody(t *testing.T) {
	useQuoteQuotaFixture(t, "192.168.7.10", "aa:bb:cc:11:22:34")

	fake := &backpressureMerchant{}
	useBackpressureMerchant(fake)

	// 1 MiB of padding in a field the handler does not even read.
	pad := strings.Repeat("A", 1<<20)
	body := fmt.Sprintf(`{"amount":1,"mint_url":"https://mint.example","pad":%q}`, pad)

	req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
	req.RemoteAddr = "192.168.7.10:41234"
	w := httptest.NewRecorder()
	HandleLightningInvoice(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d, want 413 (body %s)", w.Code, w.Body.String())
	}
	if got := decodeLnInvoiceError(t, w); got.Code != "request-too-large" {
		t.Errorf("oversized body: code = %q, want %q", got.Code, "request-too-large")
	}
	if fake.invoices != 0 {
		t.Fatalf("oversized body reached the merchant %d time(s), want 0", fake.invoices)
	}
}

// An amount above the bound is refused with a distinct code instead of being
// passed into the allotment arithmetic.
func TestLnInvoiceRejectsAmountAboveBound(t *testing.T) {
	useQuoteQuotaFixture(t, "192.168.7.11", "aa:bb:cc:11:22:35")

	fake := &backpressureMerchant{}
	useBackpressureMerchant(fake)

	// One sat above the documented 1_000_000-sat ceiling for a single invoice.
	body := `{"amount":1000001,"mint_url":"https://mint.example"}`
	req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(body))
	req.RemoteAddr = "192.168.7.11:41234"
	w := httptest.NewRecorder()
	HandleLightningInvoice(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized amount: status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if got := decodeLnInvoiceError(t, w); got.Code != "amount-too-large" {
		t.Errorf("oversized amount: code = %q, want %q", got.Code, "amount-too-large")
	}
	if fake.invoices != 0 {
		t.Fatalf("oversized amount reached the merchant %d time(s), want 0", fake.invoices)
	}
}
