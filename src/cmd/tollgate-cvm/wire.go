package main

import "time"

// CLIMessage and CLIResponse are the socket wire contract, re-declared here
// the same way src/cmd/tollgate-cli re-declares them: the Unix socket's JSON
// framing is the API, and a client that imports the server's package would
// drag the merchant/wallet graph into the package. The two structs must stay
// byte-compatible with src/cli/types.go — bridge_test.go pins the framing
// against a live socket server built from these very types.

// CLIMessage represents communication between CLI client and service.
type CLIMessage struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// CLIResponse represents a response from the service.
type CLIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Progress  string      `json:"progress,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}
