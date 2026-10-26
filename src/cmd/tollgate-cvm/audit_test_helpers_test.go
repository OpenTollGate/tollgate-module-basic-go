package main

import (
	"encoding/json"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// helpers shared by the audit-fix tests

func rpcParams(t *testing.T, tool string, args ...map[string]interface{}) json.RawMessage {
	t.Helper()
	p := map[string]interface{}{"name": tool}
	if len(args) > 0 {
		p["arguments"] = args[0]
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return b
}

func req(id float64, params json.RawMessage) JSONRPCRequest {
	return JSONRPCRequest{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params}
}

func serverKeyForTest(t *testing.T) string {
	t.Helper()
	return nostr.GeneratePrivateKey()
}
