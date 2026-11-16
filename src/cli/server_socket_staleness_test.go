package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

// isStaleSocket is the ownership decision that keeps a second daemon from
// stealing the CLI surface from a live one (#504). Its contract has three
// outcomes, and the third is the one that is easy to lose in a refactor:
//
//	stale      — proof that nobody is serving: ENOENT (no file) or
//	             ECONNREFUSED from the *connect* syscall (file, no acceptor)
//	undetermined — any other dial failure (EACCES, EPERM, ETIMEDOUT, ...):
//	             the caller MUST refuse to remove, not assume it is stale
//
// Without the undetermined branch, every dial error becomes "stale" and a
// second daemon removes a live daemon's socket — the exact single-writer
// violation these tests exist to prevent. The tests below pin all three
// outcomes at the decision function, so the fail-closed default cannot be
// dropped silently (verified by mutation: returning true for every error
// leaves TestIsStaleSocketRefusesUndeterminedErrors failing).

func TestIsStaleSocketAbsentFileIsStale(t *testing.T) {
	// An absent socket file means nothing is listening — stale.
	// Wrapped the way net.Dial wraps it, so the errors.Is unwrap path is
	// exercised rather than a bare sentinel.
	err := &net.OpError{
		Op:  "dial",
		Net: "unix",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ENOENT},
	}
	if !isStaleSocket(err) {
		t.Fatalf("absent socket file must read as stale: %v", err)
	}
	// And the plain sentinel, which os.IsNotExist would have missed.
	if !isStaleSocket(os.ErrNotExist) {
		t.Fatal("bare os.ErrNotExist must read as stale")
	}
}

func TestIsStaleSocketRefusedConnectIsStale(t *testing.T) {
	// A socket file exists but nothing accepts on it — the classic stale
	// socket left by a crashed daemon. This is the case Start() reclaims.
	err := &net.OpError{
		Op:  "dial",
		Net: "unix",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}
	if !isStaleSocket(err) {
		t.Fatalf("ECONNREFUSED from connect must read as stale: %v", err)
	}
}

func TestIsStaleSocketNilIsNotStale(t *testing.T) {
	// No error means the dial succeeded: a daemon is serving. Never stale.
	if isStaleSocket(nil) {
		t.Fatal("nil error must NOT read as stale (a successful dial proves a live daemon)")
	}
}

// TestIsStaleSocketRefusesUndeterminedErrors is the fail-closed guard. Each
// case below is a dial failure that does NOT prove the socket is unused, so
// isStaleSocket must return false and the caller must refuse to remove it.
//
// Losing this behaviour is how the original bug came back: a permission
// error would remove a live daemon's socket instead of refusing.
func TestIsStaleSocketRefusesUndeterminedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "EACCES on connect (socket owned by another user)",
			err: &net.OpError{Op: "dial", Net: "unix",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}},
		},
		{
			name: "EPERM on connect",
			err: &net.OpError{Op: "dial", Net: "unix",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.EPERM}},
		},
		{
			name: "ETIMEDOUT on connect (slow/blocked, ownership unknown)",
			err: &net.OpError{Op: "dial", Net: "unix",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ETIMEDOUT}},
		},
		{
			name: "ECONNREFUSED from a NON-connect syscall (a different call's refusal)",
			err: &net.OpError{Op: "dial", Net: "unix",
				Err: &os.SyscallError{Syscall: "sendto", Err: syscall.ECONNREFUSED}},
		},
		{
			name: "generic non-syscall error",
			err:  errors.New("dial unix: some other failure"),
		},
		{
			name: "wrapped generic error",
			err:  fmt.Errorf("claiming socket: %w", errors.New("unexpected")),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isStaleSocket(tc.err) {
				t.Fatalf("undetermined dial failure must NOT read as stale (caller must refuse to remove): %v", tc.err)
			}
		})
	}
}

// TestIsStaleSocketRealDialErrors ratchets the synthetic cases above to the
// errors the runtime actually produces: a real dial against a missing path
// and a real dial against a bound-but-not-accepting path.
func TestIsStaleSocketRealDialErrors(t *testing.T) {
	dir := t.TempDir()

	missing := dir + "/not-there.sock"
	_, err := net.Dial("unix", missing)
	if err == nil {
		t.Skip("dial unexpectedly succeeded; environment does not produce the case")
	}
	if !isStaleSocket(err) {
		t.Fatalf("real dial to a missing socket must read as stale, got false for: %v", err)
	}

	// Live listener that never Accept()s: the OS still completes the connect
	// (backlog), so this asserts the *live* side is not called stale.
	live := dir + "/live.sock"
	ln, lerr := net.Listen("unix", live)
	if lerr != nil {
		t.Skipf("cannot bind test listener: %v", lerr)
	}
	defer ln.Close()

	conn, derr := net.Dial("unix", live)
	if derr == nil {
		conn.Close()
		if isStaleSocket(derr) {
			t.Fatal("a successful dial must not be stale")
		}
		return
	}
	// If the connect did fail, it must not be classified stale unless it is
	// genuinely refused — an accepted-but-not-served socket is live.
	if isStaleSocket(derr) && !errors.Is(derr, syscall.ECONNREFUSED) {
		t.Fatalf("live listener produced a non-refused error classified as stale: %v", derr)
	}
}

// TestIsStaleSocketCleanupIsIdempotent documents that the classifier has no
// side effects on the filesystem — it decides, it does not remove.
//
// Note: a Go unix listener UNLINKS its socket file on Close(), so the file
// must be observed while the listener is still held; that is what proves the
// classifier did not touch it.
func TestIsStaleSocketCleanupIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/x.sock"
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Skipf("cannot bind: %v", err)
	}
	defer ln.Close()

	if _, serr := os.Stat(p); serr != nil {
		t.Fatalf("socket file should exist while the listener is held: %v", serr)
	}

	// Call the classifier on both a stale and an undetermined error; neither
	// may remove the live socket file.
	_ = isStaleSocket(&net.OpError{Op: "dial", Net: "unix",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}})
	_ = isStaleSocket(&net.OpError{Op: "dial", Net: "unix",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}})

	if _, serr := os.Stat(p); serr != nil {
		t.Fatalf("isStaleSocket must not delete the socket file (it only classifies): %v", serr)
	}
}
