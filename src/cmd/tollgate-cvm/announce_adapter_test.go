package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// The #986 policy tests inject fakePub, which pins publishAnnouncements and is
// the right seam for the DECISION table. But the adapter that production wires
// — publishToRelays, which drains a real SimplePool into that verdict — had no
// test, and mutating it away left the suite green:
//
//   - removing the drain (`for range … {}`, keeping accepted=0) passes, because
//     no test ever calls publishToRelays;
//   - so does checking only the first event in the loop, since fakePub returns
//     the same verdict for every event.
//
// Both are the #980 F5 failure mode in a different dress: results collected and
// then thrown away, so "announced" is logged for an event no relay took. The
// drain is the one line the whole fix depends on, so it gets a test that uses a
// real SimplePool rather than a fake.

// An unreachable relay must be reported as a refusal, not swallowed. This is
// the assertion that fails if the drain disappears: an undrained channel leaves
// accepted=0 and failed=nil, which publishAnnouncements reads as "0 of 0" —
// indistinguishable from a clean run with no failures to report.
func TestPublishToRelaysSurfacesAnUnreachableRelay(t *testing.T) {
	pool := nostr.NewSimplePool(context.Background())
	evt := nostr.Event{Kind: 11316, Content: "probe", CreatedAt: nostr.Now()}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Port 1 on loopback: refuses immediately, no network, no DNS.
	accepted, failed := publishToRelays(pool)(ctx, []string{"ws://127.0.0.1:1"}, evt)

	if accepted != 0 {
		t.Errorf("accepted = %d, want 0 for an unreachable relay", accepted)
	}
	if len(failed) != 1 {
		t.Fatalf("failed = %d, want 1 — the adapter must surface the refusal (an undrained channel reports 0)", len(failed))
	}
	if failed[0].url != "ws://127.0.0.1:1" {
		t.Errorf("failed[0].url = %q, want the relay we asked", failed[0].url)
	}
	if failed[0].err == nil {
		t.Error("failed[0].err = nil, want the connection error")
	}
}

// Every configured relay must produce exactly one verdict. A batched drain that
// drops results would under-report failures and could turn a partial outage into
// a clean-looking zero.
func TestPublishToRelaysReportsOneVerdictPerRelay(t *testing.T) {
	pool := nostr.NewSimplePool(context.Background())
	evt := nostr.Event{Kind: 11316, Content: "probe", CreatedAt: nostr.Now()}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Three distinct unreachable endpoints.
	relays := []string{"ws://127.0.0.1:1", "ws://127.0.0.1:2", "ws://127.0.0.1:3"}
	accepted, failed := publishToRelays(pool)(ctx, relays, evt)

	if accepted != 0 {
		t.Errorf("accepted = %d, want 0", accepted)
	}
	if len(failed) != len(relays) {
		t.Fatalf("failed = %d, want %d — one verdict per configured relay", len(failed), len(relays))
	}
	seen := map[string]bool{}
	for _, f := range failed {
		seen[f.url] = true
	}
	for _, r := range relays {
		if !seen[r] {
			t.Errorf("no verdict for %s — a dropped result under-reports the outage", r)
		}
	}
}

// policyErrorFacts is a tiny helper so the policy tests below assert on wording
// once, not three times.
func policyErrorFacts(err error) (kind string, zeroOf string) {
	if err == nil {
		return "", ""
	}
	msg := err.Error()
	if i := strings.Index(msg, "kind "); i >= 0 {
		rest := msg[i+len("kind "):]
		if j := strings.IndexAny(rest, " 	"); j >= 0 {
			kind = rest[:j]
		} else {
			kind = rest
		}
	}
	if i := strings.Index(msg, "accepted by 0 of "); i >= 0 {
		zeroOf = msg[i:]
	}
	return kind, zeroOf
}

// The loop must vet BOTH announcements. With the injected fake returning one
// verdict for every call, a loop that only checks the first event passes every
// policy test — but a dead relay would then be reported for 11316 while 11317
// failed silently, which is exactly what the #971 E2E caught.
//
// Counting calls is what pins the loop: it asserts the loop reached the second
// event at all.
func TestAnnounceChecksBothEvents(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()

	var calls int
	var kinds []int
	recording := func(ctx context.Context, relays []string, evt nostr.Event) (int, []relayFailure) {
		calls++
		kinds = append(kinds, evt.Kind)
		// First call accepts, so the failure must come from the SECOND event
		// being zero-accepted — proving the loop actually checked it.
		if calls == 1 {
			return len(relays), nil
		}
		return 0, []relayFailure{{url: "ws://dead.example", err: errors.New("relay is gone")}}
	}

	err := publishAnnouncements(recording, []string{"ws://ok.example"}, serverSK, "")
	if calls != 2 {
		t.Fatalf("adapter called %d time(s), want 2 — both announcements must be vetted", calls)
	}
	if err == nil {
		t.Fatal("the tools announcement was accepted by 0 relays but the announce returned nil")
	}
	kind, zeroOf := policyErrorFacts(err)
	if kind != "11317" {
		t.Errorf("error names kind %s, want 11317 (the event that actually failed): %v", kind, err)
	}
	if zeroOf == "" {
		t.Errorf("error must carry the 0-of-N verdict: %v", err)
	}
	if len(kinds) != 2 {
		t.Fatalf("distinct kinds seen = %v, want two events", kinds)
	}
}
