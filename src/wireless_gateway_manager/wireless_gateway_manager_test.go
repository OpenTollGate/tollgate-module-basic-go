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

func TestGatewayManagerGetAvailableGateways(t *testing.T) {
	ctx := context.Background()
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
