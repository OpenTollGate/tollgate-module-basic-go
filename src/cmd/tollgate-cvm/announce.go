package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// CEP-6 announcements (ADR D4-D5). The kind-11316 event IS the discovery
// mechanism: a maintainer dashboard subscribes with `#p = <maintainer npub>`
// and every tollgate that maintainer owns appears — no registry, no curator.
// 11316/11317 are replaceable (keyed by kind, pubkey, d), and because they
// are regular kinds — not gift wraps — relays honour #p on them.

const (
	annKindServer = 11316
	annKindTools  = 11317
	annDSlug      = "tollgate-control-plane"
	annClassTag   = "cvm:service:tollgate-control-plane"
)

// BuildAnnouncement creates the signed kind-11316 server announcement.
// maintainerNpub may be empty (unclaimed): the event is still announced —
// the service exists — but carries NO p tag, because an empty p tag could be
// matched as a wildcard by some relay implementations.
func BuildAnnouncement(serverSK, maintainerNpub string, relays ...string) (nostr.Event, error) {
	content := map[string]interface{}{
		"name":  "tollgate-control-plane",
		"about": "Remote management of a TollGate router over ContextVM: status, wallet balance, config, private network, upstreams. Read tools open to any signed caller; write tools maintainer-only.",
		"caps":  []string{"tools"},
	}
	if len(relays) > 0 {
		content["relays"] = relays
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nostr.Event{}, err
	}

	tags := nostr.Tags{
		nostr.Tag{"d", annDSlug},
		nostr.Tag{"t", annClassTag},
	}
	if maintainerNpub != "" {
		// The discovery key. This single tag is what makes fleet view possible.
		tags = append(tags, nostr.Tag{"p", maintainerNpub})
	}

	evt := nostr.Event{
		Kind:      annKindServer,
		Content:   string(contentJSON),
		CreatedAt: nostr.Now(),
		Tags:      tags,
	}
	if err := evt.Sign(serverSK); err != nil {
		return nostr.Event{}, fmt.Errorf("sign announcement: %w", err)
	}
	return evt, nil
}

// BuildToolsAnnouncement creates the signed kind-11317 tools-list
// announcement, generated from ToolSurface() so the announced surface and the
// dispatchable surface can never drift apart.
func BuildToolsAnnouncement(serverSK string) (nostr.Event, error) {
	tools := make([]map[string]interface{}, 0, len(ToolSurface()))
	for _, tool := range ToolSurface() {
		entry := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if tool.Write {
			// CEP-8 explicit gating: state-changing tools say so up front.
			entry["gating"] = "explicit"
		}
		tools = append(tools, entry)
	}
	contentJSON, err := json.Marshal(map[string]interface{}{"tools": tools})
	if err != nil {
		return nostr.Event{}, err
	}

	evt := nostr.Event{
		Kind:      annKindTools,
		Content:   string(contentJSON),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"d", annDSlug}},
	}
	if err := evt.Sign(serverSK); err != nil {
		return nostr.Event{}, fmt.Errorf("sign tools announcement: %w", err)
	}
	return evt, nil
}

// PublishAnnouncements signs and publishes both announcements to relays.
// Replaceable kinds mean republishing replaces, not duplicates.
func PublishAnnouncements(pool *nostr.SimplePool, relays []string, serverSK, maintainerNpub string) error {
	evt, err := BuildAnnouncement(serverSK, maintainerNpub, relays...)
	if err != nil {
		return err
	}
	toolsEvt, err := BuildToolsAnnouncement(serverSK)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for result := range pool.PublishMany(ctx, relays, evt) {
		_ = result
	}
	for result := range pool.PublishMany(ctx, relays, toolsEvt) {
		_ = result
	}
	return nil
}
