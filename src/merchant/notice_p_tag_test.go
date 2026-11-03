package merchant

import (
	"strings"
	"testing"
)

// TestNoticeEventOmitsPTagOnPaymentErrorPaths pins the #950 invariant on the
// wire: a kind-21023 notice emitted for a raw-token-flow customer carries no
// p tag (there is no customer Nostr identity to cite), and a p tag is only
// ever emitted for a real Nostr pubkey — never a device identifier or MAC
// (tollgate#26). The MAC-in-p shape was live-captured on the lab rig
// (2026-10-16, degraded service-unavailable refusal) and is the exact wire
// bug this test makes impossible to reintroduce silently.
func TestNoticeEventOmitsPTagOnPaymentErrorPaths(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	event, err := deg.PurchaseSession("cashuAanything", "82:49:5a:95:5e:53")
	if err != nil {
		t.Fatalf("degraded PurchaseSession: %v", err)
	}
	for _, tag := range event.Tags {
		if tag[0] == "p" {
			t.Fatalf("degraded refusal notice carries a p tag (%q) — raw-token customers have no Nostr identity; the MAC belongs nowhere near p (#950)", tag[1])
		}
	}
	foundLevel, foundCode := false, false
	for _, tag := range event.Tags {
		if tag[0] == "level" && tag[1] == "error" {
			foundLevel = true
		}
		if tag[0] == "code" && tag[1] == "service-unavailable" {
			foundCode = true
		}
	}
	if !foundLevel || !foundCode {
		t.Fatalf("notice missing level/code tags: %v", event.Tags)
	}
	if !strings.Contains(event.Content, "try again") {
		t.Fatalf("notice content lost its message: %q", event.Content)
	}
}

// TestNoticeEventDropsNonPubkeyRecipient guards the choke point: any future
// caller that passes a device identifier (or any non-pubkey string) as
// customerPubkey gets the tag dropped, not a spec-invalid envelope.
func TestNoticeEventDropsNonPubkeyRecipient(t *testing.T) {
	_, cm := newDegradedMerchantWithConfig(t)

	event, err := createNoticeEvent(cm, "error", "payment-error-token-spent", "spent", "82:49:5a:95:5e:53")
	if err != nil {
		t.Fatalf("createNoticeEvent: %v", err)
	}
	for _, tag := range event.Tags {
		if tag[0] == "p" {
			t.Fatalf("non-pubkey recipient emitted in p (%q) — the guard must drop it (#950)", tag[1])
		}
	}

	event, err = createNoticeEvent(cm, "error", "payment-error-token-spent", "spent", strings.Repeat("a", 63))
	if err != nil {
		t.Fatalf("createNoticeEvent short hex: %v", err)
	}
	for _, tag := range event.Tags {
		if tag[0] == "p" {
			t.Fatalf("short hex string emitted in p (%d chars) — only 64-hex pubkeys are legal", len(tag[1]))
		}
	}
}

// TestNoticeEventEmitsPubkeyRecipient pins the positive branch: a real Nostr
// pubkey (the shape a future customer-identity interface would deliver) is
// still honored.
func TestNoticeEventEmitsPubkeyRecipient(t *testing.T) {
	_, cm := newDegradedMerchantWithConfig(t)
	pubkey := strings.Repeat("ab", 32)

	event, err := createNoticeEvent(cm, "error", "session-error", "msg", pubkey)
	if err != nil {
		t.Fatalf("createNoticeEvent: %v", err)
	}
	var got string
	for _, tag := range event.Tags {
		if tag[0] == "p" {
			got = tag[1]
		}
	}
	if got != pubkey {
		t.Fatalf("pubkey recipient not honored: p=%q want %q", got, pubkey)
	}
}
