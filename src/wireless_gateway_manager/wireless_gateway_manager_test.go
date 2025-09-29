package wireless_gateway_manager

import (
	"context"
	"math"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func TestGatewayManagerInit(t *testing.T) {
	ctx := context.Background()
	// Create a mock config manager for testing
	cm := &config_manager.ConfigManager{}
	_, err := Init(ctx, cm)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}
}

func TestGatewayManagerGetAvailableGateways(t *testing.T) {
	ctx := context.Background()
	// Create a mock config manager for testing
	cm := &config_manager.ConfigManager{}
	gm, err := Init(ctx, cm)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}

	_, err = gm.GetAvailableGateways()
	if err != nil {
		t.Errorf("GetAvailableGateways failed: %v", err)
	}
}



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
