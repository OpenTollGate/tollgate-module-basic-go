package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// SocketBridge dials the TollGate service's Unix socket per call and speaks
// the CLI's framing: one CLIMessage JSON object + newline written, one
// CLIResponse JSON line read back on the same connection.
//
// Per-call dialing matches how the tollgate CLI itself behaves (every
// sendCommand opens a fresh conn) and keeps the bridge stateless — a wedged
// connection can never poison the next tool call.
type SocketBridge struct {
	path    string
	timeout time.Duration
}

func NewSocketBridge(path string, timeout time.Duration) *SocketBridge {
	return &SocketBridge{path: path, timeout: timeout}
}

// Call sends one CLIMessage and returns the service's CLIResponse.
func (b *SocketBridge) Call(msg CLIMessage) (CLIResponse, error) {
	var zero CLIResponse
	conn, err := net.DialTimeout("unix", b.path, b.timeout)
	if err != nil {
		return zero, fmt.Errorf("tollgate service unreachable on %s (is tollgate-wrt running?): %w", b.path, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(b.timeout))

	data, err := json.Marshal(msg)
	if err != nil {
		return zero, fmt.Errorf("encode message: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return zero, fmt.Errorf("send message: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return zero, fmt.Errorf("read response: %w", err)
		}
		return zero, fmt.Errorf("no response from tollgate service (connection closed)")
	}
	var resp CLIResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}
