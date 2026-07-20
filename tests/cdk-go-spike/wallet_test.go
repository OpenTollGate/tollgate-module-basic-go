//go:build cgo && linux && (amd64 || arm64)

package main

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

// testMintUrl is the cdk-go project's canonical testmint. It auto-pays the
// bolt11 invoices it issues, so a mint quote reaches QuoteStatePaid shortly
// after being requested. See cdk-go's own cdk_test.go for the same URL.
const testMintUrl = "https://testnut.cashudevkit.org"

// TestCdkGoTestmintWallet exercises the REAL cdk-go wallet path end-to-end
// against a live Cashu testmint: mnemonic → wallet → mint info → mint quote
// (bolt11) → poll until paid → mint tokens → balance reflects the mint.
//
// It is gated behind CDK_SPIKE_NETWORK=1 so CI stays green by default; the
// test SKIPS (not fails) when the env var is unset. The captured -v output
// is the real-surface artifact proving cdk-go's Wallet API works against a
// live mint.
func TestCdkGoTestmintWallet(t *testing.T) {
	if os.Getenv("CDK_SPIKE_NETWORK") != "1" {
		t.Skip("skipping network test; set CDK_SPIKE_NETWORK=1 to run")
	}

	var mnemonic string
	var wallet *cdk_ffi.Wallet
	var quoteId string

	// Register wallet teardown on the PARENT test so it survives across all
	// subtests (registering inside t.Run runs Cleanup when the subtest ends,
	// which would destroy the wallet before later subtests can use it).
	t.Cleanup(func() {
		if wallet != nil {
			wallet.Destroy()
		}
	})

	// a) Generate a fresh mnemonic.
	t.Run("generate_mnemonic", func(t *testing.T) {
		m, err := cdk_ffi.GenerateMnemonic()
		if err != nil {
			t.Fatalf("GenerateMnemonic failed: %v", err)
		}
		if m == "" {
			t.Fatal("GenerateMnemonic returned empty string")
		}
		words := strings.Fields(m)
		// BIP-39 mnemonics are 12/15/18/21/24 words; accept the common ones.
		if len(words) != 12 && len(words) != 24 {
			t.Fatalf("expected 12 or 24 mnemonic words, got %d: %q", len(words), m)
		}
		mnemonic = m
		t.Logf("generated mnemonic (%d words)", len(words))
	})

	// b) Open a wallet against the testmint with a temp-dir SQLite store.
	t.Run("create_wallet", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "wallet.sqlite")
		t.Logf("sqlite store: %s", dbPath)
		w, err := cdk_ffi.NewWallet(
			testMintUrl,
			cdk_ffi.CurrencyUnitSat{},
			mnemonic,
			cdk_ffi.WalletStoreSqlite{Path: dbPath},
			cdk_ffi.WalletConfig{TargetProofCount: nil},
		)
		if err != nil {
			t.Fatalf("NewWallet failed: %v", err)
		}
		if w == nil {
			t.Fatal("NewWallet returned nil wallet")
		}
		wallet = w
		t.Logf("wallet opened against %s", testMintUrl)
	})

	// Preflight reachability check. cdk-go's FFI does not propagate HTTP
	// errors as Go errors — when the mint is unreachable the Rust layer
	// returns an empty buffer and the Go deserializer panics on EOF. Skip
	// the remaining network subtests cleanly here (after the local FFI
	// subtests have already passed, exercising the binding) so a down mint
	// or blocked egress is obvious in the transcript instead of surfacing
	// as a CGO panic that reads like a code bug.
	mintHost := mustMintHost(t, testMintUrl)
	if err := dialCheck(mintHost, 5*time.Second); err != nil {
		t.Skipf("testmint unreachable from this host (FFI would panic on dead-mint EOF; local cdk-go subtests above already passed): %v", err)
	}

	// c) Fetch mint info — proves the wallet can reach the mint and parse NUT-06 info.
	t.Run("fetch_mint_info", func(t *testing.T) {
		info, err := wallet.FetchMintInfo()
		if err != nil {
			t.Fatalf("FetchMintInfo failed: %v", err)
		}
		if info == nil {
			t.Fatal("FetchMintInfo returned nil MintInfo")
		}
		if info.Name != nil {
			t.Logf("mint name: %s", *info.Name)
		}
		if info.Description != nil {
			t.Logf("mint description: %s", *info.Description)
		}
		if info.Pubkey != nil {
			t.Logf("mint pubkey: %s", *info.Pubkey)
		}
	})

	// d) Request a mint quote for 1 sat via bolt11. The testmint returns a
	// bolt11 invoice that it will settle itself.
	t.Run("request_mint_quote", func(t *testing.T) {
		amount := cdk_ffi.Amount{Value: 1} // 1 sat
		quote, err := wallet.MintQuote(cdk_ffi.PaymentMethodBolt11{}, &amount, nil, nil)
		if err != nil {
			t.Fatalf("MintQuote failed: %v", err)
		}
		if quote.Id == "" {
			t.Fatal("MintQuote returned empty quote Id")
		}
		if quote.Request == "" {
			t.Fatal("MintQuote returned empty Request (bolt11 invoice)")
		}
		quoteId = quote.Id
		t.Logf("quote id: %s", quoteId)
		t.Logf("bolt11 request: %s", quote.Request)
	})

	// e) Poll the mint until the quote is paid. The testmint auto-pays quickly;
	// we allow up to 30s. QuoteStatePaid (2) means the invoice was settled;
	// QuoteStateIssued (4) is also acceptable since it implies payment received
	// and tokens already issued.
	t.Run("wait_for_quote_paid", func(t *testing.T) {
		deadline := time.Now().Add(30 * time.Second)
		var lastState cdk_ffi.QuoteState
		for time.Now().Before(deadline) {
			quote, err := wallet.CheckMintQuoteStatus(quoteId)
			if err != nil {
				t.Fatalf("CheckMintQuoteStatus failed: %v", err)
			}
			lastState = quote.State
			t.Logf("quote state: %d", quote.State)
			if quote.State == cdk_ffi.QuoteStatePaid || quote.State == cdk_ffi.QuoteStateIssued {
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("quote %s did not reach PAID within 30s (last state=%d)", quoteId, lastState)
	})

	// f) Mint tokens against the now-paid quote.
	t.Run("mint_tokens", func(t *testing.T) {
		proofs, err := wallet.Mint(quoteId, cdk_ffi.SplitTargetNone{}, nil)
		if err != nil {
			t.Fatalf("Mint failed: %v", err)
		}
		if len(proofs) == 0 {
			t.Fatal("Mint returned zero proofs")
		}
		total, err := cdk_ffi.ProofsTotalAmount(proofs)
		if err != nil {
			t.Fatalf("ProofsTotalAmount failed: %v", err)
		}
		t.Logf("minted %d proofs totalling %d sats", len(proofs), total.Value)
	})

	// g) The wallet's total balance must now reflect the minted tokens.
	t.Run("total_balance_reflects_mint", func(t *testing.T) {
		balance, err := wallet.TotalBalance()
		if err != nil {
			t.Fatalf("TotalBalance failed: %v", err)
		}
		t.Logf("wallet total balance: %d sats", balance.Value)
		if balance.Value < 1 {
			t.Fatalf("expected balance >= 1 sat after mint, got %d", balance.Value)
		}
	})
}

func mustMintHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse mint URL %q: %v", rawURL, err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host + ":" + port
}

func dialCheck(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}
