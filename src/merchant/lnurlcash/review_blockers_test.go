package lnurlcash

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// errTestQuoteFailed stands in for a settlement mint that will not quote right now —
// the failure that legitimately leaves a record `pending`, so the next attempt re-enters
// the pending-resume path.
var errTestQuoteFailed = errors.New("mint refused to quote for now")

// These tests exist because a round-4 review measured two money-path defects that the
// suite could not see. Both were about a state the code reached but never re-checked:
// a settled payment whose grant had not landed, and an invoice amount that forgot the
// highest value the gate itself had observed. Neither is a unit-testing nicety — each
// one loses a customer's sats while telling them it did not.

// A settlement whose grant did NOT land must not be recorded as `redeemed`.
//
// `redeemed` is terminal and resume() answers Granted:true from it without asking
// anyone. Measured pre-fix: the first response says "could not open the gate yet", the
// journal says `redeemed`, and the next presentation of the same note says "granted"
// with zero further mint calls — the customer paid, has no access, and is told they do.
//
// Reachable without fault injection: ensureLightningAccessGranted returns nil WITHOUT
// granting when another goroutine holds record.Processing, and the 5 s quote monitor
// calls exactly that path, so the redeemer loses this race to its own monitor.
func TestAPaidButUngrantedSettlementIsNeverReportedAsGranted(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000})

	// The mint has the money; the grant did not land (another goroutine held it).
	h.gate.settle = func(string) (bool, bool, error) { return true, false, nil }

	first, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if first.Granted {
		t.Fatalf("reported a grant that did not land: %+v", first)
	}
	if !strings.Contains(strings.ToLower(first.Message), "again") {
		t.Fatalf("the customer is not told to retry: %q", first.Message)
	}

	// The journal must still be reconcilable. `redeemed` here would strand the payment.
	rec, ok, err := h.svc.store.Get(h.svc.FingerprintOfPresented(h.issuer.note()))
	if err != nil || !ok {
		t.Fatalf("journal lookup: ok=%v err=%v", ok, err)
	}
	if rec.State != IntentSubmitted {
		t.Fatalf("journal state after a paid-but-ungranted settle = %q, want %q: `redeemed` is "+
			"terminal and resume() answers granted from it, so the grant could never be retried",
			rec.State, IntentSubmitted)
	}

	// Now the grant succeeds. Re-presenting MUST reach the mint again and MUST grant.
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }
	settlesBefore := h.gate.settleCount()

	second, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if h.gate.settleCount() == settlesBefore {
		t.Fatal("the retry never asked the mint: a paid-but-ungranted record must be reconciled, " +
			"not answered from disk")
	}
	if !second.Granted {
		t.Fatalf("the retry did not grant after the mint confirmed and the grant landed: %+v", second)
	}
	// And the record is now legitimately terminal.
	if rec, _, _ := h.svc.store.Get(h.svc.FingerprintOfPresented(h.issuer.note())); rec.State != IntentRedeemed {
		t.Fatalf("state after a successful grant = %q, want %q", rec.State, IntentRedeemed)
	}

	// A THIRD presentation is answered from the journal, and correctly says granted.
	third, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("third Redeem: %v", err)
	}
	if !third.Granted {
		t.Fatalf("a resolved record must still answer granted to its owner: %+v", third)
	}
}

// The invoice amount must be the note's HIGH-WATER mark across every attempt, not the
// first sample against the current one.
//
// `AmountSats` is written once by Begin, so max(AmountSats, live) discards every
// intermediate observation. Measured pre-fix with a flaky settlement mint (invoice
// creation fails, so the record legitimately stays `pending` and the path is reached
// again): the issuer claimed 10,000 → 50,000 → 20,000, the gate invoiced
// [10000 50000 20000] — attempt 3 invoiced 20,000 after the gate itself had seen
// 50,000, stranding 30,000. k1 is single-use, so the residual is unrecoverable.
func TestInvoiceAmountRatchetKeepsTheHighestClaimEverSeen(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000})

	// Invoice creation fails, so nothing is promised and the record stays pending —
	// the only state that re-enters this path.
	h.gate.quoteErr = errTestQuoteFailed

	step := func(msat uint64) {
		h.issuer.mu.Lock()
		h.issuer.maxMsat = msat
		h.issuer.mu.Unlock()
		if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err == nil {
			t.Fatalf("expected the attempt to fail at the invoice step")
		}
	}

	step(10_000_000) // 10,000 sats
	step(50_000_000) // 50,000 sats — observed, must be remembered
	step(20_000_000) // 20,000 sats — a REVISED-DOWN claim

	rec, ok, err := h.svc.store.Get(h.svc.FingerprintOfPresented(h.issuer.note()))
	if err != nil || !ok {
		t.Fatalf("journal lookup: ok=%v err=%v", ok, err)
	}
	if rec.MaxObservedSats != 50_000 {
		t.Fatalf("MaxObservedSats = %d, want 50000: the ratchet must keep the highest claim "+
			"ever seen, not the first or the latest", rec.MaxObservedSats)
	}

	// Now let it settle, and assert the invoice is for the high-water mark.
	h.gate.quoteErr = nil
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("settling attempt: %v", err)
	}
	invs := h.gate.invoiceList()
	if len(invs) == 0 {
		t.Fatal("no invoice was requested")
	}
	if last := invs[len(invs)-1]; last != "lnbc50000quote1" {
		t.Fatalf("invoiced %q, want the note's high-water mark lnbc50000quote1: invoicing below a "+
			"value we already observed strands the difference, and k1 is single-use", last)
	}
}

// The ratchet must survive a restart: the observation is persisted before the invoice
// is requested, so a crash mid-attempt cannot lose the knowledge that the note was
// worth more.
func TestAmountRatchetSurvivesReopeningTheJournal(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000})
	h.gate.quoteErr = errTestQuoteFailed

	h.issuer.mu.Lock()
	h.issuer.maxMsat = 50_000_000
	h.issuer.mu.Unlock()
	fp := h.svc.FingerprintOfPresented(h.issuer.note())
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("expected the attempt to fail at the invoice step")
	}

	path := h.svc.store.path
	if err := h.svc.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	rec, ok, err := reopened.Get(fp)
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if rec.MaxObservedSats != 50_000 {
		t.Fatalf("MaxObservedSats after reopen = %d, want 50000: the ratchet must be durable "+
			"before the invoice is requested", rec.MaxObservedSats)
	}
}

// ObserveAmount must never lower the mark, however often it is called or in what order.
func TestObserveAmountIsMonotonic(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000})
	fp := strings.Repeat("ab", 32)

	if _, err := h.svc.store.Begin(fp, Intent{
		MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1_000, IssuerHost: "issuer.example",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	for _, step := range []struct {
		in   uint64
		want uint64
	}{
		{5_000, 5_000},
		{3_000, 5_000}, // a revised-down claim must not lower it
		{9_000, 9_000},
		{9_000, 9_000}, // idempotent
		{1, 9_000},
	} {
		rec, err := h.svc.store.ObserveAmount(fp, step.in)
		if err != nil {
			t.Fatalf("ObserveAmount(%d): %v", step.in, err)
		}
		if rec.MaxObservedSats != step.want {
			t.Fatalf("ObserveAmount(%d) -> %d, want %d", step.in, rec.MaxObservedSats, step.want)
		}
	}
}

// A ceiling of 0 must refuse everything, not disable the check. `capSats > 0 &&` turned
// a hand-built Config{Enabled:true} into an unbounded money path: measured pre-fix, a
// 500-billion-sat claim was invoiced and granted. Unreachable from config.json today
// (the loader substitutes 1000, the schema sets Min: 1), which is why it is a latent
// hole rather than a live one — and exactly the kind a migration or CLI override opens.
func TestAZeroCeilingRefusesRatherThanAcceptingAnything(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 1})
	h.svc.cfg.MaxNoteSats = 0

	// The harness note is worth 10,000 sats.
	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatalf("a zero ceiling accepted a note (result %+v): an unset ceiling must refuse, "+
			"never mean 'no limit'", res)
	}
	if res.Granted {
		t.Fatal("a zero ceiling granted access")
	}
	if invs := h.gate.invoiceList(); len(invs) != 0 {
		t.Fatalf("a zero ceiling asked the mint for an invoice: %v", invs)
	}
	if got := h.issuer.callbackCount(); got != 0 {
		t.Fatalf("a zero ceiling reached the issuer (%d callbacks)", got)
	}
}
