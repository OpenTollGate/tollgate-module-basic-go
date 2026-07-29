package sysexec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type LogFunc func(level string, msg string, fields map[string]any)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
	RunWithRetry(ctx context.Context, retries int, delay time.Duration, name string, args ...string) error
}

type DefaultRunner struct {
	Log LogFunc
}

func NewDefaultRunner(log LogFunc) *DefaultRunner {
	if log == nil {
		log = func(string, string, map[string]any) {}
	}
	return &DefaultRunner{Log: log}
}

func (r *DefaultRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	fields := map[string]any{
		"cmd":      name,
		"args":     args,
		"duration": duration.String(),
	}

	if err != nil {
		fields["output"] = string(output)
		if ctx.Err() != nil {
			fields["ctx_err"] = ctx.Err().Error()
			r.Log("error", fmt.Sprintf("command timed out: %s", name), fields)
			return string(output), fmt.Errorf("%s timed out after %v: %w (output: %s)", name, duration, ctx.Err(), string(output))
		}
		r.Log("error", fmt.Sprintf("command failed: %s", name), fields)
		return string(output), fmt.Errorf("%s failed: %w (output: %s)", name, err, string(output))
	}

	r.Log("debug", fmt.Sprintf("command succeeded: %s", name), fields)
	return string(output), nil
}

func (r *DefaultRunner) RunWithRetry(ctx context.Context, retries int, delay time.Duration, name string, args ...string) error {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		_, err := r.Run(ctx, name, args...)
		if err == nil {
			if attempt > 0 {
				r.Log("info", fmt.Sprintf("command succeeded after %d retries: %s", attempt, name), nil)
			}
			return nil
		}
		lastErr = err
		if attempt < retries && shouldRetry(err) {
			r.Log("warn", fmt.Sprintf("retrying %s (attempt %d/%d): %v", name, attempt+1, retries, err), nil)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		break
	}
	return fmt.Errorf("%s failed after %d attempts: %w", name, retries+1, lastErr)
}

func shouldRetry(err error) bool {
	msg := err.Error()
	for _, pattern := range []string{"timeout", "timed out", "temporarily unavailable", "connection refused", "no route to host"} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

type MockCall struct {
	Name string
	Args []string
}

type MockRunner struct {
	mu       sync.Mutex
	Outputs  map[string]string
	Errors   map[string]error
	Calls    []MockCall
	Sequence []MockResponse
	seqIdx   int
}

type MockResponse struct {
	Output string
	Err    error
}

func NewMockRunner() *MockRunner {
	return &MockRunner{
		Outputs: make(map[string]string),
		Errors:  make(map[string]error),
	}
}

func (m *MockRunner) WithResponse(key, output string, err error) *MockRunner {
	m.Outputs[key] = output
	m.Errors[key] = err
	return m
}

func (m *MockRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})

	if len(m.Sequence) > 0 {
		if m.seqIdx < len(m.Sequence) {
			resp := m.Sequence[m.seqIdx]
			m.seqIdx++
			return resp.Output, resp.Err
		}
	}

	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}
	return m.Outputs[key], m.Errors[key]
}

func (m *MockRunner) RunWithRetry(ctx context.Context, retries int, delay time.Duration, name string, args ...string) error {
	_, err := m.Run(ctx, name, args...)
	return err
}

func (m *MockRunner) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

func (m *MockRunner) WasCalledWith(name string, args ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := MockCall{Name: name, Args: args}
	for _, c := range m.Calls {
		if c.Name == target.Name && fmt.Sprint(c.Args) == fmt.Sprint(target.Args) {
			return true
		}
	}
	return false
}
