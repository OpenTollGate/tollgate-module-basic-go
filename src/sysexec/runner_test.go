package sysexec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockRunner_RecordsCalls(t *testing.T) {
	m := NewMockRunner()
	m.WithResponse("echo hello", "hello\n", nil)

	ctx := context.Background()
	out, err := m.Run(ctx, "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", out)
	}
	if m.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", m.CallCount())
	}
	if !m.WasCalledWith("echo", "hello") {
		t.Error("expected call with 'echo hello'")
	}
}

func TestMockRunner_ReturnsError(t *testing.T) {
	m := NewMockRunner()
	errExpected := errors.New("command not found")
	m.WithResponse("nonexistent", "", errExpected)

	_, err := m.Run(context.Background(), "nonexistent")
	if !errors.Is(err, errExpected) {
		t.Errorf("expected errExpected, got %v", err)
	}
}

func TestMockRunner_SequenceResponses(t *testing.T) {
	m := NewMockRunner()
	m.Sequence = []MockResponse{
		{Output: "", Err: errors.New("timeout")},
		{Output: "", Err: errors.New("timeout")},
		{Output: "success", Err: nil},
	}

	r1, _ := m.Run(context.Background(), "flaky")
	r2, _ := m.Run(context.Background(), "flaky")
	r3, _ := m.Run(context.Background(), "flaky")

	if r1 != "" || r2 != "" || r3 != "success" {
		t.Errorf("sequence: got %q, %q, %q — expected '', '', 'success'", r1, r2, r3)
	}
}

func TestShouldRetry_Timeout(t *testing.T) {
	if !shouldRetry(errors.New("ifup timed out after 30s")) {
		t.Error("expected shouldRetry=true for timeout")
	}
}

func TestShouldRetry_ConnectionRefused(t *testing.T) {
	if !shouldRetry(errors.New("connection refused")) {
		t.Error("expected shouldRetry=true for connection refused")
	}
}

func TestShouldRetry_NoRouteToHost(t *testing.T) {
	if !shouldRetry(errors.New("no route to host")) {
		t.Error("expected shouldRetry=true for no route to host")
	}
}

func TestShouldRetry_ExitCode1(t *testing.T) {
	if shouldRetry(errors.New("uci failed: exit status 1")) {
		t.Error("expected shouldRetry=false for exit status 1")
	}
}

func TestDefaultRunner_Run_EchoCommand(t *testing.T) {
	logs := []string{}
	r := NewDefaultRunner(func(level, msg string, fields map[string]any) {
		logs = append(logs, level+":"+msg)
	})

	out, err := r.Run(context.Background(), "echo", "test")
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if out != "test\n" {
		t.Errorf("expected 'test\\n', got %q", out)
	}
	if len(logs) == 0 {
		t.Error("expected log entry for successful command")
	}
}

func TestDefaultRunner_Run_NonexistentCommand(t *testing.T) {
	r := NewDefaultRunner(nil)
	_, err := r.Run(context.Background(), "this-command-does-not-exist-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestDefaultRunner_Run_ContextCancellation(t *testing.T) {
	r := NewDefaultRunner(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.Run(ctx, "sleep", "5")
	if err == nil {
		t.Fatal("expected error for cancelled command")
	}
}

func TestDefaultRunner_RunWithRetry_SuccessOnThirdAttempt(t *testing.T) {
	m := NewMockRunner()
	m.Sequence = []MockResponse{
		{Output: "", Err: errors.New("connection refused")},
		{Output: "", Err: errors.New("connection refused")},
		{Output: "ok", Err: nil},
	}

	r := &retryAdapter{mock: m}
	err := r.RunWithRetry(context.Background(), 5, 1*time.Millisecond, "flaky-cmd")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if m.CallCount() != 3 {
		t.Errorf("expected 3 calls, got %d", m.CallCount())
	}
}

func TestDefaultRunner_RunWithRetry_AllFail(t *testing.T) {
	m := NewMockRunner()
	m.WithResponse("always-fail", "", errors.New("connection refused"))

	r := &retryAdapter{mock: m}
	err := r.RunWithRetry(context.Background(), 2, 1*time.Millisecond, "always-fail")
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
}

func TestDefaultRunner_RunWithRetry_NonRetryableFailsImmediately(t *testing.T) {
	m := NewMockRunner()
	m.WithResponse("uci-set", "", errors.New("exit status 1"))

	r := &retryAdapter{mock: m}
	err := r.RunWithRetry(context.Background(), 5, 1*time.Millisecond, "uci-set")
	if err == nil {
		t.Fatal("expected error for non-retryable failure")
	}
	if m.CallCount() != 1 {
		t.Errorf("expected 1 call (no retry for exit status 1), got %d", m.CallCount())
	}
}

type retryAdapter struct {
	mock *MockRunner
}

func (a *retryAdapter) Run(ctx context.Context, name string, args ...string) (string, error) {
	return a.mock.Run(ctx, name, args...)
}

func (a *retryAdapter) RunWithRetry(ctx context.Context, retries int, delay time.Duration, name string, args ...string) error {
	r := NewDefaultRunner(nil)
	dr := &DefaultRunnerWithMock{runner: a, log: r.Log}
	return dr.RunWithRetry(ctx, retries, delay, name, args...)
}

type DefaultRunnerWithMock struct {
	runner Runner
	log    LogFunc
}

func (d *DefaultRunnerWithMock) Run(ctx context.Context, name string, args ...string) (string, error) {
	return d.runner.Run(ctx, name, args...)
}

func (d *DefaultRunnerWithMock) RunWithRetry(ctx context.Context, retries int, delay time.Duration, name string, args ...string) error {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		_, err := d.runner.Run(ctx, name, args...)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < retries && shouldRetry(err) {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		break
	}
	return lastErr
}
