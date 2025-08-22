// Package wireless_gateway_manager implements the GatewayManager for managing Wi-Fi gateways.
package wireless_gateway_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// Init initializes the GatewayManager and starts its background scanning routine.
func Init(ctx context.Context, cm *config_manager.ConfigManager) (*GatewayManager, error) {
	connector := &Connector{}
	scanner := &Scanner{connector: connector}
	vendorProcessor := &VendorElementProcessor{connector: connector}
	networkMonitor := NewNetworkMonitor(connector)

	gm := &GatewayManager{
		scanner:           scanner,
		connector:         connector,
		vendorProcessor:   vendorProcessor,
		networkMonitor:    networkMonitor,
		cm:                cm,
		availableGateways: make(map[string]Gateway),
		knownNetworks:     make(map[string]KnownNetwork),
		currentHopCount:   math.MaxInt32,
		scanInterval:      30 * time.Second,
		stopChan:          make(chan struct{}),
	}

	if err := gm.loadKnownNetworks(); err != nil {
		// Log the error but don't fail initialization, as the file may not exist.
		logger.WithError(err).Warn("Could not load known networks")
	}

	go gm.RunPeriodicScan(ctx)
	gm.networkMonitor.Start()

	// Set initial price state
	gm.updatePriceAndAPSSID()

	return gm, nil
}

// RunPeriodicScan runs the periodic scanning routine.
func (gm *GatewayManager) RunPeriodicScan(ctx context.Context) {
	ticker := time.NewTicker(gm.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			gm.ScanWirelessNetworks(ctx)
		case <-ctx.Done():
			close(gm.stopChan)
			return
		}
	}
}

func (gm *GatewayManager) ScanWirelessNetworks(ctx context.Context) {
	logger.Info("Starting network scan for gateway selection")

	// Update current price based on current connection status before scanning
	gm.updatePriceAndAPSSID()

	networks, err := gm.scanner.ScanWirelessNetworks()
	if err != nil {
		logger.WithError(err).Error("Failed to scan networks")
		return
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	logger.WithField("network_count", len(networks)).Info("Processing networks for gateway selection")
	gm.availableGateways = make(map[string]Gateway)
	for _, network := range networks {
		// Strict filtering: if a network is encrypted and not in known_networks, skip it.
		isEncrypted := network.Encryption != "Open" && network.Encryption != ""
		_, isKnown := gm.knownNetworks[network.SSID]
		if isEncrypted && !isKnown {
			logger.WithFields(logrus.Fields{
				"ssid":       network.SSID,
				"encryption": network.Encryption,
			}).Debug("Skipping encrypted network not in known_networks.json")
			continue
		}

		vendorElements, score, err := gm.vendorProcessor.ExtractAndScore(network)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"bssid": network.BSSID,
				"error": err,
			}).Warn("Failed to extract vendor elements")
			continue
		}

		gateway := Gateway{
			BSSID:          network.BSSID,
			SSID:           network.SSID,
			Signal:         network.Signal,
			Encryption:     network.Encryption,
			PricePerStep:   network.PricePerStep,
			StepSize:       network.StepSize,
			Score:          score,
			VendorElements: convertToStringMap(vendorElements),
		}

		// Adjust score based on price.
		if gateway.PricePerStep > 0 && gateway.StepSize > 0 {
			gatewayPrice := float64(gateway.PricePerStep * gateway.StepSize)
			if gatewayPrice > 0 {
				// Use a logarithmic penalty to handle a wide range of prices gracefully.
				penalty := 20 * (math.Log(gatewayPrice) / math.Log(10))
				gateway.Score -= int(penalty)
			}
		}

		gm.availableGateways[network.BSSID] = gateway
	}
	logger.WithField("gateway_count", len(gm.availableGateways)).Info("Identified available gateways")

	// Convert map to slice for sorting
	var sortedGateways []Gateway
	for _, gateway := range gm.availableGateways {
		sortedGateways = append(sortedGateways, gateway)
	}

	// Sort gateways by score in descending order
	sort.Slice(sortedGateways, func(i, j int) bool {
		// Prioritize lower price, then higher score
		if sortedGateways[i].PricePerStep != sortedGateways[j].PricePerStep {
			return sortedGateways[i].PricePerStep < sortedGateways[j].PricePerStep
		}
		return sortedGateways[i].Score > sortedGateways[j].Score
	})

	for i, gateway := range sortedGateways {
		if i >= 3 { // Limit to top 3 for logging
			break
		}
		logger.WithFields(logrus.Fields{
			"rank":            i + 1,
			"bssid":           gateway.BSSID,
			"ssid":            gateway.SSID,
			"signal":          gateway.Signal,
			"encryption":      gateway.Encryption,
			"price_per_step":  gateway.PricePerStep,
			"step_size":       gateway.StepSize,
			"score":           gateway.Score,
			"vendor_elements": gateway.VendorElements,
		}).Info("Top gateway candidate")
	}

	if len(sortedGateways) > 0 {
		highestPriorityGateway := sortedGateways[0]

		currentSSID, err := gm.connector.GetConnectedSSID()
		if err != nil {
			logger.WithError(err).Warn("Could not determine current connected SSID")
			// Proceed with connection attempt if current SSID cannot be determined
		}

		isConnectedToTopThree := false
		for i, gateway := range sortedGateways {
			if i >= 3 {
				break
			}
			if gateway.SSID == currentSSID {
				isConnectedToTopThree = true
				break
			}
		}

		if !isConnectedToTopThree {
			password := ""
			knownNetwork, isKnown := gm.knownNetworks[highestPriorityGateway.SSID]
			if isKnown {
				password = knownNetwork.Password
				logger.WithField("ssid", highestPriorityGateway.SSID).Info("Found known network, using stored password")
			}

			// Attempt to connect if the network is open, or if it's a known network with a password.
			if highestPriorityGateway.Encryption == "Open" || highestPriorityGateway.Encryption == "" || isKnown {
				logger.WithFields(logrus.Fields{
					"bssid": highestPriorityGateway.BSSID,
					"ssid":  highestPriorityGateway.SSID,
				}).Info("Not connected to top-3 gateway, attempting to connect to highest priority gateway")
				err := gm.connector.Connect(highestPriorityGateway, password)
				if err != nil {
					logger.WithFields(logrus.Fields{
						"ssid":  highestPriorityGateway.SSID,
						"error": err,
					}).Error("Failed to connect to gateway")
				} else {
					// Update price and SSID after successful connection
					gm.updatePriceAndAPSSID()
				}
			} else {
				logger.WithField("ssid", highestPriorityGateway.SSID).Warn("Not connected to top-3 gateway. Highest priority gateway is encrypted and not in known networks. Manual connection required")
			}
		} else {
			logger.WithField("ssid", currentSSID).Info("Already connected to one of the top three gateways, no action required")
		}
	} else {
		logger.Info("No available gateways to connect to")
	}
}

func convertToStringMap(m map[string]interface{}) map[string]string {
	stringMap := make(map[string]string)
	for k, v := range m {
		stringMap[k] = fmt.Sprintf("%v", v)
	}
	return stringMap
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

	err := gm.connector.Connect(gateway, password)
	if err == nil {
		gm.mu.Lock()
		defer gm.mu.Unlock()
		gm.updatePriceAndAPSSID()
	}
	return err
}

// SetLocalAPVendorElements sets specific Bitcoin/Nostr related vendor elements on the local AP.
func (gm *GatewayManager) SetLocalAPVendorElements(elements map[string]string) error {
	return gm.vendorProcessor.SetLocalAPVendorElements(elements)
}

// GetLocalAPVendorElements retrieves the currently configured vendor elements on the local AP.
func (gm *GatewayManager) GetLocalAPVendorElements() (map[string]string, error) {
	return gm.vendorProcessor.GetLocalAPVendorElements()
}

func (gm *GatewayManager) loadKnownNetworks() error {
	logger.Info("Loading known networks from /etc/tollgate/known_networks.json")
	file, err := ioutil.ReadFile("/etc/tollgate/known_networks.json")
	if err != nil {
		return fmt.Errorf("could not read known_networks.json: %w", err)
	}

	var knownNetworks KnownNetworks
	if err := json.Unmarshal(file, &knownNetworks); err != nil {
		return fmt.Errorf("could not unmarshal known_networks.json: %w", err)
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()
	for _, network := range knownNetworks.Networks {
		gm.knownNetworks[network.SSID] = network
		logger.WithField("ssid", network.SSID).Debug("Loaded known network")
	}

	return nil
}

func (gm *GatewayManager) updatePriceAndAPSSID() {
	// If not connected to a gateway, we are in safemode.
	// We should not update the price in the config file, but we should update the SSID to reflect SafeMode.
	if !gm.networkMonitor.IsConnected() {
		logger.WithField("module", "wireless_gateway_manager").Info("Not connected to a gateway, setting AP to SafeMode")
		gm.setSafeModeSSID()
		return
	}

	// If we are connected to a gateway, we should exit safemode.
	// The SSID will be updated with the new pricing below.
	if gm.isInSafeMode() {
		logger.WithField("module", "wireless_gateway_manager").Info("Exiting SafeMode")
		gm.exitSafeMode()
	}

	connectedSSID, err := gm.connector.GetConnectedSSID()
	if err != nil {
		logger.WithError(err).Warn("Could not get connected SSID to update price")
		// We can still proceed to set a default price based on config
	}

	// If price is 0 (or not connected), use the values from the config
	pricePerStep, stepSize := parsePricingFromSSID(connectedSSID)
	if pricePerStep == 0 {
		config := gm.cm.GetConfig()
		if len(config.AcceptedMints) > 0 {
			maxPrice := 0
			maxStepSize := 0
			for _, mint := range config.AcceptedMints {
				if int(mint.PricePerStep) > maxPrice {
					maxPrice = int(mint.PricePerStep)
				}
				if int(config.StepSize) > maxStepSize {
					maxStepSize = int(config.StepSize)
				}
			}
			pricePerStep = maxPrice
			stepSize = maxStepSize
		}
	} else {
		// Apply margin to the price
		config := gm.cm.GetConfig()
		gatewayPrice := float64(pricePerStep) * float64(stepSize)
		
		// Add detailed logging for debugging the margin issue
		logger.WithField("module", "wireless_gateway_manager").
			Infof("Applying margin. Upstream price_per_step=%d, step_size=%d. Config margin=%f, config step_size=%d",
				pricePerStep, stepSize, config.Margin, config.StepSize)

		ourPrice := gatewayPrice * (1 + config.Margin)
		ourStepSize := float64(config.StepSize)
		if ourStepSize > 0 {
			pricePerStep = int(ourPrice / ourStepSize)
		}
		// ensure stepSize is updated to our configured StepSize
		stepSize = int(config.StepSize)
		
		logger.WithField("module", "wireless_gateway_manager").
			Infof("Price with margin calculated. New price_per_step=%d, new step_size=%d",
				pricePerStep, stepSize)
	}

	// Update the local AP's SSID to advertise the new pricing
	if err := gm.connector.UpdateLocalAPSSID(pricePerStep, stepSize); err != nil {
		logger.WithError(err).Error("Failed to update local AP SSID with new pricing")
	}

	// Update the config file with the new pricing
	if err := gm.cm.UpdatePricing(pricePerStep, stepSize); err != nil {
		logger.WithError(err).Error("Failed to update config file with new pricing")
	}
}

func (gm *GatewayManager) isInSafeMode() bool {
	// Forward the call to the network monitor
	return gm.networkMonitor.IsInSafeMode()
}

func (gm *GatewayManager) setSafeModeSSID() {
	// Set the local AP's SSID to SafeMode
	if err := gm.connector.SetAPSSIDSafeMode(); err != nil {
		logger.WithError(err).Error("Failed to set AP SSID to SafeMode")
	}
}

func (gm *GatewayManager) exitSafeMode() {
	// Restore the local AP's SSID from SafeMode
	if err := gm.connector.RestoreAPSSIDFromSafeMode(); err != nil {
		logger.WithError(err).Error("Failed to restore AP SSID from SafeMode")
	}
}
