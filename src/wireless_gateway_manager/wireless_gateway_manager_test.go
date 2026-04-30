package wireless_gateway_manager

import (
	"context"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func TestGatewayManagerInit(t *testing.T) {
	ctx := context.Background()
	cm := &config_manager.ConfigManager{}
	_, err := Init(ctx, cm)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}
}

func TestGatewayManager_ScanAllRadios(t *testing.T) {
	gm := &GatewayManager{
		scanner:   &Scanner{Connector: &Connector{}},
		connector: &Connector{},
	}
	_, err := gm.ScanAllRadios()
	if err == nil {
		t.Error("Expected error scanning without radios, got nil")
	}
}

func TestGatewayManager_FormatScanResults(t *testing.T) {
	gm := &GatewayManager{}
	networks := []NetworkInfo{
		{SSID: "TestNet", Signal: -50, Encryption: "psk2", Radio: "radio0"},
		{SSID: "OpenNet", Signal: -70, Encryption: "none", Radio: "radio1"},
	}
	result := gm.FormatScanResults(networks)
	if result == "" {
		t.Error("Expected non-empty formatted result")
	}
}

func TestGatewayManager_FormatScanResults_Empty(t *testing.T) {
	gm := &GatewayManager{}
	result := gm.FormatScanResults(nil)
	if result != "No networks found" {
		t.Errorf("Expected 'No networks found', got %q", result)
	}
}
