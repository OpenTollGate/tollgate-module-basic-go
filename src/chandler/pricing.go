package chandler

import (
	"fmt"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
)

// SelectBestPricingOption selects the best pricing option based on criteria
// For now, it just returns the option with the lowest price per step
func SelectBestPricingOption(options []tollgate_protocol.PricingOption, preferredUnit string) *tollgate_protocol.PricingOption {
	if len(options) == 0 {
		return nil
	}

	var best *tollgate_protocol.PricingOption

	// First, try to find options with preferred unit
	if preferredUnit != "" {
		for i := range options {
			if options[i].PriceUnit == preferredUnit {
				if best == nil || options[i].PricePerStep < best.PricePerStep {
					best = &options[i]
				}
			}
		}
	}

	// If no preferred unit option found, select cheapest overall
	if best == nil {
		best = &options[0]
		for i := range options {
			if options[i].PricePerStep < best.PricePerStep {
				best = &options[i]
			}
		}
	}

	return best
}

// ValidateBudgetConstraints checks if a payment proposal is within budget
func ValidateBudgetConstraints(proposal *PaymentProposal, maxPricePerMs, maxPricePerByte float64, metric string, stepSize uint64) error {
	if proposal == nil || proposal.PricingOption == nil {
		return &ChandlerError{
			Type:    ErrorTypeBudget,
			Code:    "invalid-proposal",
			Message: "proposal or pricing option is nil",
		}
	}

	pricePerStep := float64(proposal.PricingOption.PricePerStep)

	// Calculate price per unit of metric (millisecond or byte)
	pricePerUnit := pricePerStep / float64(stepSize)
	var maxPrice float64
	var unitName string

	switch metric {
	case "milliseconds":
		maxPrice = maxPricePerMs
		unitName = "millisecond"
	case "bytes":
		maxPrice = maxPricePerByte
		unitName = "byte"
	default:
		return &ChandlerError{
			Type:    ErrorTypeBudget,
			Code:    "unsupported-metric",
			Message: "unsupported metric: " + metric,
		}
	}

	if pricePerUnit > maxPrice {
		return &ChandlerError{
			Type:    ErrorTypeBudget,
			Code:    "price-too-high",
			Message: fmt.Sprintf("price per %s %.6f exceeds maximum %.6f (price per step: %.6f, step size: %d)", unitName, pricePerUnit, maxPrice, pricePerStep, stepSize),
			Context: map[string]interface{}{
				"price_per_step": pricePerStep,
				"price_per_unit": pricePerUnit,
				"max_price":      maxPrice,
				"metric":         metric,
				"step_size":      stepSize,
			},
		}
	}

	return nil
}

// CalculateAllotment calculates the allotment from steps and step size
func CalculateAllotment(steps uint64, stepSize uint64) uint64 {
	return steps * stepSize
}

// CalculateStepsFromBudget calculates how many steps can be purchased with given budget
func CalculateStepsFromBudget(budget uint64, pricePerStep uint64, minSteps uint64) uint64 {
	if pricePerStep == 0 {
		return 0
	}

	steps := budget / pricePerStep
	if steps < minSteps {
		return 0 // Cannot afford minimum purchase
	}

	return steps
}

// ValidateTrustPolicy checks if an upstream pubkey is trusted
func ValidateTrustPolicy(pubkey string, allowlist, blocklist []string, defaultPolicy string) error {
	// Check blocklist first
	for _, blocked := range blocklist {
		if pubkey == blocked {
			return &ChandlerError{
				Type:           ErrorTypeTrust,
				Code:           "pubkey-blocked",
				Message:        "pubkey is in blocklist",
				UpstreamPubkey: pubkey,
			}
		}
	}

	// If allowlist is specified, pubkey must be in it
	if len(allowlist) > 0 {
		for _, allowed := range allowlist {
			if pubkey == allowed {
				return nil // Found in allowlist
			}
		}
		return &ChandlerError{
			Type:           ErrorTypeTrust,
			Code:           "pubkey-not-allowed",
			Message:        "pubkey not in allowlist",
			UpstreamPubkey: pubkey,
		}
	}

	// Apply default policy
	switch defaultPolicy {
	case "trust_all":
		return nil
	case "trust_none":
		return &ChandlerError{
			Type:           ErrorTypeTrust,
			Code:           "default-policy-deny",
			Message:        "default policy is trust_none",
			UpstreamPubkey: pubkey,
		}
	default:
		return &ChandlerError{
			Type:           ErrorTypeTrust,
			Code:           "invalid-default-policy",
			Message:        "unknown default policy: " + defaultPolicy,
			UpstreamPubkey: pubkey,
		}
	}
}
