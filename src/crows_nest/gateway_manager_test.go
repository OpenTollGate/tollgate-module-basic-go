package crows_nest

import (
	"context"
	"log"
	"testing"
)

func TestGatewayManagerInit(t *testing.T) {
	logger := log.New(log.Writer(), "test: ", log.LstdFlags)
	ctx := context.Background()
	_, err := Init(ctx, logger)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}
}

func TestGatewayManagerGetAvailableGateways(t *testing.T) {
	logger := log.New(log.Writer(), "test: ", log.LstdFlags)
	ctx := context.Background()
	gm, err := Init(ctx, logger)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}

	_, err = gm.GetAvailableGateways()
	if err != nil {
		t.Errorf("GetAvailableGateways failed: %v", err)
	}
}

func TestGatewayManagerConnectToGateway(t *testing.T) {
	logger := log.New(log.Writer(), "test: ", log.LstdFlags)
	ctx := context.Background()
	gm, err := Init(ctx, logger)
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
