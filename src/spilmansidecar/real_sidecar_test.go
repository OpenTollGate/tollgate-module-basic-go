package spilmansidecar

// Test-only helper that runs the REAL Rust sidecar binary, so the two
// independent implementations of the v1 contract are proven to agree rather
// than assumed to. Skipped unless SPILMAN_SIDECAR_BIN names a built binary.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// SPILMAN_SIDECAR_BIN_ENV names the built sidecar binary for the
// cross-language tests. Declared once so the skip message and the lookup cannot
// drift apart.
const SPILMAN_SIDECAR_BIN_ENV = "SPILMAN_SIDECAR_BIN"

type sidecarProcess struct {
	cmd    *exec.Cmd
	socket string
}

// newSidecarProcess starts the real binary on a socket inside an existing 0700
// directory and waits for the endpoint to appear.
func newSidecarProcess(t *testing.T, bin, dir, socket string) *sidecarProcess {
	t.Helper()
	cmd := exec.Command(bin, "--socket", socket)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sidecar: %v", err)
	}
	p := &sidecarProcess{cmd: cmd, socket: socket}
	t.Cleanup(p.Stop)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(socket); err == nil {
			return p
		}
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sidecar did not bind %s", socket)
	return nil
}

// Stop terminates the sidecar and reaps it. A failure to stop is reported but
// never masks the test's own result.
func (p *sidecarProcess) Stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

func (p *sidecarProcess) client(t *testing.T) *Client {
	t.Helper()
	c, err := Dial(context.Background(), p.socket, Options{Timeout: testTimeout})
	if err != nil {
		t.Fatalf("dial %s: %v", p.socket, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// realSidecar starts the binary or skips the test.
func realSidecar(t *testing.T) *sidecarProcess {
	t.Helper()
	bin := os.Getenv("SPILMAN_SIDECAR_BIN")
	if bin == "" {
		t.Skip("set SPILMAN_SIDECAR_BIN to the built sidecar to run the cross-language check")
	}
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return newSidecarProcess(t, bin, dir, filepath.Join(dir, "spilman.sock"))
}

// TestRealSidecarRefusesVersionTwo proves both implementations agree on the
// refusal path, not just the happy path: a version-2 frame is answered with an
// error that STILL correlates, so an upgrade fails closed and visibly instead
// of degrading into "unknown shape".
func TestRealSidecarRefusesVersionTwo(t *testing.T) {
	p := realSidecar(t)
	c := p.client(t)

	raw := `{"version":2,"request_id":"req-v2","operation_id":"op-v2","method":"capabilities"}` + "\n"
	if _, err := c.conn.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(testTimeout))
	line, err := bufio.NewReader(c.conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var reply struct {
		Version      int      `json:"version"`
		RequestID    string   `json:"request_id"`
		OperationID  string   `json:"operation_id"`
		Error        string   `json:"error"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		t.Fatalf("reply is not JSON: %v (%q)", err, line)
	}
	if reply.Error != string(ErrCodeUnsupportedVersion) {
		t.Fatalf("error = %q, want %q", reply.Error, ErrCodeUnsupportedVersion)
	}
	if reply.RequestID != "req-v2" || reply.OperationID != "op-v2" {
		t.Fatalf("refusal did not correlate: %+v", reply)
	}
	if reply.Version != ProtocolVersion {
		t.Fatalf("refusal declared version %d", reply.Version)
	}
	if HasSettlementCapability(reply.Capabilities) {
		t.Fatalf("refusal advertised settlement: %v", reply.Capabilities)
	}
}
