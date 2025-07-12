package tollgate_protocol

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/nbd-wtf/go-nostr"
)

// TollGateAdvertisementKind is the Nostr event kind for TollGate advertisements
const TollGateAdvertisementKind = 10021

// PricingOption represents a pricing option from an advertisement
type PricingOption struct {
	AssetType    string // "cashu"
	PricePerStep uint64 // Price per step in units
	PriceUnit    string // Price unit (e.g., "sat")
	MintURL      string // Accepted mint URL
	MinSteps     uint64 // Minimum steps to purchase
}

// AdvertisementInfo contains all pricing and configuration data extracted from an advertisement
type AdvertisementInfo struct {
	Metric         string          // "milliseconds" or "bytes"
	StepSize       uint64          // Step size from advertisement
	PricingOptions []PricingOption // Available pricing options
	TIPs           []string        // Supported TIP numbers
}

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

// ExtractAdvertisementInfo extracts pricing and configuration information from a TollGate advertisement
func ExtractAdvertisementInfo(event *nostr.Event) (*AdvertisementInfo, error) {
	if event == nil {
		return nil, fmt.Errorf("event is nil")
	}

	if event.Kind != TollGateAdvertisementKind {
		return nil, fmt.Errorf("invalid event kind: %d, expected %d", event.Kind, TollGateAdvertisementKind)
	}

	info := &AdvertisementInfo{
		PricingOptions: []PricingOption{},
		TIPs:           []string{},
	}

	// Extract information from tags
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}

		switch tag[0] {
		case "metric":
			info.Metric = tag[1]

		case "step_size":
			stepSize, err := strconv.ParseUint(tag[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid step_size: %w", err)
			}
			info.StepSize = stepSize

		case "price_per_step":
			// Format: ["price_per_step", "cashu", "210", "sat", "https://mint.url", "1"]
			if len(tag) < 6 {
				continue // Skip malformed price_per_step tags
			}

			pricePerStep, err := strconv.ParseUint(tag[2], 10, 64)
			if err != nil {
				continue // Skip invalid prices
			}

			minSteps, err := strconv.ParseUint(tag[5], 10, 64)
			if err != nil {
				minSteps = 1 // Default to 1 if parsing fails
			}

			pricingOption := PricingOption{
				AssetType:    tag[1],
				PricePerStep: pricePerStep,
				PriceUnit:    tag[3],
				MintURL:      tag[4],
				MinSteps:     minSteps,
			}

			info.PricingOptions = append(info.PricingOptions, pricingOption)

		case "tips":
			// All values after "tips" are TIP numbers
			info.TIPs = append(info.TIPs, tag[1:]...)
		}
	}

	// Validate required fields
	if info.Metric == "" {
		return nil, fmt.Errorf("missing required 'metric' tag")
	}

	if info.StepSize == 0 {
		return nil, fmt.Errorf("missing or invalid 'step_size' tag")
	}

	if len(info.PricingOptions) == 0 {
		return nil, fmt.Errorf("no valid pricing options found")
	}

	return info, nil
}
