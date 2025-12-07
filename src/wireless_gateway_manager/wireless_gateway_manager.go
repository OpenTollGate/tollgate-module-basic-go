// Package wireless_gateway_manager implements the GatewayManager for managing Wi-Fi gateways.
package wireless_gateway_manager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest"
)

// Init initializes the GatewayManager and starts its background scanning routine.
func Init(ctx context.Context, configManager *config_manager.ConfigManager, crowsnest crowsnest.Crowsnest) (*GatewayManager, error) {
	connector := &Connector{}
	scanner := &Scanner{connector: connector}
	vendorProcessor := &VendorElementProcessor{connector: connector}
	gatewayManager := &GatewayManager{
		scanner:           scanner,
		connector:         connector,
		vendorProcessor:   vendorProcessor,
		configManager:     configManager,
		crowsnest:         crowsnest,
		availableGateways: make(map[string]Gateway),
		currentHopCount:   math.MaxInt32,
		scanInterval:      30 * time.Second,
		stopChan:          make(chan struct{}),
		forceScanChan:     make(chan struct{}, 1), // Buffered channel
	}

	networkMonitor := NewNetworkMonitor(connector, gatewayManager.forceScanChan)
	gatewayManager.networkMonitor = networkMonitor

	go gatewayManager.RunPeriodicScan(ctx)
	gatewayManager.networkMonitor.Start()

	// Set initial price state
	gatewayManager.updatePriceAndAPSSID()

	return gatewayManager, nil
}

// RunPeriodicScan runs the periodic scanning routine.
func (gm *GatewayManager) RunPeriodicScan(ctx context.Context) {
	ticker := time.NewTicker(gm.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			gm.ScanWirelessNetworks(ctx)
		case <-gm.forceScanChan:
			logger.Info("Connectivity loss detected, forcing immediate network scan.")
			gm.handleConnectivityLoss(ctx)
		case <-ctx.Done():
			close(gm.stopChan)
			return
		}
	}
}

func (gm *GatewayManager) ScanWirelessNetworks(ctx context.Context) {
	// Check for internet connectivity before initiating a disruptive scan
	online, err := gm.connector.CheckInternetConnectivity()
	if err != nil {
		logger.WithError(err).Warn("Failed to perform internet connectivity check")
		// Proceed with scan anyway, as the check itself might have failed for other reasons
	}
	if online {
		logger.Info("Internet connection is active, skipping gateway scan.")
		return
	}

	// Get the current configuration
	config := gm.configManager.GetConfig()

	// Check if reseller mode is enabled
	if !config.ResellerMode {
		logger.Debug("Reseller mode is disabled. Skipping automatic gateway selection.")
		return
	}

	logger.Info("Starting network scan for gateway selection in reseller mode")

	// Update current price based on current connection status before scanning
	gm.updatePriceAndAPSSID()

	networks, err := gm.scanner.ScanWirelessNetworks()
	if err != nil {
		logger.WithError(err).Error("Failed to scan networks")
		return
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	// Filter networks to only include those with SSIDs starting with "TollGate-"
	var tollgateNetworks []NetworkInfo
	for _, network := range networks {
		if len(network.SSID) >= 9 && network.SSID[:9] == "TollGate-" {
			tollgateNetworks = append(tollgateNetworks, network)
		}
	}

	logger.WithField("network_count", len(tollgateNetworks)).Info("Processing TollGate networks for gateway selection")
	gm.availableGateways = make(map[string]Gateway)
	for _, network := range tollgateNetworks {
		// In reseller mode, we only connect to open TollGate networks
		isEncrypted := network.Encryption != "Open" && network.Encryption != ""
		if isEncrypted {
			logger.WithFields(logrus.Fields{
				"ssid":       network.SSID,
				"encryption": network.Encryption,
			}).Debug("Skipping encrypted TollGate network")
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
	logger.WithField("gateway_count", len(gm.availableGateways)).Info("Identified available TollGate gateways")

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
		}).Info("Top TollGate gateway candidate")
	}

	if len(sortedGateways) > 0 {
		// Force a reconnection attempt if the network monitor has detected a disconnection.
		// This handles the case where the gateway reboots but the client is still associated.
		if !gm.networkMonitor.IsConnected() {
			logger.Info("Network monitor reports disconnection. Forcing reconnection attempt.")
		} else {
			// If we are connected, check if it's to a top-tier gateway.
			currentSSID, err := gm.connector.GetConnectedSSID()
			if err != nil {
				logger.WithError(err).Warn("Could not determine current connected SSID")
			}

			isConnectedToTopThree := false
			if currentSSID != "" {
				for i, gateway := range sortedGateways {
					if i >= 3 {
						break
					}
					if gateway.SSID == currentSSID {
						isConnectedToTopThree = true
						break
					}
				}
			}

			if isConnectedToTopThree {
				logger.WithField("ssid", currentSSID).Info("Already connected to one of the top three TollGate gateways, no action required")
				return // Return early, no need to attempt connection
			}
		}

		// If we've reached here, it's either because we are disconnected or not connected to a top gateway.
		// Proceed with connection attempt to the highest priority gateway.
		highestPriorityGateway := sortedGateways[0]
		// In reseller mode, all TollGate networks are considered "known" and open
		password := ""

		// Attempt to connect to the highest priority TollGate network
		logger.WithFields(logrus.Fields{
			"bssid": highestPriorityGateway.BSSID,
			"ssid":  highestPriorityGateway.SSID,
		}).Info("Not connected to top-3 TollGate gateway, attempting to connect to highest priority gateway")
		err := gm.connector.Connect(highestPriorityGateway, password)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"ssid":  highestPriorityGateway.SSID,
				"error": err,
			}).Error("Failed to connect to TollGate gateway")
		} else {
			// Update price and SSID after successful connection
			gm.updatePriceAndAPSSID()

			// Add a delay to allow for DHCP to complete.
			// Crowsnest will automatically detect the new interface and gateway via netlink events.
			// There is no need to manually trigger a scan here.
			logger.Info("Connection successful, verifying DHCP lease and route...")
			if err := gm.connector.WaitForIPAddress("wwan", 30*time.Second); err != nil {
				logger.WithError(err).Error("Failed to acquire IP address after connection")
			} else {
				logger.Info("Successfully acquired IP address on wwan, waiting for default route...")
				// We need the physical interface name for the route check.
				// It's safe to assume the active STA interface is the one we just configured.
				uciInterface, err := gm.connector.getActiveSTAInterface()
				if err != nil {
					logger.WithError(err).Error("Could not get active STA interface to check for route")
				} else {
					physicalInterface, err := gm.connector.waitForInterface(uciInterface)
					if err != nil {
						logger.WithError(err).Error("Could not get physical interface to check for route")
					} else {
						if err := gm.connector.WaitForDefaultRoute(physicalInterface, 15*time.Second); err != nil {
							logger.WithError(err).Error("Failed to acquire default route after getting IP")
						} else {
							logger.Info("Default route is active. Network is fully up.")
							// Explicitly trigger a crowsnest scan on the new interface to ensure
							// the payment session is established.
							logger.WithField("interface", physicalInterface).Info("Triggering Crowsnest scan on new interface.")
							gm.crowsnest.ScanInterface(physicalInterface)
						}
					}
				}
			}
		}
	} else {
		logger.Info("No available TollGate gateways to connect to")
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

func (gm *GatewayManager) updatePriceAndAPSSID() {
	// If not connected to a gateway, we are in safemode.
	// We should not update the price in the config file, but we should update the SSID to reflect SafeMode.
	if !gm.networkMonitor.IsConnected() {
		logger.WithField("module", "wireless_gateway_manager").Info("Not connected to a gateway")
		return
	}

	connectedSSID, err := gm.connector.GetConnectedSSID()
	if err != nil {
		logger.WithError(err).Warn("Could not get connected SSID to update price")
		// We can still proceed to set a default price based on config
	}

	// If price is 0 (or not connected), use the values from the config
	pricePerStep, stepSize := parsePricingFromSSID(connectedSSID)
	if pricePerStep == 0 {
		config := gm.configManager.GetConfig()
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
		config := gm.configManager.GetConfig()
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
	if err := gm.configManager.UpdatePricing(pricePerStep, stepSize); err != nil {
		logger.WithError(err).Error("Failed to update config file with new pricing")
	}
}

func (gm *GatewayManager) handleConnectivityLoss(ctx context.Context) {
	// Add a cooldown period to allow the OS to fully process the interface down event
	// before we attempt to reconfigure it. This helps prevent race conditions.
	logger.Info("Connectivity loss detected, starting cooldown before attempting reconnection...")
	time.Sleep(5 * time.Second)

	config := gm.configManager.GetConfig()
	if config.ResellerMode {
		logger.Info("Reseller mode enabled, performing a full network scan")
		gm.ScanWirelessNetworks(ctx)
	} else {
		logger.Info("Reseller mode disabled, attempting to reconnect to the current network")
		if err := gm.connector.Reconnect(); err != nil {
			logger.WithError(err).Error("Failed to reconnect to the network")
			return
		}

		// After reconnecting, Crowsnest will detect the new interface and trigger a scan automatically.
		logger.Info("Reconnect successful, verifying DHCP lease and route...")
		if err := gm.connector.WaitForIPAddress("wwan", 30*time.Second); err != nil {
			logger.WithError(err).Error("Failed to acquire IP address after reconnect")
		} else {
			logger.Info("Successfully acquired IP address on wwan, waiting for default route...")
			uciInterface, err := gm.connector.getActiveSTAInterface()
			if err != nil {
				logger.WithError(err).Error("Could not get active STA interface to check for route")
			} else {
				physicalInterface, err := gm.connector.waitForInterface(uciInterface)
				if err != nil {
					logger.WithError(err).Error("Could not get physical interface to check for route")
				} else {
					if err := gm.connector.WaitForDefaultRoute(physicalInterface, 15*time.Second); err != nil {
						logger.WithError(err).Error("Failed to acquire default route after getting IP")
					} else {
						logger.Info("Default route is active. Network is fully up.")
					}
				}
			}
		}
	}
}
