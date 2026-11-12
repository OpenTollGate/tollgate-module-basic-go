package lnbits

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testInvoice(t *testing.T, msat int64) (string, string, string) {
	t.Helper()
	pre := sha256.Sum256([]byte("public LNbits contract test preimage"))
	hash := sha256.Sum256(pre[:])
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	inv, err := zpay32.NewInvoice(&chaincfg.MainNetParams, hash, time.Now(), zpay32.Amount(lnwire.MilliSatoshi(msat)), zpay32.Description("LNbits contract test"))
	if err != nil {
		t.Fatal(err)
	}
	bolt, err := inv.Encode(zpay32.MessageSigner{SignCompact: func(msg []byte) ([]byte, error) { return ecdsa.SignCompact(key, chainhash.HashB(msg), true), nil }})
	if err != nil {
		t.Fatal(err)
	}
	return bolt, hex.EncodeToString(hash[:]), hex.EncodeToString(pre[:])
}
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, err := New(Config{BaseURL: s.URL + "/proxy", WalletID: "wallet-a", InvoiceKey: "test-invoice-key", AdminKey: "test-admin-key", Network: "bc", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

// walletJSON is faithful to LNbits v1.6.2 admin-key wallet responses,
// which include the wallet ID (lnbits/core/views/wallet_api.py).
func walletJSON(w http.ResponseWriter) {
	fmt.Fprint(w, `{"id":"wallet-a","name":"test wallet","balance":4321}`)
}

// invoiceWalletJSON is faithful to LNbits v1.6.2 invoice-key wallet
// responses, which omit the wallet ID.
func invoiceWalletJSON(w http.ResponseWriter) {
	fmt.Fprint(w, `{"name":"test wallet","balance":4321}`)
}
func paymentJSON(bolt, hash, pre, status string, amount int64) map[string]any {
	return map[string]any{"wallet_id": "wallet-a", "payment_hash": hash, "checking_id": "internal_" + hash, "bolt11": bolt, "payment_request": bolt, "amount": amount, "status": status, "preimage": pre, "fee": -5}
}
func TestReceiveAndLookupBoundToWallet(t *testing.T) {
	bolt, hash, pre := testInvoice(t, 21000)
	var posts atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-invoice-key" {
			t.Error("wrong credential")
		}
		switch r.URL.Path {
		case "/proxy/api/v1/wallet":
			// Invoice-key response: no wallet ID in LNbits v1.6.2.
			invoiceWalletJSON(w)
		case "/proxy/api/v1/payments":
			posts.Add(1)
			var q map[string]any
			json.NewDecoder(r.Body).Decode(&q)
			if q["out"] != false || q["unit"] != "sat" || q["amount"] != float64(21) || q["external_id"] != "purchase-1" {
				t.Errorf("incorrect request: %#v", q)
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(paymentJSON(bolt, hash, "", "pending", 21000))
		case "/proxy/api/v1/payments/" + hash:
			json.NewEncoder(w).Encode(map[string]any{"paid": true, "details": paymentJSON(bolt, hash, pre, "success", 21000)})
		default:
			t.Error("wrong path")
			w.WriteHeader(404)
		}
	})
	inv, err := c.CreateInvoice(context.Background(), InvoiceRequest{AmountSat: 21, Memo: "test", Expiry: 300, ExternalID: "purchase-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.PaymentHash != hash || inv.AmountMSat != 21000 {
		t.Fatal("wrong invoice")
	}
	paid, err := c.Lookup(context.Background(), hash, Incoming)
	if err != nil {
		t.Fatal(err)
	}
	if !paid.Settled() {
		t.Fatal("not settled")
	}
	if posts.Load() != 1 {
		t.Fatal("POST duplicated")
	}
}

// LNbits v1.6.2 returns the wallet ID only for admin credentials; an
// invoice-key GET /api/v1/wallet response omits it. A receive-only client
// (no admin credential) must still obtain wallet details and create invoices.
func TestInvoiceKeyWalletOmittingIDSucceeds(t *testing.T) {
	bolt, hash, _ := testInvoice(t, 21000)
	var posts atomic.Int32
	_, s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Key"); got != "test-invoice-key" {
			t.Errorf("wrong credential: %q", got)
		}
		switch r.URL.Path {
		case "/proxy/api/v1/wallet":
			invoiceWalletJSON(w)
		case "/proxy/api/v1/payments":
			posts.Add(1)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(paymentJSON(bolt, hash, "", "pending", 21000))
		default:
			t.Errorf("wrong path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	receiveOnly, err := New(Config{BaseURL: s.URL + "/proxy", WalletID: "wallet-a", InvoiceKey: "test-invoice-key", Network: "bc", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	w, err := receiveOnly.Wallet(context.Background())
	if err != nil {
		t.Fatalf("receive-only wallet details refused: %v", err)
	}
	if w.ID != "" || w.Name != "test wallet" || w.BalanceMSat != 4321 {
		t.Fatalf("wallet not returned as supplied: %#v", w)
	}
	inv, err := receiveOnly.CreateInvoice(context.Background(), InvoiceRequest{AmountSat: 21, Memo: "test", Expiry: 300, ExternalID: "purchase-1"})
	if err != nil {
		t.Fatalf("receive-only invoice refused: %v", err)
	}
	if inv.PaymentHash != hash || inv.AmountMSat != 21000 {
		t.Fatal("wrong invoice")
	}
	if posts.Load() != 1 {
		t.Fatal("invoice POST duplicated")
	}
}

// An absent wallet ID is the only permitted relaxation, and only in the
// receiving path: a nonempty wrong ID or a negative balance is always
// refused.
func TestWalletRejectsWrongIDAndNegativeBalance(t *testing.T) {
	for _, body := range []string{
		`{"id":"other","name":"test wallet","balance":4321}`,
		`{"name":"test wallet","balance":-1}`,
		`{"id":"wallet-a","name":"test wallet","balance":-5}`,
	} {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.Wallet(context.Background()); err == nil {
			t.Fatalf("unsafe wallet details accepted: %s", body)
		}
	}
}

func TestNoIDPreflightDoesNotAcceptForeignCreatedInvoice(t *testing.T) {
	bolt, hash, _ := testInvoice(t, 21000)
	var posts atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-invoice-key" {
			t.Error("receiving used a spending credential")
		}
		if r.Method == "GET" {
			invoiceWalletJSON(w)
			return
		}
		posts.Add(1)
		p := paymentJSON(bolt, hash, "", "pending", 21000)
		p["wallet_id"] = "other"
		json.NewEncoder(w).Encode(p)
	})
	_, err := c.CreateInvoice(context.Background(), InvoiceRequest{AmountSat: 21, Expiry: 300, ExternalID: "purchase-1"})
	if !IsUnknown(err) {
		t.Fatalf("foreign created invoice must remain uncertain: %v", err)
	}
	if posts.Load() != 1 {
		t.Fatal("invoice request was skipped or retried")
	}
}
func TestLookupRejectsUnboundOrInconsistentPayment(t *testing.T) {
	bolt, hash, pre := testInvoice(t, 21000)
	for _, scenario := range []string{"bare-paid", "foreign-wallet", "wrong-hash", "wrong-direction", "wrong-amount", "conflicting-state", "conflicting-invoice", "bad-preimage", "bad-json", "trailing-json", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				p := paymentJSON(bolt, hash, pre, "success", 21000)
				b := map[string]any{"paid": true, "details": p}
				switch scenario {
				case "bare-paid":
					delete(b, "details")
				case "foreign-wallet":
					p["wallet_id"] = "other"
				case "wrong-hash":
					p["payment_hash"] = strings.Repeat("a", 64)
				case "wrong-direction":
					p["amount"] = -21000
				case "wrong-amount":
					p["amount"] = 22000
				case "conflicting-state":
					p["status"] = "pending"
				case "conflicting-invoice":
					p["payment_request"] = "other"
				case "bad-preimage":
					p["preimage"] = strings.Repeat("b", 64)
				case "bad-json":
					fmt.Fprint(w, "{")
					return
				case "trailing-json":
					fmt.Fprint(w, `{} {}`)
					return
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", (1<<20)+1))
					return
				}
				json.NewEncoder(w).Encode(b)
			})
			if _, err := c.Lookup(context.Background(), hash, Incoming); err == nil {
				t.Fatal("unsafe settlement accepted")
			}
		})
	}
}

// The successful send must verify the admin wallet before its monetary POST.
// Foreign or missing identity refusal is covered separately below, and
// ambiguous POST retry refusal by TestAmbiguousSendRedactsAndNeverRetries.
func TestSendSettlesAndChecksWalletBeforePosting(t *testing.T) {
	bolt, hash, pre := testInvoice(t, 21000)
	var posts, gets atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-admin-key" {
			t.Error("wrong admin credential")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/api/v1/wallet":
			if gets.Add(1) != 1 || posts.Load() != 0 {
				t.Error("wallet verification must happen once before payment")
			}
			walletJSON(w)
			return
		case r.Method == http.MethodPost && r.URL.Path == "/proxy/api/v1/payments":
			if gets.Load() != 1 {
				t.Error("monetary POST attempted before admin wallet verification")
			}
			posts.Add(1)
		default:
			t.Error("unexpected send request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var q map[string]any
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error("invalid send request body")
		}
		if q["out"] != true || q["bolt11"] != bolt || q["external_id"] != "payout-1" {
			t.Error("wrong send")
		}
		json.NewEncoder(w).Encode(paymentJSON(bolt, hash, pre, "success", -21000))
	})
	p, err := c.PayInvoice(context.Background(), bolt, "payout-1")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Settled() {
		t.Fatal("not settled")
	}
	if _, err := c.PayInvoice(context.Background(), "invalid", "bad"); err == nil {
		t.Fatal("invalid invoice accepted")
	}
	if gets.Load() != 1 || posts.Load() != 1 {
		t.Fatal("send retried or invalid invoice reached the provider")
	}
}
func TestWrongAdminWalletNeverSends(t *testing.T) {
	bolt, _, _ := testInvoice(t, 1000)
	var posts atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
		}
		fmt.Fprint(w, `{"id":"other","balance":1000}`)
	})
	if _, err := c.PayInvoice(context.Background(), bolt, "payout"); err == nil {
		t.Fatal("foreign wallet accepted")
	}
	if posts.Load() != 0 {
		t.Fatal("payment attempted")
	}
}

// PayInvoice must obtain the exact wallet ID from the admin credential's
// wallet response before any monetary POST. Purpose must not be inferred
// from key values: even when the admin and invoice credentials coincide, a
// response without a wallet ID refuses the send.
func TestSendRequiresAdminWalletIDBeforePOST(t *testing.T) {
	bolt, _, _ := testInvoice(t, 1000)
	for _, tc := range []struct{ name, adminKey string }{
		{"distinct credentials", "test-admin-key"},
		{"same credentials", "test-invoice-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
				}
				invoiceWalletJSON(w)
			}))
			t.Cleanup(s.Close)
			c, err := New(Config{BaseURL: s.URL + "/proxy", WalletID: "wallet-a", InvoiceKey: "test-invoice-key", AdminKey: tc.adminKey, Network: "bc", HTTPClient: s.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.PayInvoice(context.Background(), bolt, "payout"); err == nil {
				t.Fatal("send without verified wallet identity accepted")
			}
			if posts.Load() != 0 {
				t.Fatal("monetary POST attempted without wallet identity")
			}
		})
	}
}
func TestAmbiguousSendRedactsAndNeverRetries(t *testing.T) {
	bolt, _, _ := testInvoice(t, 1000)
	var posts atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			walletJSON(w)
			return
		}
		posts.Add(1)
		w.WriteHeader(500)
		fmt.Fprint(w, "test-admin-key private-response")
	})
	_, err := c.PayInvoice(context.Background(), bolt, "payout")
	if err == nil || !IsUnknown(err) {
		t.Fatalf("not ambiguous: %v", err)
	}
	if strings.Contains(err.Error(), "test-admin-key") || strings.Contains(err.Error(), "private-response") {
		t.Fatal("secret leaked")
	}
	if posts.Load() != 1 {
		t.Fatal("POST retried")
	}
}
func TestRedirectRefused(t *testing.T) {
	var hit atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
	defer s.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, s.URL, 307) })
	if _, err := c.Wallet(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if hit.Load() != 0 {
		t.Fatal("credential forwarded")
	}
}
func TestReceiveOnlyCannotSpendAndOverflowNeverCallsServer(t *testing.T) {
	bolt, _, _ := testInvoice(t, 1000)
	var calls atomic.Int32
	c, s := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); walletJSON(w) })
	readOnly, err := New(Config{BaseURL: s.URL, WalletID: "wallet-a", InvoiceKey: "test-invoice-key", Network: "bc", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readOnly.PayInvoice(context.Background(), bolt, "payout"); err == nil {
		t.Fatal("receive-only connection spent")
	}
	if _, err = c.CreateInvoice(context.Background(), InvoiceRequest{AmountSat: ^uint64(0), Expiry: 60, ExternalID: "invalid"}); err == nil {
		t.Fatal("amount overflow accepted")
	}
	if _, err = c.Lookup(context.Background(), "../wallet", Incoming); err == nil {
		t.Fatal("path injection accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached provider")
	}
}
func TestUnsafeEndpointAndNetworkRefused(t *testing.T) {
	for _, base := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?key=x", "https://example.com/#fragment", "file:///tmp/socket"} {
		if _, err := New(Config{BaseURL: base, WalletID: "wallet-a", InvoiceKey: "key", Network: "bc"}); err == nil {
			t.Fatalf("unsafe endpoint accepted %s", base)
		}
	}
	if _, err := New(Config{BaseURL: "https://example.com", WalletID: "wallet-a", InvoiceKey: "key", Network: ""}); err == nil {
		t.Fatal("unspecified network accepted")
	}
}
func TestSendProofMismatchRetainsUnknownOutcome(t *testing.T) {
	bolt, hash, _ := testInvoice(t, 1000)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			walletJSON(w)
			return
		}
		json.NewEncoder(w).Encode(paymentJSON(bolt, hash, strings.Repeat("f", 64), "success", -1000))
	})
	if _, err := c.PayInvoice(context.Background(), bolt, "payout"); err == nil || !IsUnknown(err) {
		t.Fatal("unverified outgoing success was accepted or treated as definite failure")
	}
}
func TestCancelledContextCannotSend(t *testing.T) {
	bolt, _, _ := testInvoice(t, 1000)
	var calls atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.PayInvoice(ctx, bolt, "payout"); err == nil {
		t.Fatal("cancelled operation succeeded")
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled operation reached provider")
	}
}
func TestClientFormattingCannotExposeCredentials(t *testing.T) {
	c := &Client{walletID: "wallet-a", invoiceKey: "test-invoice-key", adminKey: "test-admin-key"}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"pointer", c},
		{"value", *c},
	} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				got := fmt.Sprintf(format, tc.value)
				if strings.Contains(got, "test-invoice-key") || strings.Contains(got, "test-admin-key") {
					t.Fatal("client formatting exposes a synthetic credential")
				}
				if !strings.Contains(got, "LNbits client (credentials redacted)") {
					t.Fatal("client redaction marker missing")
				}
			})
		}
	}
}
