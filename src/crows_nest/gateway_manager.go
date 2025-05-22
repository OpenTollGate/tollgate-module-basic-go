// Package crows_nest implements the GatewayManager for managing Wi-Fi gateways.
package crows_nest

import (
	"context"
	"log"
	"sync"
	"time"
)

// GatewayManager orchestrates the gateway management operations.
type GatewayManager struct {
	scanner       *Scanner
	connector     *Connector
	vendorProcessor *VendorElementProcessor
	mu            sync.RWMutex
	availableGateways map[string]Gateway
	scanInterval  time.Duration
	stopChan      chan struct{}
	log           *log.Logger
}

// Gateway represents a Wi-Fi gateway with its details.
type Gateway struct {
	BSSID string `json:"bssid"`
	SSID string `json:"ssid"`
	Signal int `json:"signal"`
	Encryption string `json:"encryption"`
	Score int `json:"score"`
	VendorElements map[string]string `json:"vendor_elements"`
}

// Init initializes the GatewayManager and starts its background scanning routine.
func Init(ctx context.Context, logger *log.Logger) (*GatewayManager, error) {
	scanner := &Scanner{log: logger}
	connector := &Connector{log: logger}
	vendorProcessor := &VendorElementProcessor{log: logger}

	gm := &GatewayManager{
		scanner:       scanner,
		connector:     connector,
		vendorProcessor: vendorProcessor,
		availableGateways: make(map[string]Gateway),
		scanInterval:  30 * time.Second,
		stopChan:      make(chan struct{}),
		log:           logger,
	}

	go gm.RunPeriodicScan(ctx)

	return gm, nil
}

// RunPeriodicScan runs the periodic scanning routine.
func (gm *GatewayManager) RunPeriodicScan(ctx context.Context) {
	ticker := time.NewTicker(gm.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			gm.scanNetworks(ctx)
		case <-ctx.Done():
			close(gm.stopChan)
			return
		}
	}
}

func (gm *GatewayManager) scanNetworks(ctx context.Context) {
	networks, err := gm.scanner.ScanNetworks()
	if err != nil {
		gm.log.Printf("[crows_nest] ERROR: Failed to scan networks: %v", err)
		return
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	gm.availableGateways = make(map[string]Gateway)
	for _, network := range networks {
		vendorElements, score, err := gm.vendorProcessor.ExtractAndScore(network)
		if err != nil {
			gm.log.Printf("[crows_nest] WARN: Failed to extract vendor elements for %s: %v", network.BSSID, err)
			continue
		}

		gateway := Gateway{
			BSSID: network.BSSID,
			SSID:  network.SSID,
			Signal: network.Signal,
			Encryption: network.Encryption,
			Score: score,
			VendorElements: vendorElements,
		}

		gm.availableGateways[network.BSSID] = gateway
	}
}

// GetAvailableGateways returns a snapshot of the currently available gateways.
func (gm *GatewayManager) GetAvailableGateways() ([]Gateway, error) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	var gateways []Gateway
	for _, gateway := range gm.availableGateways {
		gateways = append(gateways, gateway)
	}

	return gateways, nil
}

// ConnectToGateway instructs the GatewayManager to connect to the specified gateway.
func (gm *GatewayManager) ConnectToGateway(bssid string, password string) error {
	gm.mu.RLock()
	gateway, ok := gm.availableGateways[bssid]
	gm.mu.RUnlock()

	if !ok {
		return errors.New("gateway not found")
	}

	return gm.connector.Connect(gateway)
}

// SetLocalAPVendorElements sets specific Bitcoin/Nostr related vendor elements on the local AP.
func (gm *GatewayManager) SetLocalAPVendorElements(elements map[string]string) error {
	return gm.vendorProcessor.SetLocalAPVendorElements(elements)
}

// GetLocalAPVendorElements retrieves the currently configured vendor elements on the local AP.
func (gm *GatewayManager) GetLocalAPVendorElements() (map[string]string, error) {
	return gm.vendorProcessor.GetLocalAPVendorElements()
}