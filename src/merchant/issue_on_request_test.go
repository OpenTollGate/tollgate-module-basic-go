package merchant

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// issueTestToken is the token the fake wallet hands back from Send. Only its
// serialized form matters to the caller.
type issueTestToken struct{ tollwallet.Token }

func (issueTestToken) Mint() string               { return "http://mint.example:3398" }
func (issueTestToken) Amount() uint64             { return 10 }
func (issueTestToken) Unit() string               { return "mb" }
func (issueTestToken) Serialize() (string, error) { return issueTestSerialized, nil }
func (issueTestToken) Close()                     {}

const issueTestSerialized = "cashuB" + "issued-token-issued-token-issued-token"

// issueFakeWallet records the order of the operations it is asked to perform,
// because the order IS the safety property: quote, then wait for payment, then
// mint, then send. It can be told to fail at a chosen step.
type issueFakeWallet struct {
	tollwallet.WalletPort

	calls     []string
	state     tollwallet.MintQuoteState
	mintErr   error
	sendErr   error
	mintedFor string
	// mintCount counts how many times MintTokens was called, so a retry that
	// re-mints an already-issued quote is visible.
	mintCount int
}

func (w *issueFakeWallet) RequestMintQuote(amount uint64, mintURL string) (*tollwallet.MintQuote, error) {
	w.calls = append(w.calls, fmt.Sprintf("quote:%d:%s", amount, mintURL))
	return &tollwallet.MintQuote{
		QuoteID: "quote-1",
		Request: "pay-this-please",
		State:   tollwallet.StateUnpaid,
		Amount:  amount,
	}, nil
}

func (w *issueFakeWallet) GetMintQuoteState(quoteID string) (tollwallet.MintQuoteState, error) {
	w.calls = append(w.calls, "state:"+quoteID)
	return w.state, nil
}

func (w *issueFakeWallet) MintTokens(quoteID string) (uint64, error) {
	w.calls = append(w.calls, "mint:"+quoteID)
	w.mintCount++
	w.mintedFor = quoteID
	if w.mintErr != nil {
		return 0, w.mintErr
	}
	return 10, nil
}

func (w *issueFakeWallet) Send(amount uint64, mintURL string, includeFees bool) (tollwallet.Token, error) {
	w.calls = append(w.calls, fmt.Sprintf("send:%d:%s", amount, mintURL))
	if w.sendErr != nil {
		return nil, w.sendErr
	}
	return issueTestToken{}, nil
}

func newIssueMerchant(w *issueFakeWallet) *Merchant {
	// Short bounds: the timeout path must be exercised, not skipped for being
	// slow. The production defaults are 2s/2m.
	return &Merchant{
		tollwallet:        w,
		issuePollInterval: time.Millisecond,
		issueTimeout:      50 * time.Millisecond,
	}
}

// TestIssueOnRequestHappyPath pins the operation order and the returned token.
// The customer asks for megabytes; the operator issues them.
func TestIssueOnRequestHappyPath(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StatePaid}
	m := newIssueMerchant(w)

	token, err := m.IssueOnRequest("http://mint.example:3398", "mb", 10)
	if err != nil {
		t.Fatalf("IssueOnRequest: %v", err)
	}
	if token != issueTestSerialized {
		t.Errorf("token = %q, want the serialized token", token)
	}

	want := []string{
		"quote:10:http://mint.example:3398",
		"state:quote-1",
		"mint:quote-1",
		"send:10:http://mint.example:3398",
	}
	if strings.Join(w.calls, "|") != strings.Join(want, "|") {
		t.Errorf("operation order matters:\n got %v\nwant %v", w.calls, want)
	}
}

// TestIssueOnRequestRefusesUnpaidQuote is the money-safety case: an unpaid
// quote must never reach MintTokens. The mint would reject it, but issuing
// must not depend on the mint being the only guard.
func TestIssueOnRequestRefusesUnpaidQuote(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StateUnpaid}
	m := newIssueMerchant(w)

	_, err := m.IssueOnRequest("http://mint.example:3398", "mb", 10)
	if err == nil {
		t.Fatal("want an error for an unpaid quote")
	}
	for _, c := range w.calls {
		if strings.HasPrefix(c, "mint:") {
			t.Fatalf("MintTokens must not be called for an unpaid quote; calls=%v", w.calls)
		}
	}
	if !strings.Contains(err.Error(), "UNPAID") {
		t.Errorf("error should name the quote state so the operator knows to wait; got %q", err.Error())
	}
}

// TestIssueOnRequestRejectsUnknownUnit covers the mispricing guard: the unit
// has to be one the configured mint actually issues. An empty unit means "sat"
// and is only valid against a sat mint, which is not this case.
func TestIssueOnRequestRejectsUnknownUnit(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StatePaid}
	m := newIssueMerchant(w)

	if _, err := m.IssueOnRequest("http://mint.example:3398", "parks", 10); err == nil {
		t.Fatal("want an error for a unit the mint does not issue")
	}
	if len(w.calls) != 0 {
		t.Errorf("a rejected request must not touch the wallet; calls=%v", w.calls)
	}
}

// TestIssueOnRequestZeroAmount is a guard against a fat-fingered operator
// request: a zero-value token is not a product.
func TestIssueOnRequestZeroAmount(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StatePaid}
	m := newIssueMerchant(w)

	if _, err := m.IssueOnRequest("http://mint.example:3398", "mb", 0); err == nil {
		t.Fatal("want an error for a zero amount")
	}
	if len(w.calls) != 0 {
		t.Errorf("a rejected request must not touch the wallet; calls=%v", w.calls)
	}
}

// TestIssueOnRequestSendFailureIsReportedAsFundsHeld is the case the operator
// must be told about plainly. If MintTokens succeeded and Send failed, the
// ecash exists IN THE OPERATOR'S WALLET and no token was handed over. Reporting
// that as a plain failure invites the operator to retry blindly, and a retry
// that re-mints the same quote is the duplicate-outputs brick (#257/#266).
func TestIssueOnRequestSendFailureIsReportedAsFundsHeld(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StatePaid, sendErr: fmt.Errorf("connection reset")}
	m := newIssueMerchant(w)

	_, err := m.IssueOnRequest("http://mint.example:3398", "mb", 10)
	if err == nil {
		t.Fatal("want an error when Send fails")
	}
	if w.mintCount != 1 {
		t.Fatalf("MintTokens should have been called exactly once; got %d", w.mintCount)
	}
	msg := err.Error()
	for _, want := range []string{"minted", "wallet"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Errorf("error must state the funds are already minted and held (%q missing); got %q", want, msg)
		}
	}
}

// TestIssueOnRequestMintFailureDoesNotSend: if minting fails there is nothing
// to send, and calling Send anyway would attempt to spend an empty wallet.
func TestIssueOnRequestMintFailureDoesNotSend(t *testing.T) {
	w := &issueFakeWallet{state: tollwallet.StatePaid, mintErr: fmt.Errorf("mint 429")}
	m := newIssueMerchant(w)

	if _, err := m.IssueOnRequest("http://mint.example:3398", "mb", 10); err == nil {
		t.Fatal("want an error when MintTokens fails")
	}
	for _, c := range w.calls {
		if strings.HasPrefix(c, "send:") {
			t.Fatalf("Send must not run when minting failed; calls=%v", w.calls)
		}
	}
}
