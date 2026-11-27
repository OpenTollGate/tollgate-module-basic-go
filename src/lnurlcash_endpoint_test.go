package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// The endpoint is the only place a bearer secret enters the gate from an
// untrusted client, so these tests are about refusals. Each one names the failure
// it prevents; a test that only proves the happy path would let any of the
// following ship.

// A minimal real note: the LUD-25 bearer vector, plus an issuer hint so the bare
// k1 form can be parsed.
const endpointTestSpend = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

type stubIssuer struct {
	state lnurlcash.IssuerState
	err   error
	calls int
}

func (s *stubIssuer) Info(_ context.Context, _ lnurlcash.Note, _ string) (lnurlcash.IssuerState, error) {
	s.calls++
	return s.state, s.err
}

func endpointTestRailWithStore(t *testing.T, melted *bool) (*merchant.LNURLCashRail, *paymentjournal.Store) {
	t.Helper()
	store, err := paymentjournal.Open(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return merchant.NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			t.Fatal("an unaccepted mint must be refused before any invoice is requested")
			return "", "", nil
		},
		func(note lnurlcash.Note, callback merchant.IssuerCallback, invoice string) error {
			*melted = true
			return nil
		},
	), store
}

func endpointTestRail(t *testing.T, melted *bool) *merchant.LNURLCashRail {
	t.Helper()
	store, err := paymentjournal.Open(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return merchant.NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-endpoint", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback merchant.IssuerCallback, invoice string) error {
			*melted = true
			return nil
		},
	)
}

// installMerchantKnowingConfig puts a merchant in the provider whose accepted list is the
// config's, which is what a fully-started gate has.
//
// The testenv build leaves the provider holding startingMerchant{}, whose
// GetAcceptedMints() is nil — correct for a starting gate, and useless for testing the
// redemption path, because mintAccepted now asks the MERCHANT (the authority the allotment
// code consults) rather than the live config. Without this, every endpoint test is refused
// at the mint gate before it reaches what it is testing.
func installMerchantKnowingConfig(t *testing.T) {
	t.Helper()
	cfg := configManager.GetConfig()
	if cfg == nil || len(cfg.AcceptedMints) == 0 {
		t.Skip("no accepted mints in this config")
	}
	prev := merchantProvider.inner.GetMerchant()
	merchantProvider.inner.SetMerchant(&staleMintMerchant{accepted: append([]config_manager.MintConfig(nil), cfg.AcceptedMints...)})
	t.Cleanup(func() { merchantProvider.inner.SetMerchant(prev) })
}

// acceptedMintForTest returns a mint URL this gate's config actually accepts.
//
// The handler refuses an unaccepted mint before journaling (that is the fix), so these
// tests must pay through a mint the gate serves. Reading it from config keeps the test
// from hardcoding a third-party host that could be added or removed in config.
func acceptedMintForTest(t *testing.T) string {
	t.Helper()
	cfg := configManager.GetConfig()
	if cfg == nil || len(cfg.AcceptedMints) == 0 {
		t.Skip("no accepted mints in this config; the redemption path needs one")
	}
	return cfg.AcceptedMints[0].URL
}

// resolveTestDevice makes the request's socket address resolve to a MAC, so the
// device-identity guard passes. It is deliberately not bypassed: the guard's own
// refusal is covered separately, and a test that stubbed identity out would stop
// exercising the same path production takes.
func resolveTestDevice(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	dhcpLeasePath = filepath.Join(dir, "dhcp.leases")
	// httptest's default RemoteAddr is 192.0.2.1:1234.
	if err := os.WriteFile(dhcpLeasePath, []byte("1700000000 00:00:00:00:00:01 192.0.2.1 host 01:00:00:00:00:01:00:00\n"), 0644); err != nil {
		t.Fatalf("writing the lease fixture: %v", err)
	}
	t.Cleanup(func() { dhcpLeasePath = "/tmp/dhcp.leases" })
}

func postRedeem(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/lnurlcash/redeem", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// THE OPT-IN GATE. A disabled rail must be indistinguishable from one that does
// not exist, and it must not touch the journal, the mint or the issuer.
func TestRedeemRefusesWhenRailIsDisabled(t *testing.T) {
	resolveTestDevice(t)
	var melted bool
	var issuerCalled bool
	rail := endpointTestRail(t, &melted)

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return false, "lnurlcash.enabled is false (the rail is opt-in)" },
		func(string) (lnurlCashIssuer, error) {
			issuerCalled = true
			return &stubIssuer{}, nil
		})

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"mint.example"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("a disabled rail must answer 404, got %d", rec.Code)
	}
	if issuerCalled || melted {
		t.Fatal("a disabled rail must not reach the issuer or melt anything")
	}
	if !strings.Contains(rec.Body.String(), "lnurlcash_disabled") {
		t.Fatalf("the refusal must name the reason, got %s", rec.Body.String())
	}
}

// A DEFINITIVE issuer refusal means the note will never work. Nothing was spent,
// and the customer must be told not to bother retrying.
func TestRedeemReportsDefinitiveIssuerRefusal(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{err: &lnurlcash.IssuerError{
		Reason:     "already in use",
		Definitive: true,
	}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"mint.example"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a definitive refusal, got %d", rec.Code)
	}
	if melted {
		t.Fatal("a definitively refused note must never reach the melt")
	}
	if !strings.Contains(rec.Body.String(), "note_refused") {
		t.Fatalf("expected the note_refused code, got %s", rec.Body.String())
	}
}

// AN AMBIGUOUS LOOKUP IS NOT A REFUSAL, AND MUST NOT SPEND. A timeout on the
// informational GET means we do not know the note's value; it does not mean the
// note is bad. The critical assertion is that nothing reached the melt.
func TestRedeemDoesNotSpendWhenIssuerLookupIsAmbiguous(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{err: &lnurlcash.IssuerError{
		Reason:     "pending",
		Definitive: false,
	}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"mint.example"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for an ambiguous lookup, got %d", rec.Code)
	}
	if melted {
		t.Fatal("an inconclusive lookup must never reach the melt — no bearer secret leaves the router")
	}
	if !strings.Contains(rec.Body.String(), "issuer_unreachable") {
		t.Fatalf("expected issuer_unreachable, got %s", rec.Body.String())
	}
}

// A ZERO-VALUE ANSWER BUYS NOTHING. Reported as redeemable-but-worthless, and the
// melt is never attempted.
func TestRedeemRefusesAZeroValueNote(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 0}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"mint.example"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a zero-value note, got %d", rec.Code)
	}
	if melted {
		t.Fatal("a note the issuer values at zero must never reach the melt")
	}
}

// THE HAPPY PATH. Proves the endpoint actually wires issuer → amount → rail →
// melt, which is the one thing a suite of refusal tests cannot see.
func TestRedeemHappyPathReturnsAQuoteAndGrantsNothing(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 1000}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(domain string) (lnurlCashIssuer, error) {
			if domain != "mint.example" {
				t.Fatalf("the issuer was looked up by the wrong domain: %q", domain)
			}
			return issuer, nil
		})

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"mint.example"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !melted {
		t.Fatal("the endpoint returned success without melting the note")
	}
	var resp lnurlCashRedeemResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("the response is not JSON: %v", err)
	}
	if resp.Status != 1 || resp.QuoteID != "quote-endpoint" {
		t.Fatalf("expected status 1 and the quote id, got %+v", resp)
	}
	// The endpoint must not claim to have granted anything: access comes from the
	// quote settling, through the existing monitor.
	if strings.Contains(strings.ToLower(rec.Body.String()), "grant") {
		t.Fatalf("the endpoint must not report a grant; it reports a quote: %s", rec.Body.String())
	}
}

// A NOTE WITH NO ISSUER HINT CANNOT BE PARSED. A bare k1 names no host, and guessing
// one would let the caller choose which host we talk to.
func TestRedeemRefusesABareSpendWithNoIssuer(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	var issuerCalled bool

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) {
			issuerCalled = true
			return &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 1000}}, nil
		})

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bare spend without an issuer must be refused as invalid, got %d", rec.Code)
	}
	if issuerCalled || melted {
		t.Fatal("an unparseable note must never reach the issuer")
	}
}

// THE ALLOWLIST IS THE SSRF BOUNDARY. A note naming a host the operator did not
// allow must be refused before any network call.
func TestRedeemRefusesAnIssuerOutsideTheAllowlist(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	var issuerCalled bool

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(domain string) (lnurlCashIssuer, error) {
			issuerCalled = true
			return nil, errors.New("not allowlisted")
		})

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"`+acceptedMintForTest(t)+`","issuer":"evil.example"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a disallowed issuer, got %d", rec.Code)
	}
	_ = issuerCalled // the resolver refuses, so no client is handed out
	if melted {
		t.Fatal("a note naming a disallowed issuer must never be melted")
	}
}

// GET IS NOT A REDEMPTION. A bearer note in a URL query would land in proxy logs.
func TestRedeemRejectsNonPost(t *testing.T) {
	var melted bool
	rail := endpointTestRail(t, &melted)
	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return &stubIssuer{}, nil })

	req := httptest.NewRequest(http.MethodGet, "/lnurlcash/redeem?note="+endpointTestSpend, nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	if melted {
		t.Fatal("a GET must never reach the melt")
	}
}

// AN OVERSIZED BODY IS A 413, NOT AN ALLOCATION.
func TestRedeemRejectsAnOversizedBody(t *testing.T) {
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return &stubIssuer{}, nil })

	big := `{"note":"` + strings.Repeat("a", maxLNURLCashBodyBytes+100) + `","mint":"` + acceptedMintForTest(t) + `"}`
	rec := postRedeem(t, h, big)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if melted {
		t.Fatal("an oversized body must never reach the melt")
	}
}

// A REPLAY IS REPORTED SO THE CUSTOMER CAN RECOVER, NOT AS A BARE FAILURE. If the
// note was already redeemed and a grant exists, "no" without "check your session"
// loses them their money.
func TestRedeemReportsAReplayAsRecoverable(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 1000}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	body := `{"note":"` + endpointTestSpend + `","mint":"` + acceptedMintForTest(t) + `","issuer":"mint.example"}`

	if rec := postRedeem(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("first presentation should succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := postRedeem(t, h, body)

	if rec.Code != http.StatusConflict {
		t.Fatalf("a second presentation must be a conflict, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "note_already_presented") {
		t.Fatalf("expected note_already_presented, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Check your session") {
		t.Fatalf("the replay message must point the customer at their session, got %s", rec.Body.String())
	}
}

// AN UNTHROTTLED REDEMPTION IS AN AMPLIFIER.
//
// Every redemption makes the gate issue an outbound request to a third-party issuer
// from the gate's own address. Without the limiter on this route, one guest behind the
// portal can make the gate hammer each issuer on the allowlist as fast as they can
// type — burning the operator's standing with issuers that rate-limit or block by IP,
// and degrading redemptions for every other guest behind the same NAT address.
//
// WHY THIS READS THE SOURCE INSTEAD OF DRIVING THE MUX: the redemption route is only
// registered when the operator has opted in, and the testenv config has the rail off
// (correctly — that is the shipped default). A mux-driven version of this test therefore
// exercised the "/" catch-all, passed with the limiter REMOVED, and proved nothing. The
// negative control is what caught it: red-without-the-fix is the only thing that makes a
// green meaningful, and this one stayed green.
//
// So assert the wiring where it is unambiguous: the registration line must carry the
// limiter, and it must sit outermost so the budget is spent before any network call.
func TestRedeemRouteIsRateLimited(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	body := string(src)

	const route = `mux.HandleFunc("/lnurlcash/redeem"`
	i := strings.Index(body, route)
	if i < 0 {
		t.Fatal("the /lnurlcash/redeem route is not registered at all")
	}
	// Look at the registration block only: from the HandleFunc to the following
	// top-level closing brace.
	rest := body[i:]
	end := strings.Index(rest, "\n\t\t\t\t})")
	if end < 0 {
		end = len(rest)
	}
	registration := rest[:end]

	if !strings.Contains(registration, "RateLimitMiddleware(") {
		t.Fatal("the redemption route is not behind RateLimitMiddleware: one guest can make the gate hammer every allowlisted issuer, using the gate's own address")
	}
	// Outermost: RateLimitMiddleware(CorsMiddleware(requireStarted(...)))
	if !strings.Contains(registration, "RateLimitMiddleware(CorsMiddleware(requireStarted(handler)))") {
		t.Fatalf("the limiter must be the outermost wrapper so the budget is spent before the handler runs; got:\n%s", registration)
	}
}

// AN UNACCEPTED MINT MUST BE REFUSED BEFORE THE JOURNAL IS TOUCHED.
//
// Redeem() journals its intent before it asks the mint for an invoice, and a pre-issuer
// failure frees the note for retry — each retry writing a fresh record that nothing
// reclaims. With the mint taken from the request body and never checked, a caller could
// loop one note against a mint this gate does not serve and walk the journal to its cap,
// after which Prepare refuses for EVERY customer.
//
// The refusal must be 400 mint_not_accepted, and it must happen before Redeem, so no
// record is created at all.
func TestRedeemRefusesAnUnacceptedMintBeforeJournaling(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t) // otherwise the identity guard short-circuits and this proves nothing
	melted := false
	rail, store := endpointTestRailWithStore(t, &melted)

	h := handleLNURLCashRedeem(rail, func() (bool, string) { return true, "" },
		func(domain string) (lnurlCashIssuer, error) {
			return &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 1000}}, nil
		})

	// A mint this gate definitely does not serve. Not "mint.example" by luck: assert it
	// is absent from the accepted list, so the test cannot silently become a happy-path
	// test if someone adds that host to the config.
	unaccepted := "https://definitely-not-an-accepted-mint.invalid"
	for _, m := range configManager.GetConfig().AcceptedMints {
		if tollwallet.MintURLMatches(m.URL, unaccepted) {
			t.Fatalf("%s is an accepted mint in this config; pick another for this test", unaccepted)
		}
	}

	body := `{"note":"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",` +
		`"mint":"` + unaccepted + `","issuer":"mint.example"}`
	rec := httptest.NewRecorder()
	// Keep httptest's default RemoteAddr (192.0.2.1) — resolveTestDevice seeds a lease for
	// exactly that address. Overriding it made the identity guard refuse first, and the
	// "no journal records" assertion then passed for the wrong reason.
	req := httptest.NewRequest(http.MethodPost, "/lnurlcash/redeem", strings.NewReader(body))
	h.ServeHTTP(rec, req)

	if melted {
		t.Fatal("the handler reached the melt for a mint this gate does not serve")
	}
	// The testenv config has no accepted mints, so this exercises the reject path. Assert
	// the journal was not written to: that is the whole point.
	ops, err := store.List()
	if err != nil {
		t.Fatalf("listing the journal: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("an unaccepted mint left %d journal record(s); each one is permanent and a retry loop consumes one per attempt", len(ops))
	}
	// Assert the SPECIFIC refusal. Without this the test passed with the mint check
	// removed, because the identity guard refused first and the journal stayed empty
	// either way — the negative control is the only reason that was caught.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unaccepted mint, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mint_not_accepted") {
		t.Fatalf("expected the mint_not_accepted refusal, got: %s", rec.Body.String())
	}
}

// A SUCCESS RESPONSE MUST ALWAYS NAME A QUOTE.
//
// A sibling implementation returned HTTP 200 with an empty state on paths that should
// have been errors — a customer told "you are paid, your access is pending" with nothing
// behind it. The rule here: status:1 is only ever written when a quote id exists, and the
// portal must never have to guess.
//
// This drives every refusal the rail can produce and asserts none of them is a 200, then
// asserts the one success carries a quote.
func TestRedeemNeverReportsSuccessWithoutAQuote(t *testing.T) {
	installMerchantKnowingConfig(t)
	resolveTestDevice(t)

	type tc struct {
		name  string
		issue func(macAddress, mintURL string, sats uint64) (string, string, error)
		melt  func(n lnurlcash.Note, cb merchant.IssuerCallback, invoice string) error
		want  int
	}
	cases := []tc{
		{
			name: "invoice failed before the issuer",
			issue: func(string, string, uint64) (string, string, error) {
				return "", "", errors.New("mint down")
			},
			melt: func(lnurlcash.Note, merchant.IssuerCallback, string) error { return nil },
			want: http.StatusBadGateway,
		},
		{
			name: "melt returned an error",
			issue: func(string, string, uint64) (string, string, error) {
				return "quote-x", "lnbc1", nil
			},
			melt: func(lnurlcash.Note, merchant.IssuerCallback, string) error {
				return errors.New("issuer unreachable — outcome unknown")
			},
			want: http.StatusBadGateway,
		},
		{
			name: "melt succeeded but returned no quote id",
			issue: func(string, string, uint64) (string, string, error) {
				// An invoice function that "succeeds" with an empty quote id must not
				// become a 200: this is the empty-state bug in its exact shape.
				return "", "lnbc1", nil
			},
			melt: func(lnurlcash.Note, merchant.IssuerCallback, string) error { return nil },
			// NOT 200. Before the fix this returned {"status":1} with no quote id; the
			// rail now treats an empty quote as the pre-issuer failure it is, which is
			// a 502 redeem_unconfirmed refusal. The test asserting 200 was asserting
			// the bug — which is why it passed before the fix went in and failed after.
			want: http.StatusBadGateway,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, err := paymentjournal.Open(filepath.Join(t.TempDir(), "journal"))
			if err != nil {
				t.Fatalf("journal: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			rail := merchant.NewLNURLCashRail(store, 0, c.issue, c.melt)
			h := handleLNURLCashRedeem(rail, func() (bool, string) { return true, "" },
				func(string) (lnurlCashIssuer, error) {
					return &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 21000}}, nil
				})

			body := `{"note":"` + endpointTestSpend + `","mint":"` + acceptedMintForTest(t) +
				`","issuer":"mint.example"}`
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/lnurlcash/redeem", strings.NewReader(body))
			h.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK {
				var got lnurlCashRedeemResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("a 200 carried no parsable body: %s", rec.Body.String())
				}
				if got.QuoteID == "" {
					t.Fatalf("HTTP 200 with no quote id — the customer is told they are paid and nothing exists behind it: %s", rec.Body.String())
				}
			} else if c.want == http.StatusOK {
				t.Fatalf("expected success for this case but got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// A JOURNAL REFUSAL MUST NOT BE REPORTED AS AN AMBIGUOUS MELT.
//
// The first independent review found this and it was marked fixed. It was not: the
// endpoint had cases for already-redeemed and retries-exhausted, and EVERYTHING else fell
// to the default branch, which answers 502 redeem_unconfirmed with retry_after 30 and
// "if payment went through, access will follow shortly".
//
// For a Prepare refusal that is false in every particular: nothing was sent to the issuer,
// no invoice exists, no payment can go through, and the portal retries forever against a
// journal that will never accept the write. An operator gets no signal that the gate cannot
// record payments at all.
func TestJournalRefusalIsNotReportedAsAnAmbiguousMelt(t *testing.T) {
	resolveTestDevice(t)
	store, err := paymentjournal.Open(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rail := merchant.NewLNURLCashRail(store, 0,
		func(string, string, uint64) (string, string, error) { return "q", "lnbc1", nil },
		func(lnurlcash.Note, merchant.IssuerCallback, string) error { return nil })

	// Fill the journal to its cap so Prepare refuses. This stands in for ANY Prepare
	// refusal — a corrupt file, a failed fsync, a full journal — because the store returns
	// the same ErrRefused for all of them and the endpoint cannot tell them apart either.
	for i := 0; i < 1100; i++ {
		_, _ = store.Prepare(paymentjournal.Operation{
			ID:       "fill-" + strings.Repeat("a", 1) + strings.Repeat("b", 0) + itoa(i),
			WalletID: "w", Direction: paymentjournal.Incoming,
			AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
		}, paymentjournal.Budget{})
	}

	h := handleLNURLCashRedeem(rail, func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) {
			return &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 21000}}, nil
		})
	body := `{"note":"` + endpointTestSpend + `","mint":"` + acceptedMintForTest(t) + `","issuer":"mint.example"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/lnurlcash/redeem", strings.NewReader(body))
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadGateway && strings.Contains(rec.Body.String(), "redeem_unconfirmed") {
		t.Fatalf("a journal refusal was reported as an ambiguous melt (502 redeem_unconfirmed): %s\n"+
			"nothing was sent to the issuer and no invoice exists, so telling the customer "+
			"\"if payment went through, access will follow\" is false, and retry_after sends them "+
			"round a loop that cannot succeed", rec.Body.String())
	}
}

// THE MINT CHECK MUST ASK THE SAME AUTHORITY THE ALLOTMENT CODE ASKS.
//
// The check originally read configManager.GetConfig() — the LIVE config, swapped by
// ReloadConfig on every `tollgate config` save. The allotment code reads m.config, a snapshot
// captured when the merchant was built and never reassigned. After an operator added a mint,
// this check said yes while allotment still said "mint configuration not found": Prepare had
// already journalled a record, the invoice step failed, the record was marked Failed, and the
// note retried its full budget. That is the exact journal-consumption loop the mint check was
// added to close, reopened by a config reload.
//
// The fix asks the merchant. This test pins the SOURCE of the answer, because a future reader
// "simplifying" it back to configManager would look correct and be wrong.
func TestMintAcceptanceAsksTheMerchantNotTheLiveConfig(t *testing.T) {
	resolveTestDevice(t)

	// Build a merchant whose accepted list EXCLUDES a mint the live config contains, then
	// install it. That divergence is the whole danger: after an operator adds a mint, the
	// live config says yes while the boot-time snapshot the allotment code reads still says
	// no. Without this divergence the two sources agree by accident and any implementation
	// passes.
	live := acceptedMintForTest(t)
	const onlyInLiveConfig = "https://added-after-boot.invalid"

	prev := merchantProvider.inner.GetMerchant()
	merchantProvider.inner.SetMerchant(&staleMintMerchant{})
	t.Cleanup(func() { merchantProvider.inner.SetMerchant(prev) })

	if mintAccepted(onlyInLiveConfig) {
		t.Fatal("the check accepted a mint the MERCHANT does not know — it is reading the live config, and after a reload that admits mints the allotment code still rejects, reopening the journal-consumption loop")
	}
	if mintAccepted(live) {
		t.Fatal("the check accepted a mint the merchant does not know")
	}

	// Now install a merchant that DOES know the config's mints — what a fully-started gate
	// has — and assert it accepts. Restoring `prev` here would be wrong: in the testenv build
	// `prev` is startingMerchant{}, whose accepted list is nil, so the assertion would fail
	// for a reason that has nothing to do with what this test is about.
	merchantProvider.inner.SetMerchant(&staleMintMerchant{accepted: configManager.GetConfig().AcceptedMints})
	if !mintAccepted(live) {
		t.Fatal("a merchant that reports this mint as accepted was still refused — the check is not consulting the merchant at all")
	}

	// A mint nobody accepts is refused, and refused BEFORE any journal write.
	melted := false
	rail, store := endpointTestRailWithStore(t, &melted)
	_ = store
	h := handleLNURLCashRedeem(rail, func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) {
			return &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 21000}}, nil
		})
	body := `{"note":"` + endpointTestSpend + `","mint":"https://nobody-accepts-this.invalid","issuer":"mint.example"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/lnurlcash/redeem", strings.NewReader(body)))

	if melted {
		t.Fatal("an unaccepted mint reached the melt")
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "mint_not_accepted") {
		t.Fatalf("expected 400 mint_not_accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// staleMintMerchant stands in for a merchant built before a config reload: it reports the
// accepted list it was constructed with and nothing else.
type staleMintMerchant struct {
	merchant.MerchantInterface
	accepted []config_manager.MintConfig
}

func (m *staleMintMerchant) GetAcceptedMints() []config_manager.MintConfig { return m.accepted }
