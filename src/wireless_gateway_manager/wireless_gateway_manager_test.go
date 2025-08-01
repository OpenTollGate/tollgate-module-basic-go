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
			name: "Non-TollGate SSID",
			ssid: "MyHomeNetwork",
			want: 0,
		},
		{
			name: "TollGate SSID with invalid hop count",
			ssid: "TollGate-IJKL-2.4GHz-abc",
			want: 0,
		},
		{
			name: "TollGate SSID with missing parts",
			ssid: "TollGate-MNOP",
			want: 0,
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
