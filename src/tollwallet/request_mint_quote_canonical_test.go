package tollwallet

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// newQuoteMint stands up the same hermetic NUT-01/02 endpoint set as
// newTestMint (duplicate_mint_registry_test.go) plus a NUT-04 bolt11
// quote endpoint that counts every POST, so a test can prove a quote
// request actually reached the mint and was served.
func newQuoteMint(t *testing.T) (server *httptest.Server, keysetID, pubKeyHex string, quotePosts *atomic.Int64) {
	t.Helper()

	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubKeyHex = hex.EncodeToString(priv.PubKey().SerializeCompressed())
	keysetID = strings.Repeat("ab", 16) // hex-decodable, as AddMint requires

	var posts atomic.Int64

	mux := http.NewServeMux()
	serveKeys := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"keysets":[{"id":"%s","unit":"sat","keys":{"1":"%s"},"active":true,"input_fee_ppk":0}]}`, keysetID, pubKeyHex)
	}
	serveKeysets := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"keysets":[{"id":"%s","unit":"sat","active":true,"input_fee_ppk":0}]}`, keysetID)
	}
	serveQuote := func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"quote":"quote-canonical-0001","request":"nonstandard-invoice-string","amount":21,"unit":"sat","state":"UNPAID","expiry":4102444800}`))
	}
	for _, prefix := range []string{"/v1", "/Bitcoin/v1"} {
		mux.HandleFunc(prefix+"/keys", serveKeys)
		mux.HandleFunc(prefix+"/keysets", serveKeysets)
		mux.HandleFunc(prefix+"/mint/quote/bolt11", serveQuote)
	}
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, keysetID, pubKeyHex, &posts
}

// TestRequestMintQuote_CanonicalizesMintURL pins the Lightning-lane
// defect the happy-path suite tracks as its one known issue: the portal
// echoes the advertisement's mint URL verbatim, TollWallet registers
// mints under normalizeMintURL's canonical spelling, and the underlying
// gonuts wallet resolves mints by exact string match against that
// canonical key. RequestMintQuote must canonicalize before delegating,
// or a registered, healthy mint answers "mint does not exist" and every
// Lightning-lane quote POST fails with HTTP 400.
//
// Both spelling pairs are one logical mint: the root-path pair mirrors
// production (the advertisement carries no trailing slash; the wallet
// key does), the /Bitcoin pair mirrors the issue #375 alias class.
func TestRequestMintQuote_CanonicalizesMintURL(t *testing.T) {
	server, _, _, quotePosts := newQuoteMint(t)

	cases := []struct {
		name       string
		registered string
		advertised string
	}{
		{"root path: advertisement without trailing slash", strings.TrimSuffix(server.URL, "/") + "/", strings.TrimSuffix(server.URL, "/")},
		{"path alias: trailing-slash spelling of the same mint", server.URL + "/Bitcoin", server.URL + "/Bitcoin/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tw, err := New(dir, []string{tc.registered}, false)
			if err != nil {
				t.Fatalf("create wallet: %v", err)
			}
			defer tw.Shutdown()

			quote, err := tw.RequestMintQuote(21, tc.advertised)
			if err != nil {
				t.Fatalf("RequestMintQuote(advertised=%q) failed: %v — the mint IS registered (as %q); the underlying wallet resolves mints by exact string, so a non-canonical spelling must be canonicalized at this seam (happy-path Lightning-lane 400, issue #375 alias class)", tc.advertised, err, tc.registered)
			}
			if quote.Quote != "quote-canonical-0001" {
				t.Errorf("quote id = %q, want quote-canonical-0001", quote.Quote)
			}
			if quote.Amount != 21 {
				t.Errorf("quote amount = %d, want 21", quote.Amount)
			}
		})
	}

	if got := quotePosts.Load(); got != int64(len(cases)) {
		t.Errorf("mint received %d quote POSTs, want %d (each case must reach the mint, not just pass a local guard)", got, len(cases))
	}
}
