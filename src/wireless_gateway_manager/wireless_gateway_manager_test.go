package wireless_gateway_manager

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// func TestGatewayManagerInit(t *testing.T) {
// 	// We expect Init to fail in a non-OpenWRT env, so we check for an error
// 	_, err := Init(context.Background())
// 	if err == nil {
// 		t.Error("expected an error, but got nil")
// 	}
// }

func TestGatewayManagerGetAvailableGateways(t *testing.T) {
	// We expect GetAvailableGateways to fail in a non-OpenWRT env
	gm, err := Init(context.Background())
	if err != nil {
		t.Skip("Skipping test, Init failed as expected")
	}
	if _, err := gm.GetAvailableGateways(); err == nil {
		t.Error("expected an error, but got nil")
	}
}

func TestGatewayManagerConnectToGateway(t *testing.T) {
	// We expect ConnectToGateway to fail in a non-OpenWRT env
	gm, err := Init(context.Background())
	if err != nil {
		t.Skip("Skipping test, Init failed as expected")
	}
	if err := gm.ConnectToGateway("some-bssid", "some-password"); err == nil {
		t.Error("expected an error, but got nil")
	}
}
*/

// Note: parseVendorElements is currently commented out in vendor_element_manager.go
// This test is kept for when that functionality is restored
/*
func TestVendorElementProcessor_parseVendorElements_ShortIEs(t *testing.T) {
	processor := &VendorElementProcessor{}

	tests := []struct {
		name    string
		rawIEs  []byte
		wantErr bool
	}{
		{
			name:    "rawIEs too short for OUI (less than 3 bytes)",
			rawIEs:  []byte{0x01, 0x02},
			wantErr: true,
		},
		{
			name:    "data too short for kbAllocation (less than 4 bytes after OUI)",
			rawIEs:  []byte{0x00, 0x00, 0x00, 0x01, 0x02, 0x03}, // OUI + 3 bytes data
			wantErr: true,
		},
		{
			name:    "data too short for contribution (less than 8 bytes after OUI)",
			rawIEs:  []byte{0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}, // OUI + 7 bytes data
			wantErr: true,
		},
		{
			name:    "valid OUI and data length",
			rawIEs:  []byte{0x00, 0x00, 0x00, 0x31, 0x30, 0x30, 0x30, 0x31, 0x30, 0x30, 0x30, 0x30}, // OUI + "1000" + "1000"
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := processor.parseVendorElements(tt.rawIEs)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVendorElements() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
*/

func TestParseHopCountFromSSID(t *testing.T) {
	tests := []struct {
		name string
		ssid string
		want int
	}{
		{
			name: "Valid TollGate SSID with hop count",
			ssid: "TollGate-ABCD-2.4GHz-1",
			want: 1,
		},
		{
			name: "Valid TollGate SSID with zero hop count",
			ssid: "TollGate-EFGH-5GHz-0",
			want: 0,
		},
		{
			name: "TollGate SSID without hop count",
			ssid: "TollGate-ABCD-2.4GHz",
			want: math.MaxInt32,
		},
		{
			name: "Non-TollGate SSID",
			ssid: "MyHomeNetwork",
			want: 0,
		},
		{
			name: "TollGate SSID with invalid hop count",
			ssid: "TollGate-IJKL-2.4GHz-abc",
			want: math.MaxInt32,
		},
		{
			name: "TollGate SSID with missing parts",
			ssid: "TollGate-MNOP",
			want: math.MaxInt32,
		},
		{
			name: "Empty SSID",
			ssid: "",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseHopCountFromSSID(tt.ssid); got != tt.want {
				t.Errorf("parseHopCountFromSSID() = %v, want %v", got, tt.want)
			}
		})
	}
}

/**
func TestScanWirelessNetworksScoring(t *testing.T) {
	gm := &GatewayManager{
		availableGateways: make(map[string]Gateway),
	}

	// Gateways to be tested
	gateways := []struct {
		gateway Gateway
		penalty int
	}{
		{gateway: Gateway{BSSID: "1", SSID: "TollGate-A", HopCount: 0, Score: 100}, penalty: 0},
		{gateway: Gateway{BSSID: "2", SSID: "TollGate-B", HopCount: 1, Score: 100}, penalty: 20},
		{gateway: Gateway{BSSID: "3", SSID: "TollGate-C", HopCount: 2, Score: 100}, penalty: 40},
		{gateway: Gateway{BSSID: "4", SSID: "TollGate-D", HopCount: 3, Score: 100}, penalty: 60},
	}

	for _, item := range gateways {
		gm.availableGateways[item.gateway.BSSID] = item.gateway
	}

	// Apply scoring penalty
	for bssid, gw := range gm.availableGateways {
		gw.Score -= gw.HopCount * 20
		gm.availableGateways[bssid] = gw
	}

	// Expected scores after penalty
	expectedScores := map[string]int{
		"1": 100,
		"2": 80,
		"3": 60,
		"4": 40,
	}

	for bssid, expectedScore := range expectedScores {
		if gm.availableGateways[bssid].Score != expectedScore {
			t.Errorf("Expected score for BSSID %s to be %d, but got %d", bssid, expectedScore, gm.availableGateways[bssid].Score)
		}
	}
}
type mockConnector struct {
	Connector
	mockUciOutput string
	mockUciError  error
}
**/

/**
func (m *mockConnector) ExecuteUCI(args ...string) (string, error) {
	return m.mockUciOutput, m.mockUciError
}
**/

/*
func TestGetRadioDeviceNames(t *testing.T) {
	tests := []struct {
		name          string
		mockUciOutput string
		mockUciError  error
		expected      []string
		expectErr     bool
	}{
		{
			name: "Valid wireless config with two radios",
			mockUciOutput: `
wireless.mt798111='wifi-device'
wireless.mt798112='wifi-device'
`,
			expected:  []string{"mt798111", "mt798112"},
			expectErr: false,
		},
		{
			name:          "UCI command fails",
			mockUciError:  errors.New("uci command failed"),
			expected:      nil,
			expectErr:     true,
		},
		{
			name:          "No wifi-device sections found",
			mockUciOutput: "wireless.default_radio0='wifi-iface'",
			expected:      nil,
			expectErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := &mockConnector{
				mockUciOutput: tt.mockUciOutput,
				mockUciError:  tt.mockUciError,
			}
			radioNames, err := connector.GetRadioDeviceNames()

			if (err != nil) != tt.expectErr {
				t.Errorf("GetRadioDeviceNames() error = %v, wantErr %v", err, tt.expectErr)
				return
			}

			if !equalSlices(radioNames, tt.expected) {
				t.Errorf("GetRadioDeviceNames() = %v, want %v", radioNames, tt.expected)
			}
		})
	}
}
*/

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

/**
func (c *mockConnector) GetRadioDeviceNames() ([]string, error) {
	output, err := c.ExecuteUCI("show", "wireless")
	if err != nil {
		return nil, fmt.Errorf("failed to get wireless config: %w", err)
	}

	var radioNames []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, "='wifi-device'") {
			section := strings.TrimSuffix(line, "='wifi-device'")
			parts := strings.Split(section, ".")
			if len(parts) > 0 {
				radioNames = append(radioNames, parts[len(parts)-1])
			}
		}
	}

	if len(radioNames) == 0 {
		return nil, errors.New("no wifi-device sections found in wireless config")
	}

	return radioNames, nil
}
**/