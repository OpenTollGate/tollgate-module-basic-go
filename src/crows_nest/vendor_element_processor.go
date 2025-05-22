// Package crows_nest implements the VendorElementProcessor for handling Bitcoin/Nostr vendor elements.
package crows_nest

import (
"encoding/hex"
"log"
"strconv"
"strings"
import (
"net"
)

const (
	bitcoinOUI = "00:11:22" // Example OUI for Bitcoin
	nostrOUI  = "00:33:44" // Example OUI for Nostr
)

// VendorElementProcessor handles Bitcoin/Nostr related vendor elements.
type VendorElementProcessor struct {
	log *log.Logger
}

// ExtractAndScore extracts vendor elements from NetworkInfo and calculates a score.
func (v *VendorElementProcessor) ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error) {
	// Assuming RawIEs contains the raw Information Elements
	rawIEs := ni.RawIEs

	// Parse rawIEs to extract vendor-specific IEs
	vendorElements, err := v.parseVendorElements(rawIEs)
	if err != nil {
		v.log.Printf("[crows_nest] ERROR: Failed to parse vendor elements: %v", err)
		return nil, 0, err
	}

	// Calculate score based on vendor elements and signal strength
	score := v.calculateScore(ni.Signal, vendorElements)

	return vendorElements, score, nil
}

// parseVendorElements parses raw Information Elements to extract vendor-specific data.
func (v *VendorElementProcessor) parseVendorElements(rawIEs []byte) (map[string]interface{}, error) {
	// Simplified parsing logic for demonstration
	vendorElements := make(map[string]interface{})

	// Check for vendor-specific OUI
	oui := hex.EncodeToString(rawIEs[:3])
	if strings.Contains(bitcoinOUI, oui) || strings.Contains(nostrOUI, oui) {
	// Extract relevant data fields
	// For simplicity, assume the data starts after the OUI
	data := rawIEs[3:]
	// Parse data into meaningful fields
	// Example: kb_allocation_decimal, contribution_decimal
	kbAllocation, err := strconv.ParseFloat(string(data[:4]),64)
	if err != nil {
		return nil, err
	}
	contribution, err := strconv.ParseFloat(string(data[4:8]),64)
	if err != nil {
		return nil, err
	}

	vendorElements["kb_allocation_decimal"] = kbAllocation
	vendorElements["contribution_decimal"] = contribution
}

	return vendorElements, nil
}

// calculateScore calculates a score based on signal strength and vendor elements.
func (v *VendorElementProcessor) calculateScore(signal int, vendorElements map[string]interface{}) int {
	// Simplified scoring logic
	score := signal

	// Adjust score based on vendor elements
	if kbAllocation, ok := vendorElements["kb_allocation_decimal"]; ok {
	score += int(kbAllocation.(float64) *10)
}
	if contribution, ok := vendorElements["contribution_decimal"]; ok {
	score += int(contribution.(float64) *5)
}

	return score
}

// SetLocalAPVendorElements sets vendor elements on the local AP.
func (v *VendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	// Convert elements to a byte slice representing the vendor-specific IE
	// For simplicity, assume direct conversion
	oui := net.ParseMAC(bitcoinOUI)
	if oui == nil {
	return errors.New("invalid OUI")
}
	vendorIE := append(oui, []byte("example data")...)

	// Encode vendorIE to hex string
	hexVendorIE := hex.EncodeToString(vendorIE)

	// Use Connector to execute UCI command
	// uci set wireless.default_radio0.ie='<HEX_STRING>'
	cmd := fmt.Sprintf("set wireless.default_radio0.ie='%s'", hexVendorIE)
	// Assuming ExecuteUCI is accessible
	if err := ExecuteUCI(cmd); err != nil {
	return err
}

	return nil
}

// GetLocalAPVendorElements retrieves the currently configured vendor elements on the local AP.
func (v *VendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	// Retrieve current 'ie' value using UCI
	// uci get wireless.default_radio0.ie
	cmd := "get wireless.default_radio0.ie"
	output, err := ExecuteUCI(cmd)
	if err != nil {
	return nil, err
}

	// Decode hex string back to byte slice
	vendorIE, err := hex.DecodeString(output)
	if err != nil {
	return nil, err
}

	// Parse vendorIE to reconstruct elements map
	elements := make(map[string]string)
	// Simplified parsing for demonstration
	elements["example_key"] = string(vendorIE)

	return elements, nil
}