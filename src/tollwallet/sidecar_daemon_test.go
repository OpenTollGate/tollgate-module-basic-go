//go:build cdk_wallet

package tollwallet

// Tests for the CDK sidecar daemon.
//
// The daemon exists because cdk-go needs cgo FFI and therefore cannot be part of
// the router binary: `CGO_ENABLED=0 GOARCH=mipsle` and `arm64` both fail at the
// cdkffi package, which is precisely the constraint AGENTS.md records. So the
// wallet runs as its own process and the module reaches it over AF_UNIX through
// the sidecar protocol `sidecar.go` already speaks.
//
// These tests use the real daemon over a real unix socket, and drive it through
// the real SidecarWallet client, so what is proven is the pair, not either half.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startDaemon runs a daemon on a socket in a temp dir and returns its address
// plus a stop function.
func startDaemon(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "w.sock")
	wallet, err := NewWalletPort(filepath.Join(dir, "cdk.sqlite"), nil, false, nil)
	if err != nil {
		t.Fatalf("open cdk wallet: %v", err)
	}
	d := NewSidecarDaemon(wallet)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	go func() {
		if err := d.Serve(ctx, sock, ready); err != nil {
			t.Logf("daemon stopped: %v", err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}
	return sock, func() { cancel(); d.Wait() }
}

// A client that only ever reads must work, and the same connection must survive
// more than one request: the protocol is line-delimited, and a daemon that
// closes after one reply would break every caller that asks two questions.
func TestSidecarDaemonAnswersRepeatedReads(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	for i, method := range []string{"info", "get_balance", "get_all_mint_balances"} {
		req := map[string]any{"id": i + 1, "method": method}
		if err := enc.Encode(req); err != nil {
			t.Fatalf("write %s: %v", method, err)
		}
		var resp struct {
			ID     uint64          `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  string          `json:"error"`
		}
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("read %s: %v", method, err)
		}
		if resp.ID != uint64(i+1) {
			t.Fatalf("%s: id = %d, want %d (responses must be correlated)", method, resp.ID, i+1)
		}
		if !resp.OK {
			t.Fatalf("%s failed: %s", method, resp.Error)
		}
	}
}

// The real client must be able to open the daemon and read a manifest and a
// balance. This is the seam the module actually uses, so it is what is tested.
func TestSidecarDaemonServesTheRealClient(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	w, manifest, err := OpenWallet(WalletConfig{Backend: "sidecar", Socket: sock})
	if err != nil {
		t.Fatalf("open sidecar wallet: %v", err)
	}
	defer w.Shutdown()

	if manifest == nil {
		t.Fatal("a sidecar backend must return the daemon's capability manifest")
	}
	if bal := w.GetBalance(); bal != 0 {
		t.Fatalf("balance = %d, want 0 on a fresh wallet", bal)
	}
}

// An unknown method is a refusal with an error, not a hang and not a silent
// success: a caller must be able to tell that the daemon did not understand it.
func TestSidecarDaemonRefusesUnknownMethod(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	dec := json.NewDecoder(conn)

	if err := json.NewEncoder(conn).Encode(map[string]any{"id": 1, "method": "no_such_method"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.OK {
		t.Fatal("an unknown method was accepted")
	}
	if resp.Error == "" {
		t.Fatal("a refusal must say what it refused")
	}
}

// A malformed line must not kill the daemon: one bad client cannot be allowed to
// stop the wallet for every other caller.
func TestSidecarDaemonSurvivesGarbage(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	dec := json.NewDecoder(conn)
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("a malformed request got no reply at all: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("malformed request replied ok=%v error=%q", resp.OK, resp.Error)
	}
	conn.Close()

	// The daemon must still answer the next client.
	conn2, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("daemon died after a malformed request: %v", err)
	}
	defer conn2.Close()
	if err := json.NewEncoder(conn2).Encode(map[string]any{"id": 9, "method": "get_balance"}); err != nil {
		t.Fatalf("write after garbage: %v", err)
	}
	var ok struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(conn2).Decode(&ok); err != nil || !ok.OK {
		t.Fatalf("daemon did not serve a good client after a bad one: %v %v", err, ok.OK)
	}
}

// The socket must not be world-writable: this process holds money.
func TestSidecarDaemonSocketIsPrivate(t *testing.T) {
	sock, stop := startDaemon(t)
	defer stop()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("socket mode = %o, want no group/other access", perm)
	}
}
