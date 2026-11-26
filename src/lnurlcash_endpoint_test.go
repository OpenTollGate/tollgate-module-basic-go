package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"mint.example"}`)

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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"mint.example"}`)

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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"mint.example"}`)

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
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 0}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"mint.example"}`)

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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"mint.example"}`)

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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example"}`)

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

	rec := postRedeem(t, h, `{"note":"`+endpointTestSpend+`","mint":"https://mint.example","issuer":"evil.example"}`)

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

	big := `{"note":"` + strings.Repeat("a", maxLNURLCashBodyBytes+100) + `","mint":"https://mint.example"}`
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
	resolveTestDevice(t)
	var melted bool
	rail := endpointTestRail(t, &melted)
	issuer := &stubIssuer{state: lnurlcash.IssuerState{WithdrawableMSat: 1000}}

	h := handleLNURLCashRedeem(rail,
		func() (bool, string) { return true, "" },
		func(string) (lnurlCashIssuer, error) { return issuer, nil })

	body := `{"note":"` + endpointTestSpend + `","mint":"https://mint.example","issuer":"mint.example"}`

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
