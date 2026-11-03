//go:build !nft_noflock

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockGate takes an exclusive flock(2) on path for the duration of fn.
//
// flock is the right primitive here because the shim is a *separate process*:
// the module invokes the binary, so an in-process mutex in the module holds no
// lock at all across two shim runs. The lock file is created (and its parent
// made) if missing.
func lockGate(path string, fn func() error) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
