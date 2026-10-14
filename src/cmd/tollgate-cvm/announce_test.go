package main

import (
	"encoding/json"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// The announcement is the discovery mechanism: a dashboard subscribes
// kind 11316 with #p = maintainer npub and finds every tollgate the
// maintainer owns. These tests pin the event's shape.

func TestAnnouncementShape(t *testing.T) {
	const maintainer = "npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew"
	serverSK := nostr.GeneratePrivateKey()
	serverPK, _ := nostr.GetPublicKey(serverSK)

	evt, err := BuildAnnouncement(serverSK, maintainer, "relay.example", "test-relay2")
	if err != nil {
		t.Fatalf("BuildAnnouncement: %v", err)
	}

	if evt.Kind != 11316 {
		t.Fatalf("kind %d, want 11316 (CEP-6 server announcement)", evt.Kind)
	}
	if evt.PubKey != serverPK {
		t.Fatalf("announced under %s, want the server key", evt.PubKey)
	}
	if ok, _ := evt.CheckSignature(); !ok {
		t.Fatal("announcement not signed")
	}

	// d tag: replaceable-address stability
	if d := evt.Tags.GetFirst([]string{"d"}); d == nil || d.Value() == "" {
		t.Fatal("no d tag — 11316 is replaceable keyed by (kind, pubkey, d)")
	}

	// THE discovery key: maintainer npub in an indexed p tag
	p := evt.Tags.GetFirst([]string{"p"})
	if p == nil {
		t.Fatal("no p tag — a dashboard can never find this tollgate")
	}
	if p.Value() != maintainer {
		t.Fatalf("p tag %s, want maintainer npub %s", p.Value(), maintainer)
	}

	// t tag: namespaced service class, filterable
	tt := evt.Tags.GetFirst([]string{"t"})
	if tt == nil || tt.Value() != "cvm:service:tollgate-control-plane" {
		t.Fatalf("t tag %v, want cvm:service:tollgate-control-plane", tt)
	}

	// content is valid JSON with name/about
	var content map[string]interface{}
	if err := json.Unmarshal([]byte(evt.Content), &content); err != nil {
		t.Fatalf("content not json: %v", err)
	}
	if content["name"] != "tollgate-control-plane" {
		t.Fatalf("content name %v", content["name"])
	}
}

// Tools announcement (11317): generated from the surface, and the banned
// tools must be structurally unable to appear.
func TestToolsAnnouncement(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()

	evt, err := BuildToolsAnnouncement(serverSK)
	if err != nil {
		t.Fatalf("BuildToolsAnnouncement: %v", err)
	}
	if evt.Kind != 11317 {
		t.Fatalf("kind %d, want 11317", evt.Kind)
	}
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(evt.Content), &tools); err != nil {
		t.Fatalf("content not json: %v", err)
	}
	if len(tools.Tools) != len(ToolSurface()) {
		t.Fatalf("%d tools announced, surface has %d", len(tools.Tools), len(ToolSurface()))
	}
	for _, tool := range tools.Tools {
		if tool.Name == "drain" || tool.Name == "fund" {
			t.Fatalf("banned tool %q announced", tool.Name)
		}
	}
}

// An unclaimed router must NOT announce a maintainer p tag — an empty p tag
// would be a wildcard a dashboard could match on some relays.
func TestAnnouncementUnclaimed(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	evt, err := BuildAnnouncement(serverSK, "", "relay.example")
	if err == nil {
		p := evt.Tags.GetFirst([]string{"p"})
		if p != nil {
			t.Fatalf("unclaimed router announced p tag %q — this puts it in someone's fleet view", p.Value())
		}
	}
	// err != nil (refusing to announce unclaimed) is also acceptable: log loud
}
