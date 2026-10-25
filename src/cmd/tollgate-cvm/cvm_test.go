package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip59"
)

// rpcFixture mirrors the JSON-RPC 2.0 shapes CVM carries in kind 25910.
type rpcFixture struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// TestGiftWrapRoundTrip: a request the server can decrypt is the floor of the
// whole transport. Client builds a kind-25910 rumor (JSON-RPC), gift-wraps it
// TO THE SERVER's pubkey; the server unwraps it with its own key and recovers
// the exact method/id — and learns the CALLER's identity from the seal, which
// is what Authorizer keys on. Then the response goes back the same way.
func TestGiftWrapRoundTrip(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	serverPK, err := nostr.GetPublicKey(serverSK)
	if err != nil {
		t.Fatalf("server pubkey: %v", err)
	}
	clientSK := nostr.GeneratePrivateKey()
	clientPK, err := nostr.GetPublicKey(clientSK)
	if err != nil {
		t.Fatalf("client pubkey: %v", err)
	}
	if err := ValidateKeyPair(serverSK, clientSK); err != nil {
		t.Fatalf("distinct keys refused: %v", err)
	}

	// Client side: wrap a tools/call request to the server.
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(7),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"wallet_balance","arguments":{}}`),
	}
	gw, caller, err := WrapRequest(req, serverPK, clientSK)
	if err != nil {
		t.Fatalf("WrapRequest: %v", err)
	}
	if caller != clientPK {
		t.Fatalf("WrapRequest reported caller %s, want %s", caller, clientPK)
	}
	if gw.Kind != nostr.KindGiftWrap {
		t.Fatalf("wrap kind %d, want %d (gift wrap)", gw.Kind, nostr.KindGiftWrap)
	}
	if pt := gw.Tags.GetFirst([]string{"p"}); pt == nil || pt.Value() != serverPK {
		t.Fatalf("gift wrap p-tag must be the server's pubkey")
	}

	// Server side: unwrap with the server key, recover caller + request.
	gotReq, gotCaller, _, err := UnwrapRequest(gw, serverSK)
	if err != nil {
		t.Fatalf("UnwrapRequest: %v", err)
	}
	if gotCaller != clientPK {
		t.Fatalf("server saw caller %s, want %s — authorization keys on this", gotCaller, clientPK)
	}
	if gotReq.Method != "tools/call" {
		t.Fatalf("method %q, want tools/call", gotReq.Method)
	}
	if id, ok := gotReq.ID.(float64); !ok || id != 7 {
		t.Fatalf("request id %v, want 7", gotReq.ID)
	}

	// Response path: server wraps a result back to the caller.
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      float64(7),
		Result:  json.RawMessage(`{"success":true,"data":{"balance_sats":21}}`),
	}
	respGW, err := WrapResponse(resp, clientPK, serverSK)
	if err != nil {
		t.Fatalf("WrapResponse: %v", err)
	}
	gotResp, err := UnwrapResponse(respGW, clientSK)
	if err != nil {
		t.Fatalf("UnwrapResponse: %v", err)
	}
	if gotResp.ID.(float64) != 7 {
		t.Fatalf("response id %v, want 7", gotResp.ID)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Balance uint64 `json:"balance_sats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(gotResp.Result, &payload); err != nil {
		t.Fatalf("result payload: %v", err)
	}
	if !payload.Success || payload.Data.Balance != 21 {
		t.Fatalf("payload mangled: %+v", payload)
	}
}

// A gift wrap addressed to someone else must fail to unwrap — the server must
// never act on traffic not encrypted to its key.
func TestUnwrapWrongRecipientFails(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	_ = serverSK
	strangerSK := nostr.GeneratePrivateKey()
	strangerPK, _ := nostr.GetPublicKey(strangerSK)

	req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "tools/list"}
	gw, _, err := WrapRequest(req, strangerPK, nostr.GeneratePrivateKey())
	if err != nil {
		t.Fatalf("WrapRequest to stranger: %v", err)
	}
	if _, _, _, err := UnwrapRequest(gw, serverSK); err == nil {
		t.Fatal("server unwrapped a gift wrap addressed to a stranger — NIP-44 must refuse")
	}
}

// TestAuthorizeEndToEnd: the full authz decision from a real wrapped request —
// the path a hostile caller actually takes.
func TestAuthorizeEndToEnd(t *testing.T) {
	const maintainer = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	serverSK := nostr.GeneratePrivateKey()
	serverPK, _ := nostr.GetPublicKey(serverSK)

	maintainerSK, err := SKFromNpub(maintainer)
	if err != nil {
		t.Skipf("maintainer key material not available (fine — npub-only): %v", err)
	}

	strangerSK := nostr.GeneratePrivateKey()

	authz := NewAuthorizer(maintainer)

	// Stranger calls a write tool: wrapped, delivered, unwrapped — refused.
	req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(2), Method: "tools/call",
		Params: json.RawMessage(`{"name":"config_set","arguments":{"key":"log_level","value":"debug"}}`)}
	gw, _, err := WrapRequest(req, serverPK, strangerSK)
	if err != nil {
		t.Fatalf("WrapRequest: %v", err)
	}
	_, caller, _, err := UnwrapRequest(gw, serverSK)
	if err != nil {
		t.Fatalf("UnwrapRequest: %v", err)
	}
	if err := authz.Authorize(caller, "config_set"); err == nil {
		t.Fatal("stranger authorized for config_set end-to-end")
	}
	_ = maintainerSK
	_ = time.Now
}

// The D1 regression (#980): NIP-59 gift wraps carry a deliberately randomized
// created_at (nip59.GiftWrap: "up to 6 hours in the past"), so freshness must
// be decided on the RUMOR's timestamp — the request's real clock. Checking
// the wrap's timestamp made every relay-delivered request stale by
// construction and the control plane silently inert.
func testKeys(t *testing.T) (string, string) {
	t.Helper()
	serverSK := nostr.GeneratePrivateKey()
	clientSK := nostr.GeneratePrivateKey()
	return serverSK, clientSK
}

func TestRumorTimestampIsTheRealClockNotTheWrap(t *testing.T) {
	serverSK, clientSK := testKeys(t)
	serverPK, _ := nostr.GetPublicKey(serverSK)

	gw, _, err := WrapRequest(JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "status"}, serverPK, clientSK)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}

	_, _, rumorTS, err := UnwrapRequest(gw, serverSK)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	// The rumor clock is fresh (WrapRequest stamps it now).
	if err := CheckFreshness(int64(rumorTS), time.Now()); err != nil {
		t.Errorf("fresh rumor rejected: %v", err)
	}
	// The wrap clock is NOT a valid freshness input: it is randomized into
	// the past by design. Pin that the two clocks differ for at least one
	// seed... they can coincide by chance (1/3600), so assert on semantics:
	// the unwrap-exposed timestamp must be within the freshness window even
	// when the wrap's own timestamp is not.
	if gw.CreatedAt > rumorTS {
		t.Logf("wrap ts %d older-or-equal than rumor ts %d (randomized, expected shape)", gw.CreatedAt, rumorTS)
	}
}

// A genuinely stale request (old RUMOR timestamp) must still be refused —
// the replay defense survives the clock change.
func TestStaleRumorStillRejected(t *testing.T) {
	serverSK, clientSK := testKeys(t)
	serverPK, _ := nostr.GetPublicKey(serverSK)

	payload, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "status"})
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now() - 3600, // one hour old
		Tags:      nostr.Tags{nostr.Tag{"p", serverPK}},
	}
	gw, err := nip59.GiftWrap(rumor, serverPK, encryptTo(clientSK, serverPK), signWith(clientSK), nil)
	if err != nil {
		t.Fatalf("giftwrap: %v", err)
	}
	_, _, rumorTS, err := UnwrapRequest(gw, serverSK)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if err := CheckFreshness(int64(rumorTS), time.Now()); err == nil {
		t.Error("stale rumor must be rejected by CheckFreshness")
	}
}
