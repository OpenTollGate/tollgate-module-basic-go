package merchant

import (
	"testing"
)

// And the defence-in-depth assertion: even if a caller does hand over something
// MAC-shaped, the notice that reaches the wire must carry no p tag. This is the
// choke point's guarantee, asserted at the same level as the invariant it
// protects, so the two layers are covered independently.
func TestNoticeEventChokePointDropsAMACShapedRecipient(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	evt, err := deg.CreateNoticeEvent("error", "probe", "message", "82:49:5a:95:5e:53")
	if err != nil {
		t.Fatalf("CreateNoticeEvent: %v", err)
	}
	for _, tag := range evt.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			t.Fatalf("a MAC-shaped recipient produced a p tag (%q) — the choke point must drop it (#950)", tag[1])
		}
	}
}

// A real pubkey must still be honoured — otherwise the fix above could be
// "achieved" by dropping every p tag, which would break legitimate notices.
func TestNoticeEventChokePointKeepsARealPubkey(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	const realPubkey = "70208b3e272936bb31cd7849beb6b673e18b75ee0cdbd0822f77386f5db0156a"
	evt, err := deg.CreateNoticeEvent("error", "probe", "message", realPubkey)
	if err != nil {
		t.Fatalf("CreateNoticeEvent: %v", err)
	}
	for _, tag := range evt.Tags {
		if len(tag) >= 2 && tag[0] == "p" && tag[1] == realPubkey {
			return
		}
	}
	t.Fatalf("a valid Nostr pubkey lost its p tag: %v", evt.Tags)
}
