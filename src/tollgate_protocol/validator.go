package tollgate_protocol

import (
	"encoding/json"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
)

// TollGateAdvertisementKind is the Nostr event kind for TollGate advertisements
const TollGateAdvertisementKind = 10021

// ValidateAdvertisement validates a TollGate advertisement
// It checks if the nostr event is properly signed and has the correct kind
func ValidateAdvertisement(event *nostr.Event) error {
	if event == nil {
		return fmt.Errorf("event is nil")
	}

	// Check if it's a TollGate advertisement kind
	if event.Kind != TollGateAdvertisementKind {
		return fmt.Errorf("invalid event kind: %d, expected %d", event.Kind, TollGateAdvertisementKind)
	}

	// Verify the event signature
	ok, err := event.CheckSignature()
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// ParseAdvertisementFromBytes parses a TollGate advertisement from raw bytes
func ParseAdvertisementFromBytes(data []byte) (*nostr.Event, error) {
	var event nostr.Event
	err := json.Unmarshal(data, &event)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nostr event: %w", err)
	}

	return &event, nil
}

// ValidateAdvertisementFromBytes validates a TollGate advertisement from raw bytes
func ValidateAdvertisementFromBytes(data []byte) (*nostr.Event, error) {
	event, err := ParseAdvertisementFromBytes(data)
	if err != nil {
		return nil, err
	}

	err = ValidateAdvertisement(event)
	if err != nil {
		return nil, err
	}

	return event, nil
}
