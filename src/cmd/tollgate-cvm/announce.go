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
//
// THE p TAG CARRIES HEX, NOT BECH32. This was a real defect, caught only by
// publishing to a live relay and subscribing back: the tag was written as
// `npub1…`, and NIP-01 indexes `p` values as 32-byte hex pubkeys, so a
// `#p = <hex>` subscription NEVER matched — the whole discovery mechanism was
// dead on arrival while every unit test passed. The config field stays bech32
// (what an operator reads and types); the tag is hex (what a relay indexes).
// The bech32 spelling stays in the content so a human reading the event sees it.
func BuildAnnouncement(serverSK, maintainerNpub string, relays ...string) (nostr.Event, error) {
	maintainerHex, err := maintainerHexFromNpub(maintainerNpub)
	if err != nil {
		return nostr.Event{}, err
	}

	content := map[string]interface{}{
		"name":  "tollgate-control-plane",
		"about": "Remote management of a TollGate router over ContextVM: status, wallet balance, config, private network, upstreams. Read tools open to any signed caller; write tools maintainer-only.",
		"caps":  []string{"tools"},
	}
	if maintainerNpub != "" {
		content["maintainer_npub"] = maintainerNpub
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
	if maintainerHex != "" {
		// The discovery key, in the form a relay can index (hex).
		tags = append(tags, nostr.Tag{"p", maintainerHex})
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
func BuildToolsAnnouncement(serverSK, maintainerNpub string) (nostr.Event, error) {
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

	// Same p-tag discipline as the 11316 announcement: the tag carries the
	// maintainer pubkey as HEX (the form a relay indexes for #p), omitted —
	// never empty — when the router is unclaimed. Without it the maintainer
	// dashboard's `kinds=11317, #p=<hex>` filter cannot index the tools list
	// at all (#980 F3, found by the #971 E2E).
	maintainerHex, err := maintainerHexFromNpub(maintainerNpub)
	if err != nil {
		return nostr.Event{}, err
	}

	tags := nostr.Tags{nostr.Tag{"d", annDSlug}}
	if maintainerHex != "" {
		tags = append(tags, nostr.Tag{"p", maintainerHex})
	}

	evt := nostr.Event{
		Kind:      annKindTools,
		Content:   string(contentJSON),
		CreatedAt: nostr.Now(),
		Tags:      tags,
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
	toolsEvt, err := BuildToolsAnnouncement(serverSK, maintainerNpub)
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
