package paymentjournal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParentSyncConcurrentOpenerCannotSkipSync(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "journal")
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		s, err := open(dir, func(path string) error {
			if path == root {
				close(entered)
				<-release
				return errors.New("synthetic creator parent-sync failure")
			}
			return syncDirectory(path)
		})
		if s != nil {
			s.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("creator did not reach parent sync")
	}
	parentCalls := 0
	second, secondErr := open(dir, func(path string) error {
		if path == root {
			parentCalls++
			return errors.New("synthetic second parent-sync failure")
		}
		return syncDirectory(path)
	})
	opened := second != nil
	if second != nil {
		second.Close()
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("creator should refuse failed parent sync")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("creator did not exit")
	}
	if opened || secondErr == nil || parentCalls != 1 {
		t.Fatalf("second opener skipped parent durability: opened=%v error=%v parentCalls=%d", opened, secondErr, parentCalls)
	}
}

func TestParentSyncRetryDoesNotTrustAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "journal")
	retained := filepath.Join(dir, "retained-state")
	first, err := open(dir, func(path string) error {
		if path == root {
			// A concurrent participant or retained recovery file can make
			// best-effort empty-directory removal fail after the I/O error.
			if err := os.WriteFile(retained, []byte("synthetic retained data"), 0600); err != nil {
				t.Fatal(err)
			}
			return errors.New("synthetic first parent-sync failure")
		}
		return syncDirectory(path)
	})
	if first != nil {
		first.Close()
	}
	if err == nil || first != nil {
		t.Fatal("first open did not refuse")
	}
	parentCalls := 0
	second, err := open(dir, func(path string) error {
		if path == root {
			parentCalls++
			return errors.New("synthetic retry parent-sync failure")
		}
		return syncDirectory(path)
	})
	if second != nil {
		second.Close()
	}
	if second != nil || err == nil || parentCalls != 1 {
		t.Fatalf("retry bypassed parent sync: opened=%v error=%v parentCalls=%d", second != nil, err, parentCalls)
	}
	b, err := os.ReadFile(retained)
	if err != nil || string(b) != "synthetic retained data" {
		t.Fatal("refusal changed retained data")
	}
}

func TestParentSyncExistingJournalFailurePreservesOperations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "journal")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(intent("retained-operation"), Budget{LimitMSat: 2000}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	path := filepath.Join(dir, "operations.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := open(dir, func(path string) error {
		if path == root {
			return errors.New("synthetic existing-parent sync failure")
		}
		return syncDirectory(path)
	})
	if reopened != nil {
		reopened.Close()
	}
	if reopened != nil || err == nil {
		t.Fatal("existing journal bypassed parent sync")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed open changed durable operations")
	}
}
