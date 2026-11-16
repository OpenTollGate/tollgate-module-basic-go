package tollwallet

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The single-writer invariant (#504): exactly one process may hold the
// wallet DB's write lock. These tests drive the REAL open path —
// New → fork LoadWallet → bolt.Open(Timeout: 5s) — against a database
// file locked by an external holder (an flock, exactly what another
// daemon or the sidecar would hold), and pin both refusal outcomes:
//
//   - the refusal is ErrWalletLocked (distinguishable from every other
//     construction failure, so the daemon can fail the boot instead of
//     degrading with a misleading "first boot or no cached data" log);
//   - the refusal is FAST (bounded by the fork's 5 s open timeout, not an
//     unbounded block);
//   - recovery: once the holder releases, the next open succeeds — a
//     crashed holder never wedges the wallet permanently.
func TestNewRefusesWhenAnotherProcessHoldsWalletDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wallet.db")

	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("creating the wallet.db holder file: %v", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("taking the external flock: %v", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	start := time.Now()
	_, err = New(dir, []string{"https://mint.example.test"}, false)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("New succeeded while another process holds the wallet.db flock — the single-writer invariant is not enforced")
	}
	if !errors.Is(err, ErrWalletLocked) {
		t.Fatalf("expected ErrWalletLocked through the wrap chain, got: %v", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("the lock refusal took %v — the open must be bounded by the fork's 5 s timeout", elapsed)
	}
}

func TestNewAcquiresWalletAfterHolderReleases(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wallet.db")

	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("creating the wallet.db holder file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("taking the external flock: %v", err)
	}

	// A conflicting open while held must refuse…
	if _, err := New(dir, []string{"https://mint.example.test"}, false); !errors.Is(err, ErrWalletLocked) {
		t.Fatalf("expected ErrWalletLocked while held, got: %v", err)
	}

	// …and after the holder releases (the crash/stop of the other daemon),
	// the next open must succeed and hold the DB itself.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("releasing the external flock: %v", err)
	}
	f.Close()

	w, err := New(dir, []string{"https://mint.example.test"}, false)
	if err != nil {
		t.Fatalf("New after holder release: %v", err)
	}
	if err := w.Shutdown(); err != nil {
		t.Fatalf("shutting the recovered wallet: %v", err)
	}
}
