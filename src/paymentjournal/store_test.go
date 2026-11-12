package paymentjournal

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	op, e = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100})
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
			if _, e := s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100}); e == nil {
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
	a, _ = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 2200})
	a, e = s.Advance(a.ID, a.Version, Paid, 50)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = s.SubmitOutgoing(b.ID, b.Version, Budget{LimitMSat: 2200})
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
func helperExitCode(t *testing.T, err error) int {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper did not produce an exit status: %v", err)
	}
	return exitErr.ExitCode()
}
func TestActualProcessExitAfterSubmitted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(os.Args[0], "-test.run=^TestJournalCrashHelper$")
	cmd.Env = append(os.Environ(), "TG_JOURNAL_CRASH_DIR="+dir)
	err := cmd.Run()
	if err == nil {
		t.Fatal("helper did not crash")
	}
	if code := helperExitCode(t, err); code != 37 {
		t.Fatalf("helper exit code %d, want 37", code)
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
	if _, e = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100}); e != nil {
		os.Exit(22)
	}
	os.Exit(37)
}
func TestPeriodBoundaryKeepsUncertainReservations(t *testing.T) {
	s, _ := openTest(t)
	a, _ := s.Prepare(intent("a"), Budget{LimitMSat: 1100})
	a, _ = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 1100})
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
			err = cmd.Run()
			if err == nil {
				t.Fatal("helper did not exit")
			}
			wantCode := 37
			if phase == "after" {
				wantCode = 38
			}
			if code := helperExitCode(t, err); code != wantCode {
				t.Fatalf("helper exit code %d for phase %s, want %d", code, phase, wantCode)
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
	s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100})
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

// Replacing writer.lock under a live writer must poison that writer instead
// of letting it mutate without the single-writer guarantee. The replacement
// file is an ordinary unheld lock file, so the next writer opens and locks it
// without any unlink recovery.
func TestReplacedWriterLockPoisonsWriter(t *testing.T) {
	s, dir := openTest(t)
	if _, err := s.Prepare(intent("before-replace"), Budget{LimitMSat: 1100}); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, filepath.Join(dir, "writer.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(intent("after-replace"), Budget{LimitMSat: 2200}); err == nil {
		t.Fatal("mutation accepted after writer.lock was replaced")
	}
	if _, err := s.Get("before-replace"); err == nil {
		t.Fatal("poisoned writer still served reads")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(dir)
	if err != nil {
		t.Fatal("journal not reopenable against the replaced lock file", err)
	}
	defer next.Close()
	if _, err = next.Prepare(intent("after-reopen"), Budget{LimitMSat: 2200}); err != nil {
		t.Fatal("reopened journal refused a new intent", err)
	}
}

func TestUnknownSnapshotFieldRefusedAtReopen(t *testing.T) {
	s, dir := openTest(t)
	if _, err := s.Prepare(intent("one"), Budget{LimitMSat: 1100}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operations.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"format":1`) {
		t.Fatal("fixture missing expected format field")
	}
	b = []byte(strings.Replace(string(b), `"format":1`, `"format":1,"unexpected_field":1`, 1))
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil {
		reopened.Close()
		t.Fatal("unknown snapshot field accepted at reopen")
	}
}

func TestOversizeSnapshotRefusedAtReopen(t *testing.T) {
	s, dir := openTest(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operations.json")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Padding an otherwise valid snapshot isolates the size guard: invalid
	// JSON would still refuse even if the byte-limit check were removed.
	oversized := append(valid, bytes.Repeat([]byte(" "), maxBytes+1-len(valid))...)
	if !json.Valid(oversized) || len(oversized) != maxBytes+1 {
		t.Fatal("oversize fixture must be valid JSON just above the byte limit")
	}
	if err := os.WriteFile(path, oversized, 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir); err == nil {
		reopened.Close()
		t.Fatal("oversize snapshot accepted at reopen")
	}
}

// A writer killed by SIGKILL must not brick the journal: the kernel releases
// the flock and the next Open acquires it. Readiness is synchronized through
// the holder's stdout, not a sleep; the wait is bounded and the kill is
// asserted as a real SIGKILL via WaitStatus.
func TestKilledWriterReleasesLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "killed")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHolderHelper$")
	cmd.Env = append(os.Environ(), "TG_JOURNAL_HOLD_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		stdin.Close()
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	ready := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if sc.Text() == "JOURNAL-HOLDER-READY" {
				ready <- nil
				return
			}
		}
		if err := sc.Err(); err != nil {
			ready <- err
		} else {
			ready <- io.ErrUnexpectedEOF
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal("holder did not signal readiness", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("holder never signaled readiness")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	waited = true
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("holder was not killed by SIGKILL: %v", waitErr)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal("journal not reopenable after SIGKILL of the lock holder", err)
	}
	defer s.Close()
	persisted, err := s.Get("before-kill")
	if err != nil || persisted.State != Prepared || persisted.Version != 1 || persisted.AmountMSat != 1000 || persisted.FeeReserveMSat != 100 || persisted.WalletID != "wallet-a" {
		t.Fatal("prepared intent did not survive SIGKILL unchanged")
	}
	if _, err = s.Prepare(intent("after-kill"), Budget{LimitMSat: 2200}); err != nil {
		t.Fatal("reopened journal refused a new intent", err)
	}
}

func TestLockHolderHelper(t *testing.T) {
	dir := os.Getenv("TG_JOURNAL_HOLD_DIR")
	if dir == "" {
		return
	}
	s, err := Open(dir)
	if err != nil {
		os.Exit(30)
	}
	if _, err = s.Prepare(intent("before-kill"), Budget{LimitMSat: 1100}); err != nil {
		os.Exit(33)
	}
	fmt.Println("JOURNAL-HOLDER-READY")
	// Deterministic hold: block on stdin until the parent kills this process.
	// KeepAlive stops the lock file's finalizer from closing the flock meanwhile.
	if _, err = os.Stdin.Read(make([]byte, 1)); err == nil {
		os.Exit(31)
	}
	runtime.KeepAlive(s)
	os.Exit(32)
}
