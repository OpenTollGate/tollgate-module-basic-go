package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startFakeService stands up a one-shot Unix socket service that replies to
// a single CLI request with the given CLIResponse, so the client's full
// RunE flow (prompt, socket round-trip, display, exit code) can be
// exercised deterministically.
func startFakeService(t *testing.T, response CLIResponse) {
	t.Helper()
	path := socketPath()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on fake socket: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		data, err := json.Marshal(response)
		if err != nil {
			return
		}
		conn.Write(append(data, '\n'))
	}()

	t.Cleanup(func() {
		listener.Close()
		os.Remove(path)
		wg.Wait()
	})
}

func withStdinEOF(t *testing.T) {
	t.Helper()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { devNull.Close() })
	origStdin := os.Stdin
	os.Stdin = devNull
	t.Cleanup(func() { os.Stdin = origStdin })
}

func resetDrainCmdState(t *testing.T) {
	t.Helper()
	jsonOutput = false
	drainAssumeYes = false
	t.Cleanup(func() {
		jsonOutput = false
		drainAssumeYes = false
	})
}

func drainResponseCanned(t *testing.T, success bool, saveToFile string) CLIResponse {
	t.Helper()
	return CLIResponse{
		Success: success,
		Message: "canned",
		Data: map[string]interface{}{
			"success":      success,
			"partial":      !success,
			"tokens":       []map[string]interface{}{{"mint_url": "https://mint.example/Bitcoin", "balance_sats": 50, "token": "cashu-test-token"}},
			"errors":       []map[string]interface{}{{"mint_url": "https://mint-b.test/Bitcoin", "error": "no balance available"}},
			"total_sats":   50,
			"save_to_file": saveToFile,
		},
		Timestamp: time.Now(),
	}
}

func TestDrainCashuCmd_JSONMode_ServerReportsFailure_ReturnsError(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	startFakeService(t, CLIResponse{Success: false, Error: "no balance available", Timestamp: time.Now()})

	jsonOutput = true
	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err == nil {
		t.Fatal("JSON mode must exit non-zero when the service reports success:false")
	}
}

func TestDrainCashuCmd_JSONMode_ServerReportsSuccess_Succeeds(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	startFakeService(t, CLIResponse{Success: true, Message: "drained", Timestamp: time.Now()})

	jsonOutput = true
	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err != nil {
		t.Fatalf("JSON mode on success must exit 0, got: %v", err)
	}
}

func TestDrainCashuCmd_YesFlag_SkipsPrompt_SavesTokens(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	savePath := filepath.Join(t.TempDir(), "wallet_drain_test.txt")
	startFakeService(t, drainResponseCanned(t, true, savePath))

	// stdin is EOF: the --yes flag must make the confirmation prompt
	// unnecessary for non-interactive callers.
	withStdinEOF(t)
	drainAssumeYes = true

	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err != nil {
		t.Fatalf("--yes drain must succeed end to end, got: %v", err)
	}

	saved, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("tokens must be saved to %s: %v", savePath, err)
	}
	if !strings.Contains(string(saved), "cashu-test-token") {
		t.Fatalf("saved file must contain the token, got: %q", saved)
	}
}

func TestDrainCashuCmd_PlainMode_PartialFailure_SavesTokens_ExitsNonZero(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	savePath := filepath.Join(t.TempDir(), "wallet_drain_partial.txt")
	startFakeService(t, drainResponseCanned(t, false, savePath))

	withStdinEOF(t)
	drainAssumeYes = true

	err := drainCashuCmd.RunE(drainCashuCmd, []string{})
	if err == nil {
		t.Fatal("partial drain failure must exit non-zero in plain mode")
	}

	// The successfully drained token must still be persisted even though
	// the command reports failure — hiding it would repeat issue #375's
	// fund loss through the plain-mode path.
	saved, readErr := os.ReadFile(savePath)
	if readErr != nil {
		t.Fatalf("partial tokens must be saved even on failure: %v", readErr)
	}
	if !strings.Contains(string(saved), "cashu-test-token") {
		t.Fatalf("saved file must contain the partial token, got: %q", saved)
	}
}

func TestDrainCashuCmd_PlainMode_ExplicitDecline_ReturnsError(t *testing.T) {
	resetDrainCmdState(t)
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	declined, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp stdin: %v", err)
	}
	if _, err := declined.WriteString("n\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := declined.Seek(0, 0); err != nil {
		t.Fatalf("seek stdin: %v", err)
	}
	t.Cleanup(func() { declined.Close() })

	origStdin := os.Stdin
	os.Stdin = declined
	t.Cleanup(func() { os.Stdin = origStdin })

	if err := drainCashuCmd.RunE(drainCashuCmd, []string{}); err == nil {
		t.Fatal("explicitly declined drain must exit non-zero, not masquerade as success")
	}
}
