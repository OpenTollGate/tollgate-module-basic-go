package relay

import (
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestPrivateRelay(t *testing.T) {
	// Create a new private relay
	pr := NewPrivateRelay()

	// Test that the relay is initialized correctly
	if pr.GetEventCount() != 0 {
		t.Errorf("Expected empty relay, got %d events", pr.GetEventCount())
	}

	// Create test events for each TollGate kind
	testEvents := []*nostr.Event{
		// Payment event (kind 21000)
		{
			ID:        "test_payment_id",
			PubKey:    "test_pubkey_customer",
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Kind:      21000,
			Tags: nostr.Tags{
				{"p", "test_pubkey_tollgate"},
				{"device-identifier", "mac", "00:1A:2B:3C:4D:5E"},
				{"payment", "cashuB..."},
			},
			Content: "",
			Sig:     "test_signature",
		},
		// TollGate Discovery event (kind 10021)
		{
			ID:        "test_discovery_id",
			PubKey:    "test_pubkey_tollgate",
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Kind:      10021,
			Tags: nostr.Tags{
				{"metric", "milliseconds"},
				{"step_size", "60000"},
				{"tips", "1", "2", "3"},
			},
			Content: "",
			Sig:     "test_signature",
		},
		// Session event (kind 1022)
		{
			ID:        "test_session_id",
			PubKey:    "test_pubkey_tollgate",
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Kind:      1022,
			Tags: nostr.Tags{
				{"p", "test_pubkey_customer"},
				{"device-identifier", "mac", "00:1A:2B:3C:4D:5E"},
				{"purchased_steps", "10"},
			},
			Content: "",
			Sig:     "test_signature",
		},
		// Notice event (kind 21023)
		{
			ID:        "test_notice_id",
			PubKey:    "test_pubkey_tollgate",
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Kind:      21023,
			Tags: nostr.Tags{
				{"p", "test_pubkey_customer"},
				{"level", "error"},
				{"code", "payment-error"},
			},
			Content: "Payment processing failed",
			Sig:     "test_signature",
		},
	}

	// Test storing valid TollGate events
	for _, event := range testEvents {
		err := pr.PublishEvent(event)
		if err == nil {
			t.Errorf("Expected signature validation error, but got nil for event %s", event.ID)
		}
	}

	// Test rejecting invalid kinds
	invalidEvent := &nostr.Event{
		ID:        "test_invalid_id",
		PubKey:    "test_pubkey",
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      1, // Invalid kind for TollGate
		Tags:      nostr.Tags{},
		Content:   "This should be rejected",
		Sig:       "test_signature",
	}

	// Test kind validation
	reject, msg := pr.validateTollGateKind(nil, invalidEvent)
	if !reject {
		t.Errorf("Expected invalid event to be rejected")
	}
	if msg == "" {
		t.Errorf("Expected rejection message for invalid kind")
	}

	// Test valid kind validation
	reject, msg = pr.validateTollGateKind(nil, testEvents[0])
	if reject {
		t.Errorf("Expected valid TollGate event to be accepted, got: %s", msg)
	}

	// Test clearing the relay
	pr.Clear()
	if pr.GetEventCount() != 0 {
		t.Errorf("Expected relay to be empty after clear, got %d events", pr.GetEventCount())
	}
}

func TestTollGateKinds(t *testing.T) {
	// Test that all expected TollGate kinds are defined
	expectedKinds := []int{21000, 10021, 1022, 21023}

	for _, kind := range expectedKinds {
		if !TollGateKinds[kind] {
			t.Errorf("Expected TollGate kind %d to be defined", kind)
		}
	}

	// Test that invalid kinds are not defined
	invalidKinds := []int{1, 2, 3, 4, 5, 1000, 20000, 22000, 21021, 21022}

	for _, kind := range invalidKinds {
		if TollGateKinds[kind] {
			t.Errorf("Expected kind %d to not be a TollGate kind", kind)
		}
	}
}

func TestQueryFiltering(t *testing.T) {
	pr := NewPrivateRelay()

	// Create test events with different pubkeys
	event1 := &nostr.Event{
		ID:        "event1",
		PubKey:    "pubkey1",
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      21000,
		Tags:      nostr.Tags{},
		Content:   "",
		Sig:       "sig1",
	}

	event2 := &nostr.Event{
		ID:        "event2",
		PubKey:    "pubkey2",
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      10021,
		Tags:      nostr.Tags{},
		Content:   "",
		Sig:       "sig2",
	}

	// Store events directly (bypassing signature validation for test)
	pr.storeEvent(nil, event1)
	pr.storeEvent(nil, event2)

	// Test filtering by pubkey
	filter := nostr.Filter{
		Authors: []string{"pubkey1"},
	}

	events, err := pr.QueryEvents(filter)
	if err != nil {
		t.Errorf("Error querying events: %v", err)
	}

	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}

	if events[0].ID != "event1" {
		t.Errorf("Expected event1, got %s", events[0].ID)
	}

	// Test filtering by kind
	filter = nostr.Filter{
		Kinds: []int{10021},
	}

	events, err = pr.QueryEvents(filter)
	if err != nil {
		t.Errorf("Error querying events: %v", err)
	}

	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}

	if events[0].ID != "event2" {
		t.Errorf("Expected event2, got %s", events[0].ID)
	}
}
