package merchant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// A PAID purchase must never end as a silent no-op (bench MT3000, pre17,
// 2026-09-26).
//
// The measured failure: `state=PAID`, the merchant wallet +1 sat, and
// `access_granted` never became true — the money moved and the customer stayed
// out. The value is in the operator's wallet, so the ONE thing the module must
// not do is answer as if nothing happened: the caller gets an error it can act
// on and the operator gets a line that names the client.
//
// Addresses here belong to this file only.

const (
	// paidUngrantedMAC carries a purchase whose gate cannot be opened.
	paidUngrantedMAC = "aa:bb:cc:dd:ee:72"
)

// TestAPaidPurchaseThatCannotBeGrantedIsLoudAndSpecific pins the contract for
// the customer-visible half of the defect. The invoice is settled and the tokens
// are issued (StateIssued, so no mint call is needed), and the enforcement layer
// cannot open the gate. What the caller must get back is a specific error — not
// a status with `access_granted:false` and no explanation — and the module log
// must carry an ERROR line that names the client, because the log is the
// operator's only surface.
//
// Before this change the failure came back as the underlying
// `failed to open gate: …` error, which names neither the client nor the fact
// that the purchase was paid, and the status poll answered 500 with the generic
// "failed to fetch invoice status".
func TestAPaidPurchaseThatCannotBeGrantedIsLoudAndSpecific(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "milliseconds")
	// NoDogSplash does not hold this client as Authenticated: the gate really is
	// shut and the authorisation is what has to open it.
	ndsctl.setRegistered(t, false)

	const quoteID = "paid-quote-cannot-be-granted"

	// The invoice settled and the tokens were issued: everything up to the gate
	// succeeded, so nothing but the gate can fail here.
	m.lightningQuoteMu.Lock()
	if m.lightningQuotes == nil {
		m.lightningQuotes = make(map[string]*lightningQuoteRecord)
	}
	m.lightningQuotes[quoteID] = &lightningQuoteRecord{
		MacAddress:     paidUngrantedMAC,
		MintURL:        renewalMintURL,
		Amount:         renewalSats,
		CreatedAt:      time.Now(),
		CachedState:    tollwallet.StateIssued,
		CachedStateAt:  time.Now(),
		HasCachedState: true,
	}
	m.lightningQuoteMu.Unlock()

	// The enforcement layer cannot open the gate (the wedged/failing ndsctl
	// state the bench measured).
	ndsctl.failAuth(t, true)

	logs := captureSyncLogs(t)

	status, err := m.GetLightningInvoiceStatus(quoteID, paidUngrantedMAC)
	if err == nil {
		t.Fatalf("GetLightningInvoiceStatus reported SUCCESS (access_granted=%v) for a paid purchase whose gate could not be opened: the customer paid and the module answered as if nothing were wrong", status != nil && status.AccessGranted)
	}
	if !errors.Is(err, ErrAccessGrantNotApplied) {
		t.Fatalf("error = %v, want it to wrap ErrAccessGrantNotApplied so a caller can tell 'paid but not granted' from 'the invoice is unpaid' and from a lookup failure", err)
	}
	if !strings.Contains(err.Error(), paidUngrantedMAC) {
		t.Fatalf("error = %q, want it to name the client whose purchase is stuck", err.Error())
	}

	// The operator's surface: an ERROR line that names the client, so the
	// purchase cannot be missed in the log the way `state=PAID,
	// access_granted:false` hides it on the wire. (The bench harness asserts
	// exactly this: a purchase that was not granted must leave a loud, specific
	// error naming the client.)
	window := logs.String()
	if !strings.Contains(window, "ERROR") {
		t.Fatalf("no ERROR line was logged for a paid purchase that could not be granted; log = %q", window)
	}
	if !strings.Contains(window, paidUngrantedMAC) {
		t.Fatalf("the ERROR line for the stuck paid purchase does not name the client; log = %q", window)
	}
	if !strings.Contains(window, "NOT complete") {
		t.Fatalf("the log does not say the purchase is not complete; log = %q", window)
	}

	// And the module must not have recorded a success anywhere: the quote is
	// neither granted nor stuck in a state a later poll cannot get past.
	record, err := m.getLightningQuoteRecord(quoteID)
	if err != nil {
		t.Fatalf("getLightningQuoteRecord: %v", err)
	}
	if record.SessionGranted {
		t.Fatal("the quote was marked SessionGranted although the gate was never opened: the status poll would report a paid customer with access they do not have")
	}
	if record.Processing {
		t.Fatal("the quote was left in Processing after the grant failed: every later poll returns early without an error, which is the silent no-op this assertion exists to prevent")
	}
}
