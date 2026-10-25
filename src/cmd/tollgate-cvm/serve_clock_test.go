package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip59"
)

// unwrapFresh is the relay loop's gate: unwrap, then decide freshness on the
// request's real clock. #982 fixed the loop to use the rumor's created_at
// instead of the gift wrap's, but nothing pinned that choice — reverting the
// loop to the wrap clock left the whole suite green, because the existing test
// called UnwrapRequest and CheckFreshness itself rather than calling the loop's
// gate. These tests call the gate.

// The regression this file exists for: a request whose WRAP is hours old but
// whose RUMOR is fresh must be ACCEPTED. If the gate reads the wrap clock, this
// fails — which is the #980 D1 bug.
func TestUnwrapFreshAcceptsFreshRumorBehindAnOldWrap(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	clientSK := nostr.GeneratePrivateKey()
	serverPK, err := nostr.GetPublicKey(serverSK)
	if err != nil {
		t.Fatalf("server pk: %v", err)
	}

	payload, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "status"})
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"p", serverPK}},
	}
	gw, err := nip59.GiftWrap(rumor, serverPK, encryptTo(clientSK, serverPK), signWith(clientSK), nil)
	if err != nil {
		t.Fatalf("giftwrap: %v", err)
	}
	// NIP-59 backdates the wrap. 10h is its maximum.
	gw.CreatedAt = nostr.Now() - nostr.Timestamp((10 * time.Hour).Seconds())

	req, caller, err := unwrapFresh(gw, serverSK, time.Now())
	if err != nil {
		t.Fatalf("a fresh request behind a backdated wrap was refused (%v) — the gate is reading the wrap clock, which is #980 D1", err)
	}
	if req.Method != "status" {
		t.Errorf("method = %q, want status", req.Method)
	}
	callerPK, _ := nostr.GetPublicKey(clientSK)
	if caller != callerPK {
		t.Errorf("caller = %q, want the seal author %q", caller, callerPK)
	}
}

// The other direction, kept so the gate is not weakened into accepting
// everything: a stale RUMOR is refused even when the wrap looks fresh.
func TestUnwrapFreshRefusesAStaleRumor(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	clientSK := nostr.GeneratePrivateKey()
	serverPK, _ := nostr.GetPublicKey(serverSK)

	payload, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "status"})
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now() - nostr.Timestamp((time.Hour).Seconds()),
		Tags:      nostr.Tags{nostr.Tag{"p", serverPK}},
	}
	gw, err := nip59.GiftWrap(rumor, serverPK, encryptTo(clientSK, serverPK), signWith(clientSK), nil)
	if err != nil {
		t.Fatalf("giftwrap: %v", err)
	}
	gw.CreatedAt = nostr.Now() // wrap looks fresh; the rumor does not

	if _, _, err := unwrapFresh(gw, serverSK, time.Now()); err == nil {
		t.Error("a one-hour-old rumor was accepted because its wrap looked fresh — the gate is reading the wrap clock")
	}
}

// A wrap addressed to someone else must be refused before freshness matters.
func TestUnwrapFreshRefusesAStrangerWrap(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	clientSK := nostr.GeneratePrivateKey()
	strangerSK := nostr.GeneratePrivateKey()
	strangerPK, _ := nostr.GetPublicKey(strangerSK)

	payload, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: float64(1), Method: "status"})
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"p", strangerPK}},
	}
	gw, err := nip59.GiftWrap(rumor, strangerPK, encryptTo(clientSK, strangerPK), signWith(clientSK), nil)
	if err != nil {
		t.Fatalf("giftwrap: %v", err)
	}

	if _, _, err := unwrapFresh(gw, serverSK, time.Now()); err == nil {
		t.Error("server unwrapped a wrap addressed to a stranger — NIP-44 must refuse")
	}
}
