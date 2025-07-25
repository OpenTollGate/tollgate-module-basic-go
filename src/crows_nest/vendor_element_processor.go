// Package crows_nest implements the VendorElementProcessor for handling Bitcoin/Nostr vendor elements.
package crows_nest

import (
	"log"
	"strings"
)

/*
const (
	bitcoinOUI = "00:11:22" // Example OUI for Bitcoin
	nostrOUI   = "00:33:44" // Example OUI for Nostr
)
*/

// VendorElementProcessor handles Bitcoin/Nostr related vendor elements.
type VendorElementProcessor struct {
	log       *log.Logger
	connector *Connector
}

// ExtractAndScore extracts vendor elements from NetworkInfo and calculates a score.
func (v *VendorElementProcessor) ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error) {
	// Temporarily bypass vendor element parsing as per user request
	// rawIEs := ni.RawIEs
	vendorElements := make(map[string]interface{}) // Initialize as empty for now
	/*
		vendorElements, err := v.parseVendorElements(rawIEs)
		if err != nil {
			v.log.Printf("[crows_nest] ERROR: Failed to parse vendor elements: %v", err)
			return nil, 0, err
		}
	*/

	score := v.calculateScore(ni.Signal, ni.SSID, vendorElements) // Pass SSID to calculateScore
	return vendorElements, score, nil
}

/*
func (v *VendorElementProcessor) parseVendorElements(rawIEs []byte) (map[string]interface{}, error) {
	vendorElements := make(map[string]interface{})

	// Ensure rawIEs has at least 3 bytes for OUI
	if len(rawIEs) < 3 {
		return nil, fmt.Errorf("rawIEs too short to parse OUI: %d bytes", len(rawIEs))
	}

	oui := hex.EncodeToString(rawIEs[:3])
	if strings.Contains(bitcoinOUI, oui) || strings.Contains(nostrOUI, oui) {
		data := rawIEs[3:]
		// Ensure data has at least 8 bytes for kbAllocation and contribution
		if len(data) < 8 {
			return nil, fmt.Errorf("vendor element data too short: %d bytes, expected at least 8", len(data))
		}
		kbAllocation, err := strconv.ParseFloat(string(data[:4]), 64)
		if err != nil {
			return nil, err
		}
		contribution, err := strconv.ParseFloat(string(data[4:8]), 64)
		if err != nil {
			return nil, err
		}

		vendorElements["kb_allocation_decimal"] = kbAllocation
		vendorElements["contribution_decimal"] = contribution
	}

	return vendorElements, nil
}
*/

// calculateScore calculates the score for a network. For now, it prioritizes "TollGate-" SSIDs.
func (v *VendorElementProcessor) calculateScore(signal int, ssid string, vendorElements map[string]interface{}) int {
	score := signal

	if strings.HasPrefix(ssid, "TollGate-") {
		// Assign a higher score for TollGate networks for prioritization
		score += 100 // Arbitrary boost for now
	}

	/*
		if kbAllocation, ok := vendorElements["kb_allocation_decimal"]; ok {
			score += int(kbAllocation.(float64) * 10)
		}
		if contribution, ok := vendorElements["contribution_decimal"]; ok {
			score += int(contribution.(float64) * 5)
		}
	*/

	return score
}

func (v *VendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	// Re-add necessary imports if this functionality is to be fully restored and used.
	// For now, returning nil to satisfy the interface and allow compilation.
	v.log.Printf("[crows_nest] SetLocalAPVendorElements called with: %v (functionality currently stubbed)", elements)
	return nil
}

func (v *VendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	// Re-add necessary imports and logic if this functionality is to be fully restored and used.
	// For now, returning an empty map and nil error to satisfy the interface and allow compilation.
	v.log.Println("[crows_nest] GetLocalAPVendorElements called (functionality currently stubbed)")
	return make(map[string]string), nil
}
