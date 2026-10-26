package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// A live daemon holds LOCK_EX on wallet.db. The read-only open must return the
// distinct "locked" verdict within its Timeout instead of retrying forever.
func TestWalletCheckReportsLockedBehindLiveDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	daemon, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()

	done := make(chan error, 1)
	go func() { done <- checkWalletDBInProcess(path) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrWalletDBLocked) {
			t.Fatalf("got %v, want ErrWalletDBLocked", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkWalletDBInProcess did not return within 5s behind a live LOCK_EX holder")
	}
}
