// Package crows_nest implements the VendorElementProcessor for handling Bitcoin/Nostr vendor elements.
package crows_nest

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
)

const (
	bitcoinOUI = "00:11:22" // Example OUI for Bitcoin
	nostrOUI   = "00:33:44" // Example OUI for Nostr
)

// VendorElementProcessor handles Bitcoin/Nostr related vendor elements.
type VendorElementProcessor struct {
	log       *log.Logger
	connector *Connector
}

// ExtractAndScore extracts vendor elements from NetworkInfo and calculates a score.
func (v *VendorElementProcessor) ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error) {
	rawIEs := ni.RawIEs
	vendorElements, err := v.parseVendorElements(rawIEs)
	if err != nil {
		v.log.Printf("[crows_nest] ERROR: Failed to parse vendor elements: %v", err)
		return nil, 0, err
	}

	score := v.calculateScore(ni.Signal, vendorElements)
	return vendorElements, score, nil
}

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

func (v *VendorElementProcessor) calculateScore(signal int, vendorElements map[string]interface{}) int {
	score := signal

	if kbAllocation, ok := vendorElements["kb_allocation_decimal"]; ok {
		score += int(kbAllocation.(float64) * 10)
	}
	if contribution, ok := vendorElements["contribution_decimal"]; ok {
		score += int(contribution.(float64) * 5)
	}

	return score
}

func (v *VendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	oui, err := net.ParseMAC(bitcoinOUI)
	if err != nil {
		return err
	}
	vendorIE := append(oui, []byte("example data")...)

	hexVendorIE := hex.EncodeToString(vendorIE)
	cmd := fmt.Sprintf("set wireless.default_radio0.ie='%s'", hexVendorIE)
	_, err = v.connector.ExecuteUCI(cmd)
	if err != nil {
		return err
	}

	return nil
}

func (v *VendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	cmd := "get wireless.default_radio0.ie"
	output, err := v.connector.ExecuteUCI(cmd)
	if err != nil {
		return nil, err
	}

	vendorIE, err := hex.DecodeString(output)
	if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	elements := make(map[string]string)
	elements["example_key"] = string(vendorIE)

	return elements, nil
}
