//go:build nft_noflock

package main

// lockGate without flock. This file is compiled ONLY under `-tags nft_noflock`
// and exists so the concurrency evidence has a real, code-level negative
// control: build the package with the tag stripped of its lock and the
// concurrency leg must fail. It is never part of a production build.
//
// The tag name is deliberately ugly so it is never set by accident.
func lockGate(_ string, fn func() error) error { return fn() }
