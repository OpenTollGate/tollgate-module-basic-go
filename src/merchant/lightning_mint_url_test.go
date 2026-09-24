package merchant

// The Lightning lane's mint lookup, pinned against the shape the shipped
// default config and the captive portal actually produce it in.
//
// The portal does not invent a mint URL: it echoes the advertisement's
// `price_per_step` tag, which is `accepted_mints[].url` verbatim — and the
// shipped default writes a bare mint host with no trailing slash
// ("https://mint.example.com"). The wallet, however, registers a mint in
// canonical form and keys its mint map by that exact string, which for a
// path-less URL is "https://mint.example.com/" — the canonicaliser gives an
// empty path the single "/" it stands for. A lookup with the un-slashed
// spelling therefore misses and the lane answers 400 {"error":"failed to create
// lightning invoice"} (module log: error="mint does not exist"). On a default
// install that means the Lightning lane cannot sell time at all, while the
// Cashu lane — which never takes a mint URL from the client — keeps working,
// which is what hid it.
//
// The 2x2 matrix over {config with/without slash} x {request with/without
// slash} shows only the requests carrying the slash ever succeeded, regardless
// of the config form. These tests pin the fix: the module canonicalises the
// client-supplied URL before it is used as a lookup key or stored, so both
// spellings resolve to the one registered mint, and a different mint is still
// refused.

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

const (
	// advertisedMintURL is the spelling the shipped default config writes into
	// accepted_mints[].url (a bare host, no trailing slash), and therefore the
	// one the portal echoes back on the Lightning lane.
	advertisedMintURL = "https://mint.example.com"
	// registeredMintURL is what the wallet registers and keys the mint by: the
	// canonical form of the same mint, i.e. "<url>/".
	registeredMintURL = "https://mint.example.com/"
	// The same pair for a mint that lives under a path, where canonicalisation
	// collapses trailing slashes instead of adding one.
	advertisedMintPathURL = "https://mint.example.com/Bitcoin"
	registeredMintPathURL = "https://mint.example.com/Bitcoin"
)

// errMintNotExist mirrors the wallet's own error for a mint it has no entry
// for (gonuts wallet.ErrMintNotExist, "mint does not exist").
var errMintNotExist = errors.New("mint does not exist")

// mintKeyedWallet stands in for the wallet port the way the mint map behaves:
// RequestMintQuote answers only for a key it was registered with, compared as
// an exact string, and reports "mint does not exist" for everything else. It is
// deliberately NOT tolerant of URL spellings — that tolerance is what the
// module is supposed to provide, so building it into the double would hide the
// defect.
type mintKeyedWallet struct {
	tollwallet.WalletPort

	mu         sync.Mutex
	registered []string
	requested  []string
}

func (w *mintKeyedWallet) RequestMintQuote(amount uint64, mintURL string) (*tollwallet.MintQuote, error) {
	w.mu.Lock()
	w.requested = append(w.requested, mintURL)
	known := false
	for _, key := range w.registered {
		if key == mintURL {
			known = true
			break
		}
	}
	w.mu.Unlock()

	if !known {
		return nil, errMintNotExist
	}

	return &tollwallet.MintQuote{
		QuoteID: "quote-1",
		Request: "lnbc1pinnedinvoice",
		State:   tollwallet.StateUnpaid,
		Amount:  amount,
		Expiry:  uint64(time.Now().Add(time.Hour).Unix()),
	}, nil
}

func (w *mintKeyedWallet) GetMintQuoteState(string) (tollwallet.MintQuoteState, error) {
	return tollwallet.StateUnpaid, nil
}

func (w *mintKeyedWallet) seen() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requested...)
}

// newLightningLaneMerchant builds the smallest Merchant that can serve
// RequestLightningInvoice: pricing config, the wallet port, and the in-memory
// quote map the lane writes its record into.
func newLightningLaneMerchant(configuredMintURL string, wallet *mintKeyedWallet) *Merchant {
	return &Merchant{
		config: &config_manager.Config{
			Metric:   "bytes",
			StepSize: 22020096,
			AcceptedMints: []config_manager.MintConfig{
				{URL: configuredMintURL, PricePerStep: 1, PriceUnit: "sat"},
			},
		},
		tollwallet:      wallet,
		lightningQuotes: map[string]*lightningQuoteRecord{},
	}
}

const (
	lightningLaneMAC    = "02:00:00:00:00:20"
	lightningLaneAmount = 210
)

// TestRequestLightningInvoice_MintURLEverySpelling exercises the 2x2 matrix the
// defect was recorded with: both config forms crossed with both request forms
// must each return an invoice. Before the fix only the requests carrying the
// trailing slash did.
func TestRequestLightningInvoice_MintURLEverySpelling(t *testing.T) {
	cases := []struct {
		name        string
		configured  string
		requestMint string
		walletKey   string
		wasWorking  bool // true = this cell already worked before the fix
	}{
		{
			name:       "config without slash, request without slash",
			configured: advertisedMintURL, requestMint: advertisedMintURL,
			walletKey: registeredMintURL, wasWorking: false,
		},
		{
			name:       "config without slash, request with slash",
			configured: advertisedMintURL, requestMint: registeredMintURL,
			walletKey: registeredMintURL, wasWorking: true,
		},
		{
			name:       "config with slash, request without slash",
			configured: registeredMintURL, requestMint: advertisedMintURL,
			walletKey: registeredMintURL, wasWorking: false,
		},
		{
			name:       "config with slash, request with slash",
			configured: registeredMintURL, requestMint: registeredMintURL,
			walletKey: registeredMintURL, wasWorking: true,
		},
		{
			// The same defect under a path-bearing mint, where canonicalisation
			// collapses the trailing slash rather than adding one.
			name:       "mint under a path, request with a trailing slash",
			configured: advertisedMintPathURL, requestMint: advertisedMintPathURL + "/",
			walletKey: registeredMintPathURL, wasWorking: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wallet := &mintKeyedWallet{registered: []string{tc.walletKey}}
			m := newLightningLaneMerchant(tc.configured, wallet)

			invoice, err := m.RequestLightningInvoice(lightningLaneMAC, tc.requestMint, lightningLaneAmount)
			if err != nil {
				t.Fatalf("RequestLightningInvoice(mint_url=%q) with configured %q: %v\n"+
					"a request spelling that names the registered mint must find it (this cell "+
					"worked before the fix: %v)",
					tc.requestMint, tc.configured, err, tc.wasWorking)
			}
			if invoice == nil || invoice.QuoteID == "" || invoice.Invoice == "" {
				t.Fatalf("RequestLightningInvoice(mint_url=%q) returned %+v, want an invoice with a quote id and a bolt11", tc.requestMint, invoice)
			}

			seen := wallet.seen()
			if len(seen) != 1 {
				t.Fatalf("wallet port saw %d quote requests, want exactly 1: %v", len(seen), seen)
			}
			if seen[0] != tc.walletKey {
				t.Errorf("the wallet was asked for mint %q, want the registered key %q: the module must "+
					"canonicalise the client's spelling before the lookup, not pass it through",
					seen[0], tc.walletKey)
			}
		})
	}
}

// TestRequestLightningInvoice_DifferentMintStillRefused pins that
// canonicalisation cannot invent a mint identity of its own: a request for a
// DIFFERENT mint still fails, while a differently-cased spelling of the
// configured one still resolves.
func TestRequestLightningInvoice_DifferentMintStillRefused(t *testing.T) {
	wallet := &mintKeyedWallet{registered: []string{registeredMintURL}}
	m := newLightningLaneMerchant(advertisedMintURL, wallet)

	if _, err := m.RequestLightningInvoice(lightningLaneMAC, "https://mint.other.example", 210); err == nil {
		t.Fatal("RequestLightningInvoice for a mint that is neither configured nor registered must fail, got an invoice")
	}

	wallet2 := &mintKeyedWallet{registered: []string{registeredMintURL}}
	m2 := newLightningLaneMerchant(advertisedMintURL, wallet2)
	if _, err := m2.RequestLightningInvoice(lightningLaneMAC, "https://MINT.example.com/", 210); err != nil {
		t.Fatalf("RequestLightningInvoice with a non-canonical host case must still resolve to the "+
			"registered mint: %v", err)
	}
}

// TestRequestLightningInvoice_StoresCanonicalMint pins that the quote record
// and the response carry the one canonical identity, so the status poll, the
// allotment lookup and the eventual grant all address the same mint.
func TestRequestLightningInvoice_StoresCanonicalMint(t *testing.T) {
	wallet := &mintKeyedWallet{registered: []string{registeredMintURL}}
	m := newLightningLaneMerchant(advertisedMintURL, wallet)

	invoice, err := m.RequestLightningInvoice(lightningLaneMAC, advertisedMintURL, lightningLaneAmount)
	if err != nil {
		t.Fatalf("RequestLightningInvoice: %v", err)
	}
	if invoice.MintURL != registeredMintURL {
		t.Errorf("invoice mint_url = %q, want the canonical %q", invoice.MintURL, registeredMintURL)
	}

	record, err := m.getLightningQuoteRecord(invoice.QuoteID)
	if err != nil {
		t.Fatalf("getLightningQuoteRecord(%q): %v", invoice.QuoteID, err)
	}
	if record.MintURL != registeredMintURL {
		t.Errorf("stored quote mint = %q, want the canonical %q", record.MintURL, registeredMintURL)
	}
}
