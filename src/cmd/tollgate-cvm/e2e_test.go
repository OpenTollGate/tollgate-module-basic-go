//go:build e2e

// E2E: two REAL keys over REAL relays — the proof the ADR's assertions
// demand. Skipped unless TOLLGATE_CVM_E2E=1 (live test; needs network).
// Run: TOLLGATE_CVM_E2E=1 TOLLGATE_CVM_NSEC_HEX=<server-hex> go test -tags e2e -run E2E ./...
package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

var e2eRelays = []string{"wss://relay2.contextvm.org", "wss://relay.primal.net"}

// E2ERequestResponse: client wraps a status request to the server key,
// publishes the gift wrap, the daemon-side Serve loop (driven by the same
// unwrap/dispatch path) receives, dispatches against a FakeBridge, and the
// response gift-wrap comes back through the relay to the client.
//
// This exercises: real relay delivery of kind 1059, real NIP-44 keys, the
// p-tag client-side filter, unwrap, authz, dispatch, and the reply path.
func TestE2ERequestResponse(t *testing.T) {
	if os.Getenv("TOLLGATE_CVM_E2E") != "1" {
		t.Skip("set TOLLGATE_CVM_E2E=1 to run live relay e2e")
	}

	serverSK := nostr.GeneratePrivateKey()
	serverPK, _ := nostr.GetPublicKey(serverSK)
	clientSK := nostr.GeneratePrivateKey()
	clientPK, _ := nostr.GetPublicKey(clientSK)
	if err := ValidateKeyPair(serverSK, clientSK); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := nostr.NewSimplePool(ctx)

	// Client: subscribe for gift wraps addressed to the client key.
	clientEvents := pool.SubMany(ctx, e2eRelays, nostr.Filters{
		{Kinds: []int{nostr.KindGiftWrap}, Limit: 0},
	})

	// Server side: same subscription Serve uses.
	serverEvents := pool.SubMany(ctx, e2eRelays, nostr.Filters{
		{Kinds: []int{nostr.KindGiftWrap}, Limit: 0},
	})

	bridge := &FakeBridge{fn: func(msg CLIMessage) (CLIResponse, error) {
		return CLIResponse{Success: true, Data: map[string]interface{}{"running": true}, Timestamp: time.Now()}, nil
	}}
	authz := NewAuthorizer("") // reads open
	dispatcher := NewDispatcher(authz, bridge, serverPK)

	// Server loop in a goroutine (mirrors Serve, but in-test for assertions).
	type outcome struct {
		resp JSONRPCResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				done <- outcome{err: ctx.Err()}
				return
			case re, ok := <-serverEvents:
				if !ok {
					continue
				}
				evt := re.Event
				pTag := evt.Tags.GetFirst([]string{"p"})
				if pTag == nil || pTag.Value() != serverPK {
					continue
				}
				req, caller, err := UnwrapRequest(*evt, serverSK)
				if err != nil {
					continue
				}
				resp := dispatcher.Dispatch(caller, req)
				respGW, err := WrapResponse(resp, caller, serverSK, nostr.Tag{"e", evt.ID})
				if err != nil {
					continue
				}
				for r := range pool.PublishMany(ctx, e2eRelays, respGW) {
					_ = r
				}
				done <- outcome{resp: resp}
				return
			}
		}
	}()

	// Client: send the request.
	req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "tools/call",
		Params: json.RawMessage(`{"name":"status","arguments":{}}`)}
	gw, _, err := WrapRequest(req, serverPK, clientSK)
	if err != nil {
		t.Fatal(err)
	}
	for r := range pool.PublishMany(ctx, e2eRelays, gw) {
		_ = r
	}

	// Wait for the server's dispatch result.
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("server side: %v", out.err)
		}
		if out.resp.Error != nil {
			t.Fatalf("dispatch error: %+v", out.resp.Error)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("no dispatch within 45s — relay delivery of 1059 failed")
	}

	// Client: receive the response gift wrap.
	deadline := time.After(45 * time.Second)
	for {
		select {
		case re, ok := <-clientEvents:
			if !ok {
				continue
			}
			evt := re.Event
			pTag := evt.Tags.GetFirst([]string{"p"})
			if pTag == nil || pTag.Value() != clientPK {
				// the RESPONSE is wrapped TO the client — p = clientPK, not server
				continue
			}
			{ // addressed to the client; try to unwrap with the client key
				resp, err := UnwrapResponse(*evt, clientSK)
				if err != nil {
					continue // someone else's traffic that happens to match
				}
				if resp.ID != float64(1) {
					continue
				}
				var cliResp CLIResponse
				if err := json.Unmarshal(resp.Result, &cliResp); err != nil {
					t.Fatalf("result: %v", err)
				}
				if !cliResp.Success {
					t.Fatalf("CLIResponse: %+v", cliResp)
				}
				t.Logf("E2E OK: status via relay = %+v", cliResp.Data)
				return
			}
		case <-deadline:
			t.Fatal("response never arrived at the client")
		}
	}
}
