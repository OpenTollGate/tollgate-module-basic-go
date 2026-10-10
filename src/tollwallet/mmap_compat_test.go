package tollwallet

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

// gonutsStorageError reproduces the exact error shape the wallet-creation
// chain produces on a jffs2 overlay: bbolt returns the raw mmap errno and
// every gonuts wrap above it uses %v, so the errno survives only as text
// (gonuts-tollgate v0.10.0 wallet/storage/bolt.go: "error setting bolt db: %v").
func gonutsStorageError(errno error) error {
	return fmt.Errorf("failed to create wallet: InitStorage: error setting bolt db: %v", errno)
}

func TestIsStorageMmapUnsupported(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"raw EINVAL", syscall.EINVAL, true},
		{"EINVAL wrapped with %w", fmt.Errorf("wallet: %w", syscall.EINVAL), true},
		{"EINVAL flattened by %v (gonuts shape)", gonutsStorageError(syscall.EINVAL), true},
		{"ENOENT flattened by %v", gonutsStorageError(syscall.ENOENT), false},
		{"unrelated text error", errors.New("connection refused"), false},
		{"nested unrelated chain", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.New("no route to host"))), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStorageMmapUnsupported(tc.err); got != tc.want {
				t.Fatalf("IsStorageMmapUnsupported(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
