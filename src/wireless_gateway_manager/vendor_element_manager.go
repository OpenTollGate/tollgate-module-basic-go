// Package wireless_gateway_manager implements the VendorElementProcessor for handling Bitcoin/Nostr vendor elements.
package wireless_gateway_manager

import (
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	tollgateOUI      = "212121" // TollGate custom OUI
	tollgateElemType = "01"     // TollGate custom elementType
)

// ExtractAndScore extracts vendor elements from NetworkInfo and calculates a score.
func (v *VendorElementProcessor) ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error) {
	// Temporarily bypass vendor element parsing as per user request
	// rawIEs := ni.RawIEs
	vendorElements := make(map[string]interface{}) // Initialize as empty for now
	/*
		vendorElements, err := v.parseVendorElements(rawIEs)
		if err != nil {
			v.log.Printf("[wireless_gateway_manager] ERROR: Failed to parse vendor elements: %v", err)
			return nil, 0, err
		}
	*/

	score := v.calculateScore(ni, vendorElements)
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
// The hop count from ni.HopCount is intentionally not used for scoring, as it is used for filtering connections separately in the GatewayManager.
func (v *VendorElementProcessor) calculateScore(ni NetworkInfo, vendorElements map[string]interface{}) int {
	score := ni.Signal

	// Check for the TollGate prefix, e.g., "TollGate-ABCD-2.4GHz-1"
	if strings.HasPrefix(ni.SSID, "TollGate-") {
		// Assign a higher score for TollGate networks for prioritization
		score += 100 // Arbitrary boost, as per user's requirement for captive portal
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

// // stringToHex converts a string to its hexadecimal representation
// func stringToHex(s string) string {
// 	hexStr := ""
// 	for _, b := range []byte(s) {
// 		hexStr += fmt.Sprintf("%02x", b)
// 	}
// 	return hexStr
// }

// // createVendorElement creates a vendor element with the given payload
// func createVendorElement(payload string) (string, error) {
// 	if len(payload) > 247 {
// 		return "", errors.New("payload cannot exceed 247 characters to stay within vendor_elements max size of 256 chars")
// 	}

// 	var vendorElementPayload = tollgateOUI + tollgateElemType + payload

// 	payloadLengthInBytesHex := strconv.FormatInt(int64(len(vendorElementPayload)), 16)
// 	payloadHex := stringToHex(vendorElementPayload)

// 	return "dd" + payloadLengthInBytesHex + payloadHex, nil
// }

func (v *VendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	// Re-add necessary imports if this functionality is to be fully restored and used.
	// For now, returning nil to satisfy the interface and allow compilation.
	logger.WithFields(logrus.Fields{
		"elements": elements,
	}).Debug("SetLocalAPVendorElements called (functionality currently stubbed)")
	return nil
}

func (v *VendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	// Re-add necessary imports and logic if this functionality is to be fully restored and used.
	// For now, returning an empty map and nil error to satisfy the interface and allow compilation.
	logger.Debug("GetLocalAPVendorElements called (functionality currently stubbed)")
	return make(map[string]string), nil
}
