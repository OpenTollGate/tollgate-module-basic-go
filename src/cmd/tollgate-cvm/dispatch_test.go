package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return json.RawMessage(b)
}

// TestDispatchToolsList: tools/list answers with the surface, no socket call.
func TestDispatchToolsList(t *testing.T) {
	d := NewDispatcher(NewAuthorizer(""), nil, "npub1test")
	resp := d.Dispatch("someone", JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list errored: %+v", resp.Error)
	}
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(result.Tools) != len(ToolSurface()) {
		t.Fatalf("tools/list returned %d tools, surface has %d", len(result.Tools), len(ToolSurface()))
	}
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "wallet_balance" {
			found = true
		}
		if tool.Name == "drain" || tool.Name == "fund" {
			t.Fatalf("banned tool %q in tools/list output", tool.Name)
		}
	}
	if !found {
		t.Fatal("wallet_balance missing from tools/list")
	}
}

// TestDispatchCall: a permitted read tool reaches the bridge and returns the
// service's CLIResponse as the JSON-RPC result.
func TestDispatchCall(t *testing.T) {
	called := ""
	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		called = msg.Command
		return CLIResponse{Success: true, Data: map[string]interface{}{"balance_sats": 5}}, nil
	}}
	d := NewDispatcher(NewAuthorizer(""), bridge, "npub1test")

	params, _ := json.Marshal(map[string]interface{}{"name": "wallet_balance"})
	resp := d.Dispatch("anyone", JSONRPCRequest{JSONRPC: "2.0", ID: float64(2), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("tools/call errored: %+v", resp.Error)
	}
	if called != "wallet" {
		t.Fatalf("bridge saw command %q, want wallet (tool maps to CLI command)", called)
	}
	var cliResp CLIResponse
	if err := json.Unmarshal(resp.Result, &cliResp); err != nil {
		t.Fatalf("result should carry the CLIResponse: %v", err)
	}
	if !cliResp.Success {
		t.Fatal("CLIResponse not successful")
	}
}

// TestDispatchAuthzRefusal: a stranger's write-tool call is refused with an
// authz error code AND the tool never reaches the bridge.
//
// The tool must be a REAL write tool: using one that is absent from the
// surface would still produce an error (unknown tool), so the test would pass
// while proving nothing about the maintainer gate.
func TestDispatchAuthzRefusal(t *testing.T) {
	bridgeCalled := false
	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		bridgeCalled = true
		return CLIResponse{Success: true}, nil
	}}
	const maintainer = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	d := NewDispatcher(NewAuthorizer(maintainer), bridge, "npub1test")

	// Assert the premise: this tool must actually be on the surface and be a
	// write tool, or the refusal below is not evidence of the maintainer gate.
	var target *Tool
	for i := range ToolSurface() {
		if ToolSurface()[i].Name == "private_disable" {
			target = &ToolSurface()[i]
			break
		}
	}
	if target == nil || !target.Write {
		t.Fatal("private_disable must be a real write tool for this test to mean anything")
	}

	params, _ := json.Marshal(map[string]interface{}{"name": "private_disable"})
	resp := d.Dispatch("npub1stranger", JSONRPCRequest{JSONRPC: "2.0", ID: float64(3), Method: "tools/call", Params: params})
	if resp.Error == nil {
		t.Fatal("stranger's private_disable succeeded")
	}
	if resp.Error.Code != ErrAuthz {
		t.Fatalf("error code %d, want ErrAuthz %d", resp.Error.Code, ErrAuthz)
	}
	if !strings.Contains(resp.Error.Message, "maintainer-only") {
		t.Fatalf("refusal %q must be the maintainer gate, not an unknown-tool error", resp.Error.Message)
	}
	if bridgeCalled {
		t.Fatal("refused call reached the bridge — the gate must short-circuit BEFORE dispatch")
	}
}

// The maintainer's write call goes through, and its arguments are forwarded.
func TestDispatchMaintainerWrite(t *testing.T) {
	var got CLIMessage
	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		got = msg
		return CLIResponse{Success: true, Message: "set"}, nil
	}}
	const maintainer = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	d := NewDispatcher(NewAuthorizer(maintainer), bridge, "npub1test")

	params, _ := json.Marshal(map[string]interface{}{"name": "config_set", "arguments": map[string]interface{}{"key": "log_level", "value": "debug"}})
	resp := d.Dispatch(maintainer, JSONRPCRequest{JSONRPC: "2.0", ID: float64(4), Method: "tools/call", Params: params})
	if resp.Error != nil {
		t.Fatalf("maintainer's config_set refused: %+v", resp.Error)
	}
	if got.Command != "config" {
		t.Fatalf("bridge saw %q, want config", got.Command)
	}
	// The service parses args as: subcommand, key, value (handleConfigSet
	// reads args[1], args[2] — verified in src/cli/config.go).
	if len(got.Args) != 3 || got.Args[0] != "set" || got.Args[1] != "log_level" || got.Args[2] != "debug" {
		t.Fatalf("bridge args %v, want [set log_level debug]", got.Args)
	}
}

// Unknown method → JSON-RPC method-not-found, not a crash.
func TestDispatchUnknownMethod(t *testing.T) {
	d := NewDispatcher(NewAuthorizer(""), nil, "npub1test")
	resp := d.Dispatch("x", JSONRPCRequest{JSONRPC: "2.0", ID: float64(5), Method: "bogus/method"})
	if resp.Error == nil || resp.Error.Code != ErrMethodNF {
		t.Fatalf("bogus method: %+v", resp.Error)
	}
}

// tools/call with missing name → params error.
func TestDispatchCallNoName(t *testing.T) {
	d := NewDispatcher(NewAuthorizer(""), nil, "npub1test")
	resp := d.Dispatch("x", JSONRPCRequest{JSONRPC: "2.0", ID: float64(6), Method: "tools/call", Params: json.RawMessage(`{}`)})
	if resp.Error == nil || resp.Error.Code != ErrParams {
		t.Fatalf("empty params: %+v", resp.Error)
	}
}

var _ = mustJSON
