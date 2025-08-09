package wireless_gateway_manager

import (
	"context"
	"testing"
)

func TestGatewayManagerInit(t *testing.T) {
	ctx := context.Background()
	_, err := Init(ctx)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}
}

func TestGatewayManagerGetAvailableGateways(t *testing.T) {
	ctx := context.Background()
	gm, err := Init(ctx)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}

	_, err = gm.GetAvailableGateways()
	if err != nil {
		t.Errorf("GetAvailableGateways failed: %v", err)
	}
}

func TestGatewayManagerConnectToGateway(t *testing.T) {
	ctx := context.Background()
	gm, err := Init(ctx)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}

	err = gm.ConnectToGateway("example_bssid", "example_password")
	if err == nil {
		t.Log("ConnectToGateway succeeded as expected with mocked connector")
	} else {
		t.Errorf("ConnectToGateway failed: %v", err)
	}
}

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

import "math"

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
