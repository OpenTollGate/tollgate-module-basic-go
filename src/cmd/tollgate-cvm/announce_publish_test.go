package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// #980 F5: the announce path used to drain PublishMany with `_ = result`
// and always return nil, so announcing into a DEAD relay still logged
// "announced 11316/11317" (found live by the #971 E2E). These tests pin the
// policy through the publishReporter seam: refusals are surfaced, zero
// acceptance is an error, and only real acceptance counts as announced.

func fakePub(accepted int, failures []relayFailure) publishReporter {
	return func(ctx context.Context, relays []string, evt nostr.Event) (int, []relayFailure) {
		return accepted, failures
	}
}

func TestAnnounceZeroAcceptanceIsAnError(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	err := publishAnnouncements(
		fakePub(0, []relayFailure{{url: "ws://dead.example", err: errors.New("connection refused")}}),
		[]string{"ws://dead.example"}, serverSK, "",
	)
	if err == nil {
		t.Fatal("zero-acceptance announce returned nil — the daemon would log 'announced' into a dead relay again")
	}
	if !strings.Contains(err.Error(), "accepted by 0 of 1") || !strings.Contains(err.Error(), "11316") {
		t.Fatalf("error must name the kind and the 0-of-N verdict, got: %v", err)
	}
}

func TestAnnouncePartialAcceptanceIsNotAnError(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	err := publishAnnouncements(
		fakePub(1, []relayFailure{{url: "ws://flaky.example", err: errors.New("timeout")}}),
		[]string{"ws://ok.example", "ws://flaky.example"}, serverSK, "",
	)
	if err != nil {
		t.Fatalf("one relay of two accepting must not fail the announce (the event landed): %v", err)
	}
}

func TestAnnounceNoRelaysConfiguredIsAnError(t *testing.T) {
	serverSK := nostr.GeneratePrivateKey()
	err := publishAnnouncements(fakePub(0, nil), nil, serverSK, "")
	if err == nil {
		t.Fatal("publishing to zero relays must be an error, not a silent no-op success")
	}
}
