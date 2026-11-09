package paymentjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func intent(id string) Operation {
	h := sha256.Sum256([]byte("public invoice fixture " + id))
	return Operation{ID: id, WalletID: "wallet-a", Direction: Outgoing, AmountMSat: 1000, FeeReserveMSat: 100, PaymentHash: hex.EncodeToString(h[:]), Recipient: "recipient-a", Request: []byte(`{"prepared":"encrypted-request"}`)}
}
func TestPrepareDurableIdempotentAndSingleWriter(t *testing.T) {
	s, dir := openTest(t)
	op, e := s.Prepare(intent("payout-1"), Budget{LimitMSat: 1100})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Prepare(intent("payout-1"), Budget{LimitMSat: 1100})
	if e != nil || again.Version != op.Version {
		t.Fatal("intent was not idempotent")
	}
	if _, e = Open(dir); e == nil {
		t.Fatal("two writers acquired store")
	}
	s.Close()
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	got, e := r.Get("payout-1")
	if e != nil || got.State != Prepared || got.AmountMSat != 1000 {
		t.Fatal("intent not durable")
	}
	changed := intent("payout-1")
	changed.WalletID = "wallet-b"
	if _, e = r.Prepare(changed, Budget{LimitMSat: 10000}); e == nil {
		t.Fatal("ID rebound to another wallet")
	}
}
func TestUnknownHoldsBudgetAndCannotResubmit(t *testing.T) {
	s, dir := openTest(t)
	op, e := s.Prepare(intent("one"), Budget{LimitMSat: 1100})
	if e != nil {
		t.Fatal(e)
	}
	op, e = s.Advance(op.ID, op.Version, Submitted, 0)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	op, e = s.Get("one")
	if e != nil || op.State != Unknown {
		t.Fatal("unreconciled submitted operation not recovered UNKNOWN")
	}
	if _, e = s.Advance(op.ID, op.Version, Submitted, 0); e == nil {
		t.Fatal("unknown operation can be resent")
	}
	if _, e = s.Prepare(intent("two"), Budget{LimitMSat: 1100}); e == nil {
		t.Fatal("unknown reservation released")
	}
	op, e = s.Advance(op.ID, op.Version, Paid, 50)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Advance(op.ID, op.Version, Failed, 0); e == nil {
		t.Fatal("paid outcome can be erased")
	}
}
func TestConcurrentBudgetReservationAndCAS(t *testing.T) {
	s, _ := openTest(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two", "three", "four"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, e := s.Prepare(intent(id), Budget{LimitMSat: 1100}); e == nil {
				successes.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("budget race admitted %d", successes.Load())
	}
	ops, _ := s.List()
	op := ops[0]
	var applied atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Advance(op.ID, op.Version, Submitted, 0); e == nil {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	if applied.Load() != 1 {
		t.Fatal("CAS allowed duplicate submission")
	}
}
func TestPartialPayoutSuccessAndFailedReservation(t *testing.T) {
	s, _ := openTest(t)
	a, e := s.Prepare(intent("recipient-a"), Budget{LimitMSat: 2200})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Prepare(intent("recipient-b"), Budget{LimitMSat: 2200})
	if e != nil {
		t.Fatal(e)
	}
	a, _ = s.Advance(a.ID, a.Version, Submitted, 0)
	a, e = s.Advance(a.ID, a.Version, Paid, 50)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = s.Advance(b.ID, b.Version, Submitted, 0)
	if _, e = s.Advance(b.ID, b.Version, Failed, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Prepare(intent("recipient-c"), Budget{LimitMSat: 2200}); e != nil {
		t.Fatal("definitively failed reservation retained")
	}
	a, _ = s.Get(a.ID)
	if a.State != Paid || a.ActualFeeMSat != 50 {
		t.Fatal("partial successful payout discarded")
	}
}
func TestIncomingBindingAndGrantCannotReplay(t *testing.T) {
	s, _ := openTest(t)
	in := Operation{ID: "purchase", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 21000, Recipient: "aa:bb:cc:dd:ee:ff"}
	op, e := s.Prepare(in, Budget{})
	if e != nil {
		t.Fatal(e)
	}
	op, _ = s.Advance(op.ID, op.Version, Submitted, 0)
	op, e = s.BindInvoice(op.ID, op.Version, strings.Repeat("b", 64), []byte("verified invoice"))
	if e != nil || op.State != Awaiting {
		t.Fatal("incoming invoice not bound")
	}
	if _, e = s.BindInvoice(op.ID, op.Version, strings.Repeat("c", 64), nil); e == nil {
		t.Fatal("invoice replaced")
	}
	op, e = s.Advance(op.ID, op.Version, Paid, 0)
	if e != nil {
		t.Fatal(e)
	}
	op, e = s.Advance(op.ID, op.Version, GrantPending, 0)
	if e != nil {
		t.Fatal(e)
	}
	op, e = s.Advance(op.ID, op.Version, Completed, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Advance(op.ID, op.Version, GrantPending, 0); e == nil {
		t.Fatal("completed grant replayed")
	}
}
func TestForeignAndCorruptFilesFailClosed(t *testing.T) {
	s, dir := openTest(t)
	_, e := s.Prepare(intent("one"), Budget{LimitMSat: 1100})
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "operations.json"), []byte("{corrupt"), 0600)
	if _, e = s.Prepare(intent("two"), Budget{LimitMSat: 2200}); e == nil {
		t.Fatal("outside change overwritten")
	}
	s.Close()
	if _, e = Open(dir); e == nil {
		t.Fatal("corrupt journal accepted")
	}
	linkdir := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(dir, linkdir); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(linkdir); e == nil {
		t.Fatal("symlink journal accepted")
	}
}
func TestActualProcessExitAfterSubmitted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(os.Args[0], "-test.run=^TestJournalCrashHelper$")
	cmd.Env = append(os.Environ(), "TG_JOURNAL_CRASH_DIR="+dir)
	if e := cmd.Run(); e == nil {
		t.Fatal("helper did not crash")
	}
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	op, e := s.Get("crash")
	if e != nil || op.State != Unknown {
		t.Fatal("crashed submission not durable/unknown")
	}
}
func TestJournalCrashHelper(t *testing.T) {
	dir := os.Getenv("TG_JOURNAL_CRASH_DIR")
	if dir == "" {
		return
	}
	s, e := Open(dir)
	if e != nil {
		os.Exit(20)
	}
	op, e := s.Prepare(intent("crash"), Budget{LimitMSat: 1100})
	if e != nil {
		os.Exit(21)
	}
	if _, e = s.Advance(op.ID, op.Version, Submitted, 0); e != nil {
		os.Exit(22)
	}
	os.Exit(37)
}
func TestPeriodBoundaryKeepsUncertainReservations(t *testing.T) {
	s, _ := openTest(t)
	a, _ := s.Prepare(intent("a"), Budget{LimitMSat: 1100})
	a, _ = s.Advance(a.ID, a.Version, Submitted, 0)
	a, _ = s.Advance(a.ID, a.Version, Unknown, 0)
	if _, e := s.Prepare(intent("b"), Budget{LimitMSat: 1100, Since: time.Now().Add(time.Second)}); e == nil {
		t.Fatal("new period dropped uncertain payment reservation")
	}
}
func TestBoundIncomingUnknownCanReconcilePaid(t *testing.T) {
	s, _ := openTest(t)
	op, _ := s.Prepare(Operation{ID: "purchase-unknown", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a"}, Budget{})
	op, _ = s.Advance(op.ID, op.Version, Submitted, 0)
	op, _ = s.BindInvoice(op.ID, op.Version, strings.Repeat("c", 64), []byte("verified invoice"))
	op, _ = s.Advance(op.ID, op.Version, Unknown, 0)
	if _, err := s.Advance(op.ID, op.Version, Paid, 0); err != nil {
		t.Fatal("bound incoming payment cannot reconcile from unknown")
	}
}
func TestDuplicateSnapshotKeysNeverDropEarlierRecords(t *testing.T) {
	s, dir := openTest(t)
	s.Close()
	if err := os.WriteFile(filepath.Join(dir, "operations.json"), []byte(`{"format":1,"operations":{},"operations":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil {
		reopened.Close()
		t.Fatal("duplicate fields accepted")
	}
}
func TestInvalidRecoveredIncomingStateRefused(t *testing.T) {
	s, dir := openTest(t)
	op, _ := s.Prepare(Operation{ID: "purchase-invalid", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a"}, Budget{})
	s.Close()
	b, err := os.ReadFile(filepath.Join(dir, "operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), `"state":"prepared"`, `"state":"completed"`, 1))
	if err = os.WriteFile(filepath.Join(dir, "operations.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil {
		reopened.Close()
		t.Fatalf("unbound completed purchase accepted %s", op.ID)
	}
}
func TestActualProcessExitAroundAtomicRename(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "rename")
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Prepare(intent("rename"), Budget{LimitMSat: 1100}); err != nil {
				t.Fatal(err)
			}
			s.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRenameCrashHelper$")
			cmd.Env = append(os.Environ(), "TG_JOURNAL_RENAME_DIR="+dir, "TG_JOURNAL_RENAME_PHASE="+phase)
			if err = cmd.Run(); err == nil {
				t.Fatal("helper did not exit")
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			op, _ := s.Get("rename")
			want := Prepared
			if phase == "after" {
				want = Unknown
			}
			if op.State != want {
				t.Fatalf("atomic rename recovered %s, want %s", op.State, want)
			}
		})
	}
}
func TestRenameCrashHelper(t *testing.T) {
	dir := os.Getenv("TG_JOURNAL_RENAME_DIR")
	if dir == "" {
		return
	}
	s, err := Open(dir)
	if err != nil {
		os.Exit(20)
	}
	op, err := s.Get("rename")
	if err != nil {
		os.Exit(21)
	}
	s.rename = func(from, to string) error {
		if os.Getenv("TG_JOURNAL_RENAME_PHASE") == "before" {
			os.Exit(37)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		os.Exit(38)
		return nil
	}
	s.Advance(op.ID, op.Version, Submitted, 0)
	os.Exit(22)
}
func TestOriginalIncomingIntentRemainsIdempotentAfterBinding(t *testing.T) {
	s, _ := openTest(t)
	original := Operation{ID: "repeated-checkout", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a", Request: []byte("original terms")}
	op, _ := s.Prepare(original, Budget{})
	op, _ = s.Advance(op.ID, op.Version, Submitted, 0)
	op, _ = s.BindInvoice(op.ID, op.Version, strings.Repeat("b", 64), []byte("verified invoice"))
	again, err := s.Prepare(original, Budget{})
	if err != nil || again.Version != op.Version {
		t.Fatal("same checkout intent lost idempotency after binding")
	}
}
func TestOneInvoiceCannotBelongToTwoOperations(t *testing.T) {
	s, _ := openTest(t)
	first := intent("original-id")
	if _, err := s.Prepare(first, Budget{LimitMSat: 10000}); err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = "replacement-id"
	if _, err := s.Prepare(duplicate, Budget{LimitMSat: 10000}); err == nil {
		t.Fatal("invoice duplicated under another operation ID")
	}
}
func TestIncomingInvoiceCannotGrantTwoPurchases(t *testing.T) {
	s, _ := openTest(t)
	for i, id := range []string{"buyer-a", "buyer-b"} {
		op, err := s.Prepare(Operation{ID: id, WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: id}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		op, _ = s.Advance(op.ID, op.Version, Submitted, 0)
		_, err = s.BindInvoice(op.ID, op.Version, strings.Repeat("b", 64), []byte("verified invoice"))
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 && err == nil {
			t.Fatal("same paid invoice bound to two purchases")
		}
	}
}
