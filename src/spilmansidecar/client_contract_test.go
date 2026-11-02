package spilmansidecar

// Contract tests for the Go side of the Spilman sidecar IPC boundary.
//
// The fixture server below is a REAL AF_UNIX listener in a real 0700 directory:
// the properties under test (directory ownership, socket type, exactly-one-frame
// framing, response correlation) are kernel/filesystem facts, so a mock would
// only assert what it was told. Each test drives exactly the behaviour the ADR
// promises, and the failure cases are the point — a client that accepts a
// mismatched response id is how a settlement reply gets applied to the wrong
// operation.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 5 * time.Second

// fixture is a scriptable AF_UNIX server standing in for the sidecar binary.
type fixture struct {
	t        *testing.T
	dir      string
	socket   string
	handler  func(req map[string]any) []byte
	mu       sync.Mutex
	connects int
	frames   int
}

func newFixture(t *testing.T, handler func(req map[string]any) []byte) *fixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	f := &fixture{t: t, dir: dir, socket: filepath.Join(dir, "spilman.sock"), handler: handler}

	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.connects++
			f.mu.Unlock()
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fixture) connectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects
}

func (f *fixture) frameCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frames
}

func (f *fixture) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(testTimeout))
	reader := bufio.NewReader(conn)
	// One frame at a time, like the real sidecar: a second exchange on the same
	// connection must work, and a written-but-unanswered frame must be visible.
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		f.mu.Lock()
		f.frames++
		f.mu.Unlock()
		var req map[string]any
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		out := f.handler(req)
		if out == nil {
			return // read it, answer nothing: the ambiguous-exchange case
		}
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func (f *fixture) client(t *testing.T) *Client {
	t.Helper()
	c, err := Dial(context.Background(), f.socket, Options{Timeout: testTimeout})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// echoReply builds a well-formed reply mirroring the request's correlation ids.
func echoReply(req map[string]any, mutate func(map[string]any)) []byte {
	resp := map[string]any{
		"version":      float64(ProtocolVersion),
		"request_id":   req["request_id"],
		"operation_id": req["operation_id"],
		"capabilities": []string{CapabilityTransportV1},
		"error":        "",
	}
	if mutate != nil {
		mutate(resp)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return append(encoded, '\n')
}

// --- happy path ------------------------------------------------------------

func TestCapabilitiesReturnsTransportOnly(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		if req["method"] != "capabilities" {
			t.Errorf("client sent method %v", req["method"])
		}
		return echoReply(req, nil)
	})

	caps, err := f.client(t).Capabilities(context.Background(), "op-open-1")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps) != 1 || caps[0] != CapabilityTransportV1 {
		t.Fatalf("capabilities = %v, want [%s]", caps, CapabilityTransportV1)
	}
	if HasSettlementCapability(caps) {
		t.Fatal("client must not accept a transport-only peer as settlement-capable")
	}
}

func TestCapabilitiesUsesFreshRequestIDPerCall(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	f := newFixture(t, func(req map[string]any) []byte {
		mu.Lock()
		seen = append(seen, req["request_id"].(string))
		mu.Unlock()
		return echoReply(req, nil)
	})
	c := f.client(t)
	for i := 0; i < 3; i++ {
		if _, err := c.Capabilities(context.Background(), "op-open-1"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	unique := map[string]bool{}
	for _, id := range seen {
		unique[id] = true
	}
	if len(unique) != len(seen) {
		t.Fatalf("request ids repeated within one connection: %v", seen)
	}
}

// --- response correlation --------------------------------------------------

func TestRejectsMismatchedRequestID(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) { r["request_id"] = "some-other-request" })
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrResponseCorrelation) {
		t.Fatalf("err = %v, want ErrResponseCorrelation", err)
	}
}

func TestRejectsMismatchedOperationID(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) { r["operation_id"] = "some-other-operation" })
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrResponseCorrelation) {
		t.Fatalf("err = %v, want ErrResponseCorrelation", err)
	}
}

func TestRejectsMissingIDsInResponse(t *testing.T) {
	for _, field := range []string{"request_id", "operation_id"} {
		field := field
		f := newFixture(t, func(req map[string]any) []byte {
			return echoReply(req, func(r map[string]any) { delete(r, field) })
		})
		_, err := f.client(t).Capabilities(context.Background(), "op-1")
		if !errors.Is(err, ErrResponseCorrelation) {
			t.Fatalf("missing %s: err = %v, want ErrResponseCorrelation", field, err)
		}
	}
}

func TestRejectsUnsupportedResponseVersion(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) { r["version"] = 2 })
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrResponseVersion) {
		t.Fatalf("err = %v, want ErrResponseVersion", err)
	}
}

func TestRejectsUnknownResponseField(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) { r["receiver_proofs"] = "not-in-v1" })
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrResponseShape) {
		t.Fatalf("err = %v, want ErrResponseShape", err)
	}
}

// --- error mapping ---------------------------------------------------------

func TestUnsupportedCapabilityIsRecognisable(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) {
			r["error"] = string(ErrCodeUnsupportedCapability)
			r["capabilities"] = []string{CapabilityTransportV1}
		})
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("err = %v, want ErrUnsupportedCapability", err)
	}
}

func TestServerErrorTextIsPreserved(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return echoReply(req, func(r map[string]any) { r["error"] = string(ErrCodeInvalidRequest) })
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if err == nil || !strings.Contains(err.Error(), string(ErrCodeInvalidRequest)) {
		t.Fatalf("err = %v, want it to name invalid_request", err)
	}
}

// --- framing --------------------------------------------------------------

func TestRejectsOversizeResponse(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		// No newline: a client that buffers until EOF would grow without bound.
		return []byte(strings.Repeat("x", MaxFrameBytes+1))
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
}

func TestRejectsNonJSONResponse(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return []byte("not json at all\n")
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrResponseShape) {
		t.Fatalf("err = %v, want ErrResponseShape", err)
	}
}

func TestRejectsInvalidOperationIDBeforeSending(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		t.Errorf("a rejected operation id must never reach the wire")
		return echoReply(req, nil)
	})
	c := f.client(t)
	_, err := c.Capabilities(context.Background(), "bad op id")
	if !errors.Is(err, ErrInvalidOperationID) {
		t.Fatalf("err = %v, want ErrInvalidOperationID", err)
	}
	_ = c.Close()
	// Nothing may have reached the wire. Give the fixture's reader a moment to
	// observe a frame it would otherwise have read after this assertion.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f.frameCount() != 0 {
			t.Fatalf("a refused operation id reached the wire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.connectCount() != 1 {
		t.Fatalf("connects = %d, want the single dial only", f.connectCount())
	}
}

// --- ambiguity: written but unanswered ------------------------------------

func TestUnansweredExchangeIsAmbiguousAndNeverRetried(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		return nil // read the request, answer nothing, close
	})
	_, err := f.client(t).Capabilities(context.Background(), "op-1")
	if !errors.Is(err, ErrExchangeAmbiguous) {
		t.Fatalf("err = %v, want ErrExchangeAmbiguous", err)
	}
	// Exactly one connection: no automatic retry loop may hide an ambiguous
	// exchange. A future money-moving method must reconcile, not resend.
	if got := f.connectCount(); got != 1 {
		t.Fatalf("connects = %d, want 1 (no blind retry)", got)
	}
}

func TestDeadlineBoundsAStalledServer(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte {
		time.Sleep(2 * testTimeout / 2 * 2) // longer than the client deadline
		return echoReply(req, nil)
	})
	c := f.client(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Capabilities(ctx, "op-1")
	if err == nil {
		t.Fatalf("a stalled server must produce an error")
	}
	if elapsed := time.Since(start); elapsed > testTimeout {
		t.Fatalf("deadline not honoured: %v", elapsed)
	}
}

// --- endpoint verification -------------------------------------------------

func TestRefusesWorldAccessibleRuntimeDir(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte { return echoReply(req, nil) })
	if err := os.Chmod(f.dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_, err := Dial(context.Background(), f.socket, Options{Timeout: testTimeout})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("err = %v, want ErrUnsafeEndpoint", err)
	}
}

func TestRefusesSymlinkedSocket(t *testing.T) {
	f := newFixture(t, func(req map[string]any) []byte { return echoReply(req, nil) })
	link := filepath.Join(f.dir, "linked.sock")
	if err := os.Symlink(f.socket, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err := Dial(context.Background(), link, Options{Timeout: testTimeout})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("err = %v, want ErrUnsafeEndpoint (a symlink can redirect the endpoint)", err)
	}
}

func TestRefusesRegularFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "spilman.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Dial(context.Background(), path, Options{Timeout: testTimeout})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("err = %v, want ErrUnsafeEndpoint", err)
	}
}

func TestRequireCapabilityIsExplicit(t *testing.T) {
	if err := RequireCapability([]string{CapabilityTransportV1}, CapabilityTransportV1); err != nil {
		t.Fatalf("present capability rejected: %v", err)
	}
	if err := RequireCapability([]string{CapabilityTransportV1}, "settlement.v1"); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("absent capability must be ErrUnsupportedCapability, got %v", err)
	}
}

// --- validation helpers ----------------------------------------------------

func TestValidateIDRules(t *testing.T) {
	ok := []string{"a", "A-9_z", strings.Repeat("x", MaxIDLength)}
	for _, id := range ok {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{"", " ", "a b", "a.b", "a/b", "a\nb", strings.Repeat("x", MaxIDLength+1), "émoji"}
	for _, id := range bad {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil, want an error", id)
		}
	}
}

func TestFrameBoundsAreTheProtocolBound(t *testing.T) {
	if MaxFrameBytes != 65536 {
		t.Fatalf("MaxFrameBytes = %d, must match the sidecar's declared bound", MaxFrameBytes)
	}
	if ProtocolVersion != 1 {
		t.Fatalf("ProtocolVersion = %d", ProtocolVersion)
	}
}

// --- integration with the real sidecar binary (opt-in) --------------------

// TestRealSidecarCapabilities runs the actual Rust binary when
// SPILMAN_SIDECAR_BIN points at it. It is skipped otherwise, so the unit lane
// stays hermetic while the milestone can still prove the two sides agree.
func TestRealSidecarCapabilities(t *testing.T) {
	bin := os.Getenv("SPILMAN_SIDECAR_BIN")
	if bin == "" {
		t.Skip("set SPILMAN_SIDECAR_BIN to the built sidecar to run the cross-language check")
	}
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	socket := filepath.Join(dir, "spilman.sock")
	cmd := newSidecarProcess(t, bin, dir, socket)
	defer cmd.Stop()

	c := cmd.client(t)
	caps, err := c.Capabilities(context.Background(), "op-integration-1")
	if err != nil {
		t.Fatalf("Capabilities against the real sidecar: %v", err)
	}
	if len(caps) != 1 || caps[0] != CapabilityTransportV1 {
		t.Fatalf("capabilities = %v", caps)
	}
	if HasSettlementCapability(caps) {
		t.Fatalf("the real sidecar must not advertise settlement in this milestone")
	}
}

func writeln(w io.Writer, s string) error {
	_, err := fmt.Fprintln(w, s)
	return err
}

var _ = writeln
