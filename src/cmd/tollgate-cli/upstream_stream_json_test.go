package main

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
)

func TestUpstreamStreamingJSONContract(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "unreachable", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("TOLLGATE_TEST_CONFIG_DIR", shortTestConfigDir(t))
			old := jsonOutput
			jsonOutput = true
			t.Cleanup(func() { jsonOutput = old })
			if scenario != "unreachable" {
				listener, err := net.Listen("unix", socketPath())
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					defer conn.Close()
					var msg CLIMessage
					if json.NewDecoder(conn).Decode(&msg) != nil {
						return
					}
					if scenario == "empty" {
						return
					}
					enc := json.NewEncoder(conn)
					enc.Encode(CLIResponse{Progress: "[1/2] Joining..."})
					enc.Encode(CLIResponse{Success: scenario == "success", Message: "final", Error: map[bool]string{true: "", false: "join failed"}[scenario == "success"]})
				}()
				t.Cleanup(func() { listener.Close(); <-done })
			}
			output, err := captureStdout(t, func() error { return sendCommandStreaming("upstream", []string{"connect", "Saved"}, nil) })
			if (err == nil) != (scenario == "success") {
				t.Fatalf("unexpected result %v", err)
			}
			decoder := json.NewDecoder(strings.NewReader(output))
			var response CLIResponse
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("stdout must contain final JSON only: %v; %q", err, output)
			}
			if response.Success != (scenario == "success") {
				t.Fatalf("incorrect success: %+v", response)
			}
			if err := decoder.Decode(&response); err != io.EOF {
				t.Fatalf("expected exactly one JSON response, got %v", err)
			}
		})
	}
}
