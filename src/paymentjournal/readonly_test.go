package paymentjournal

import (
	"fmt"
	"path/filepath"
	"testing"
)

// A READER MUST NEVER SEE A TORN SNAPSHOT.
//
// OpenReadOnly is used by `tollgate lnurlcash` while the daemon is writing. The safety
// argument is that persist() writes a temporary file and renames it over the target, so a
// reader opens either the whole old file or the whole new one. That argument is only worth
// anything if it holds under an actual concurrent writer, so this drives one.
//
// A torn read would surface as a JSON unmarshal failure (ErrRefused), not as silent
// corruption, so the assertion is simply: every read succeeds and sees a consistent count.
func TestReadOnlyNeverSeesATornSnapshot(t *testing.T) {
	// A NESTED directory: Open syncs the PARENT of the journal dir, and a bare t.TempDir()
	// is a fresh entry whose parent is shared and refuses that sync.
	dir := filepath.Join(t.TempDir(), "journal")
	writer, err := Open(dir)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer func() { _ = writer.Close() }()

	for i := 0; i < 4; i++ {
		if _, err := writer.Prepare(Operation{
			ID:         fmt.Sprintf("seed-%02d", i),
			WalletID:   "w",
			Direction:  Incoming,
			AmountMSat: 1000,
			Recipient:  "r",
			Request:    []byte("x"),
		}, Budget{}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 60; i++ {
			// Each Prepare rewrites and renames the whole snapshot.
			_, _ = writer.Prepare(Operation{
				ID:         fmt.Sprintf("live-%03d", i),
				WalletID:   "w",
				Direction:  Incoming,
				AmountMSat: 1000,
				Recipient:  "r",
				Request:    []byte("x"),
			}, Budget{})
		}
	}()

	reads := 0
	for {
		select {
		case <-done:
			if reads == 0 {
				t.Fatal("no reads completed while the writer ran")
			}
			return
		default:
		}
		r, err := OpenReadOnly(dir)
		if err != nil {
			t.Fatalf("read-only open failed against a live writer: %v", err)
		}
		ops, err := r.List()
		_ = r.Close()
		if err != nil {
			t.Fatalf("a concurrent read got a torn snapshot (unmarshal refused): %v", err)
		}
		if len(ops) < 4 {
			t.Fatalf("a concurrent read saw %d operations, fewer than the 4 that existed before the writer started — the file was read mid-replace", len(ops))
		}
		reads++
	}
}
