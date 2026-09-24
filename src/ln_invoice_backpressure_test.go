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

// --- the quota's own bounds and key derivation ------------------------------

// The quota keys come from an attacker-influenced space (a spoofed MAC needs a
// re-association, but a LAN's address space is wide), so the maps must evict
// rather than grow. 5000 distinct keys against a 4096-entry cap must leave the
// map at its cap.
func TestQuoteQuotaMapsAreBounded(t *testing.T) {
	state := newQuoteQuotaState(defaultQuoteQuotaLimits())

	for i := 0; i < quoteQuotaMaxKeys+1000; i++ {
		key := fmt.Sprintf("mac:aa:bb:cc:%02x:%02x:%02x", i>>16, (i>>8)&0xff, i&0xff)
		state.allowFrom(state.perClient, key, 1, 1)
		state.allowFrom(state.perSource, key, 1, 1)
	}

	state.mu.Lock()
	clients, sources := len(state.perClient), len(state.perSource)
	state.mu.Unlock()

	if clients > quoteQuotaMaxKeys {
		t.Fatalf("per-client bucket map holds %d entries, want <= %d", clients, quoteQuotaMaxKeys)
	}
	if sources > quoteQuotaMaxKeys {
		t.Fatalf("per-source bucket map holds %d entries, want <= %d", sources, quoteQuotaMaxKeys)
	}
}

// The quota key is the socket's MAC — resolved through the same DHCP/ARP seam
// the rest of the API uses — and never the `mac` the caller asserts, in the body
// or in the query. An unresolvable client keys on its source address instead of
// on an empty string or the shared sentinel.
func TestClientLimiterKeyIsSocketDerived(t *testing.T) {
	const (
		socketIP  = "192.168.7.20"
		socketMAC = "aa:bb:cc:11:22:99"
	)
	useQuoteQuotaFixture(t, socketIP, socketMAC)

	req := httptest.NewRequest(http.MethodPost, "/ln-invoice?mac=de:ad:be:ef:00:01", nil)
	req.RemoteAddr = socketIP + ":41234"

	if got, want := clientLimiterKey(req), "mac:"+socketMAC; got != want {
		t.Errorf("clientLimiterKey = %q, want %q (the asserted mac must not be the identity)", got, want)
	}

	// No lease, no ARP entry: fall back to the source address, never to a key
	// every unidentified client would share.
	unknown := httptest.NewRequest(http.MethodPost, "/ln-invoice", nil)
	unknown.RemoteAddr = "192.168.7.21:41234"
	if got, want := clientLimiterKey(unknown), "ip:192.168.7.21"; got != want {
		t.Errorf("clientLimiterKey for an unresolvable client = %q, want %q", got, want)
	}
}

// A dual-stack LAN hands out many IPv6 addresses per client, so the per-source
// bucket must be the /64 (and IPv4 stays per-address).
func TestSourceNetworkKeyIsPerSlash64(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want string
	}{
		{"ipv6 first", "2001:db8:1:2::1", "ip:2001:db8:1:2::/64"},
		{"ipv6 same /64", "2001:db8:1:2::abcd", "ip:2001:db8:1:2::/64"},
		{"ipv6 other /64", "2001:db8:1:3::1", "ip:2001:db8:1:3::/64"},
		{"ipv4", "192.168.7.30", "ip:192.168.7.30"},
	}

	keys := make([]string, 0, len(cases))
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/ln-invoice", nil)
		req.RemoteAddr = "[" + tc.ip + "]:41234"
		if !strings.Contains(tc.ip, ":") {
			req.RemoteAddr = tc.ip + ":41234"
		}
		got := sourceNetworkKey(req)
		keys = append(keys, got)
		if got != tc.want {
			t.Errorf("%s: sourceNetworkKey(%s) = %q, want %q", tc.name, tc.ip, got, tc.want)
		}
	}

	if keys[0] != keys[1] {
		t.Errorf("two addresses in one /64 keyed differently: %q vs %q", keys[0], keys[1])
	}
	if keys[0] == keys[2] {
		t.Errorf("two different /64s share one source bucket: %q", keys[0])
	}
}

// --- one derivation per refused request -------------------------------------

// countClientKeyDerivations swaps a counting wrapper around the client-key
// derivation (the clientLimiterKeyFn seam in main.go) and returns a pointer to
// the count. The wrapper delegates to the real derivation, so the request under
// test stays the production request — only the observation is added.
func countClientKeyDerivations(t *testing.T) *int {
	t.Helper()

	prev := clientLimiterKeyFn
	derivations := new(int)
	clientLimiterKeyFn = func(r *http.Request) string {
		*derivations++
		return prev(r)
	}
	t.Cleanup(func() { clientLimiterKeyFn = prev })
	return derivations
}

// useQuoteQuotaState points the package-global quota state at a fresh instance
// for the duration of the test (the seam `quoteQuotas` exists for this), so the
// global layer — 2/s, burst 5 — is not left short by the POSTs a previous test
// in the run already spent.
func useQuoteQuotaState(t *testing.T) *quoteQuotaState {
	t.Helper()

	prev := quoteQuotas
	state := newQuoteQuotaState(defaultQuoteQuotaLimits())
	quoteQuotas = state
	t.Cleanup(func() { quoteQuotas = prev })
	return state
}

// A refused POST must derive the client quota key exactly once. The derivation
// reads the DHCP lease file and then the ARP table, so deriving it inside allow()
// *and* again in the refusal-logging path makes the router do that file I/O twice
// per refused request — during exactly the flood the quota exists to absorb.
func TestRefusedPostDerivesTheClientKeyOnce(t *testing.T) {
	const (
		ip  = "192.168.7.40"
		mac = "aa:bb:cc:11:22:40"
	)
	useQuoteQuotaFixture(t, ip, mac)
	useQuoteQuotaState(t)

	fake := &backpressureMerchant{}
	useBackpressureMerchant(fake)

	derivations := countClientKeyDerivations(t)

	// Spend the per-client burst (three), so the next POST is a refused one.
	for i := 1; i <= 3; i++ {
		if w := lnInvoicePost(ip, mac, "https://mint.example"); w.Code != http.StatusOK {
			t.Fatalf("POST %d: status = %d, want 200 (body %s)", i, w.Code, w.Body.String())
		}
	}

	*derivations = 0
	w := lnInvoicePost(ip, mac, "https://mint.example")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("refused POST: status = %d, want 429 (body %s)", w.Code, w.Body.String())
	}
	if got := *derivations; got != 1 {
		t.Errorf("a refused POST derived the client quota key %d times, want 1 — each derivation reads %s and then %s",
			got, dhcpLeasePath, arpTablePath)
	}
}
