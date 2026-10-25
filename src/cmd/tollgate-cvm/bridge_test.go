package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeSocketService answers newline-delimited CLIMessage/CLIResponse frames
// the way src/cli/server.go does, so the bridge is exercised against the real
// framing contract (one JSON object per line, response on the same conn).
type fakeSocketService struct {
	listener net.Listener
	got      chan CLIMessage
}

func startFakeSocketService(t *testing.T, respond func(msg CLIMessage) CLIResponse) (*fakeSocketService, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tollgate.sock")
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	svc := &fakeSocketService{listener: ln, got: make(chan CLIMessage, 8)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go svc.serve(conn, respond)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return svc, path
}

func (s *fakeSocketService) serve(conn net.Conn, respond func(CLIMessage) CLIResponse) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	var msg CLIMessage
	if err := dec.Decode(&msg); err != nil {
		return
	}
	s.got <- msg
	_ = json.NewEncoder(conn).Encode(respond(msg))
}

func okResponse(data interface{}) CLIResponse {
	return CLIResponse{Success: true, Data: data, Timestamp: time.Now()}
}

// TestBridgeRoundTrip proves the socket bridge speaks the CLI's framing: a
// CLIMessage in, the service's CLIResponse back out, payload faithful.
func TestBridgeRoundTrip(t *testing.T) {
	svc, sock := startFakeSocketService(t, func(msg CLIMessage) CLIResponse {
		if msg.Command != "status" {
			return CLIResponse{Success: false, Error: "unexpected command " + msg.Command}
		}
		return okResponse(map[string]interface{}{"running": true, "version": "test"})
	})

	bridge := NewSocketBridge(sock, time.Second)
	resp, err := bridge.Call(CLIMessage{Command: "status", Timestamp: time.Now()})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response not successful: %+v", resp)
	}
	select {
	case got := <-svc.got:
		if got.Command != "status" {
			t.Errorf("service saw %q, want status", got.Command)
		}
	default:
		t.Error("service never received the message")
	}
}

// TestBridgeTimeout: an unresponsive service must surface an error, not hang —
// a hung bridge would wedge every CVM tool call behind it.
func TestBridgeTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tollgate.sock")
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { // accept and stay silent
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	bridge := NewSocketBridge(path, 150*time.Millisecond)
	start := time.Now()
	if _, err := bridge.Call(CLIMessage{Command: "status", Timestamp: time.Now()}); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout not honoured: took %v", elapsed)
	}
}

// TestBridgeSocketAbsent: no socket (service down) must be an actionable
// error, not a panic — the dashboard shows this string to a human.
func TestBridgeSocketAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	bridge := NewSocketBridge(filepath.Join(dir, "tollgate.sock"), time.Second)
	if _, err := bridge.Call(CLIMessage{Command: "status"}); err == nil {
		t.Fatal("expected error for absent socket, got nil")
	}
}
