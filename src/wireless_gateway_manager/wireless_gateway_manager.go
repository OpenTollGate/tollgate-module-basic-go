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
	"strconv"
	"strings"
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
		knownNetworks:          make(map[string]KnownNetwork),
		gatewaysWithNoInternet: make(map[string]time.Time),
		scanInterval:           60 * time.Second,
		stopChan:               make(chan struct{}),
	}

	if err := gm.loadKnownNetworks(); err != nil {
		// Log the error but don't fail initialization, as the file may not exist.
		logger.WithError(err).Warn("Could not load known networks")
	}

	if err := gm.syncKnownNetworksFromWirelessConfig(); err != nil {
		logger.WithError(err).Warn("Could not sync known networks from wireless config")
	}

	go gm.RunPeriodicScan(ctx)
	gm.networkMonitor.Start()

	// Set initial AP SSID state
	gm.updateAPSSID()

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
	gm.mu.Lock()
	defer gm.mu.Unlock()
	logger.Info("Starting network scan for gateway selection")

	// Update current AP SSID based on current connection status before scanning
	gm.updateAPSSID()

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
			Radio:          network.Radio,
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

	// Filter out gateways that have recently failed internet connectivity checks
	var viableGateways []Gateway
	for _, gateway := range gm.availableGateways {
		if _, ok := gm.gatewaysWithNoInternet[gateway.BSSID]; !ok {
			viableGateways = append(viableGateways, gateway)
		}
	}

	// If all available gateways are in the no-internet list, try the least recently failed one.
	if len(viableGateways) == 0 && len(gm.availableGateways) > 0 {
		var leastRecentlyFailedGateway Gateway
		var oldestTimestamp time.Time

		for bssid, timestamp := range gm.gatewaysWithNoInternet {
			if gateway, ok := gm.availableGateways[bssid]; ok {
				if oldestTimestamp.IsZero() || timestamp.Before(oldestTimestamp) {
					oldestTimestamp = timestamp
					leastRecentlyFailedGateway = gateway
				}
			}
		}
		if !oldestTimestamp.IsZero() {
			logger.WithField("bssid", leastRecentlyFailedGateway.BSSID).Info("All gateways have failed connectivity checks. Retrying the least recently failed one.")
			viableGateways = append(viableGateways, leastRecentlyFailedGateway)
			// Remove from the no-internet list to allow connection attempt
			delete(gm.gatewaysWithNoInternet, leastRecentlyFailedGateway.BSSID)
		}
	}

	// Sort gateways by score in descending order
	sort.Slice(viableGateways, func(i, j int) bool {
		return viableGateways[i].Score > viableGateways[j].Score
	})

	for i, gateway := range viableGateways {
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
			"radio":           gateway.Radio,
		}).Info("Top gateway candidate")
	}

	if len(viableGateways) > 0 {
		highestPriorityGateway := viableGateways[0]

		currentSSID, err := gm.connector.GetConnectedSSID()
		if err != nil {
			logger.WithError(err).Warn("Could not determine current connected SSID")
		}

		isConnectedToTopThree := false
		for i, gateway := range viableGateways {
			if i >= 3 {
				break
			}
			if gateway.SSID == currentSSID {
				isConnectedToTopThree = true
				break
			}
		}

		// If not connected to a top gateway, or if connected but no internet, try to connect.
		if !isConnectedToTopThree || !gm.networkMonitor.IsConnected() {
			if !gm.networkMonitor.IsConnected() {
				logger.Warn("No internet connectivity, attempting to connect to the best gateway.")
				// If we are connected to a gateway but have no internet, add it to the bad list.
				if currentSSID != "" {
					for _, g := range gm.availableGateways {
						if g.SSID == currentSSID {
							gm.gatewaysWithNoInternet[g.BSSID] = time.Now()
							logger.WithField("bssid", g.BSSID).Warn("Added gateway to no-internet list")
							break
						}
					}
				}
			}

			password := ""
			knownNetwork, isKnown := gm.knownNetworks[highestPriorityGateway.SSID]
			if isKnown {
				password = knownNetwork.Password
				logger.WithField("ssid", highestPriorityGateway.SSID).Info("Found known network, using stored password")
			}

			if highestPriorityGateway.Encryption == "Open" || highestPriorityGateway.Encryption == "" || isKnown {
				logger.WithFields(logrus.Fields{
					"bssid": highestPriorityGateway.BSSID,
					"ssid":  highestPriorityGateway.SSID,
				}).Info("Attempting to connect to highest priority gateway")
				err := gm.connector.Connect(highestPriorityGateway, password)
				if err != nil {
					logger.WithFields(logrus.Fields{
						"ssid":  highestPriorityGateway.SSID,
						"error": err,
					}).Error("Failed to connect to gateway")
				} else {
					// Connection successful, clear the no-internet list
					gm.gatewaysWithNoInternet = make(map[string]time.Time)
					gm.updateAPSSID()
				}
			} else {
				logger.WithField("ssid", highestPriorityGateway.SSID).Warn("Highest priority gateway is encrypted and not in known networks. Manual connection required")
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
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gateway, ok := gm.availableGateways[bssid]

	if !ok {
		return errors.New("gateway not found")
	}

	err := gm.connector.Connect(gateway, password)
	if err == nil {
		gm.mu.Lock()
		defer gm.mu.Unlock()
		gm.updateAPSSID()
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

func (gm *GatewayManager) syncKnownNetworksFromWirelessConfig() error {
	logger.Info("Syncing known networks from wireless config")

	// 1. Get wireless config
	output, err := gm.connector.ExecuteUCI("show", "wireless")
	if err != nil {
		return fmt.Errorf("could not get wireless config: %w", err)
	}

	// 2. Parse the output to find STA interfaces
	// This is a simplified parser. A more robust solution might use a dedicated UCI parsing library.
	creds := make(map[string]KnownNetwork)
	var currentSSID, currentKey, currentEncryption string
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "'")

		if strings.HasSuffix(key, ".mode") && value == "sta" {
			// When we find a new STA section, save the previous one if it's complete
			if currentSSID != "" && currentKey != "" && currentEncryption != "" {
				creds[currentSSID] = KnownNetwork{
					SSID:       currentSSID,
					Password:   currentKey,
					Encryption: currentEncryption,
				}
			}
			// Reset for the new section
			currentSSID, currentKey, currentEncryption = "", "", ""
		}

		if strings.HasSuffix(key, ".ssid") {
			currentSSID = value
		}
		if strings.HasSuffix(key, ".key") {
			currentKey = value
		}
		if strings.HasSuffix(key, ".encryption") {
			currentEncryption = value
		}
	}
	// Save the last parsed network
	if currentSSID != "" && currentKey != "" && currentEncryption != "" {
		creds[currentSSID] = KnownNetwork{
			SSID:       currentSSID,
			Password:   currentKey,
			Encryption: currentEncryption,
		}
	}

	// 3. Add new networks to known_networks.json
	gm.mu.Lock()
	defer gm.mu.Unlock()

	var updated bool
	for ssid, cred := range creds {
		if _, exists := gm.knownNetworks[ssid]; !exists {
			gm.knownNetworks[ssid] = cred
			logger.WithField("ssid", ssid).Info("Added new network to known networks from wireless config")
			updated = true
		}
	}

	// 4. Write back to file if updated
	if updated {
		var knownNetworks KnownNetworks
		for _, network := range gm.knownNetworks {
			knownNetworks.Networks = append(knownNetworks.Networks, network)
		}
		file, err := json.MarshalIndent(knownNetworks, "", "  ")
		if err != nil {
			return fmt.Errorf("could not marshal known_networks.json: %w", err)
		}
		if err := ioutil.WriteFile("/etc/tollgate/known_networks.json", file, 0644); err != nil {
			return fmt.Errorf("could not write known_networks.json: %w", err)
		}
	}

	return nil
}

func (gm *GatewayManager) updateAPSSID() {
	// First, get the pricing information from the vendor elements
	elements, err := gm.vendorProcessor.GetLocalAPVendorElements()
	if err != nil {
		logger.WithError(err).Error("Failed to get local AP vendor elements")
		// Handle error appropriately, maybe set default pricing
		return
	}

	priceStr, ok := elements["price_per_step"]
	if !ok {
		priceStr = "0" // Default to 0 if not set
	}
	pricePerStep, err := strconv.Atoi(priceStr)
	if err != nil {
		logger.WithError(err).Error("Failed to parse price_per_step from vendor elements, defaulting to 0")
		pricePerStep = 0 // Default to 0 on parsing error
	}

	stepStr, ok := elements["step_size"]
	if !ok {
		stepStr = "0" // Default to 0 if not set
	}
	stepSize, err := strconv.Atoi(stepStr)
	if err != nil {
		logger.WithError(err).Error("Failed to parse step_size from vendor elements, defaulting to 0")
		stepSize = 0 // Default to 0 on parsing error
	}

	// If price is 0, use the values from the config
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
		ourPrice := gatewayPrice * (1 + config.Margin)
		ourStepSize := float64(config.StepSize)
		if ourStepSize > 0 {
			pricePerStep = int(ourPrice / ourStepSize)
		}
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

