// Package wireless_gateway_manager implements the GatewayManager for managing Wi-Fi gateways.
package wireless_gateway_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KnownNetwork holds credentials for a known Wi-Fi network.
type KnownNetwork struct {
	SSID       string `json:"ssid"`
	Password   string `json:"password"`
	Encryption string `json:"encryption"`
}

// KnownNetworks is a list of known networks.
type KnownNetworks struct {
	Networks []KnownNetwork `json:"known_networks"`
}

// GatewayManager orchestrates the gateway management operations.
type GatewayManager struct {
	scanner           *Scanner
	connector         *Connector
	vendorProcessor   *VendorElementProcessor
	networkMonitor    *NetworkMonitor
	mu                sync.RWMutex
	availableGateways map[string]Gateway
	knownNetworks     map[string]KnownNetwork // Key: SSID
	currentHopCount   int
	scanInterval      time.Duration
	stopChan          chan struct{}
	log               *log.Logger
}

// Gateway represents a Wi-Fi gateway with its details.
type Gateway struct {
	BSSID          string            `json:"bssid"`
	SSID           string            `json:"ssid"`
	Signal         int               `json:"signal"`
	Encryption     string            `json:"encryption"`
	HopCount       int               `json:"hop_count"`
	Score          int               `json:"score"`
	VendorElements map[string]string `json:"vendor_elements"`
}

// Init initializes the GatewayManager and starts its background scanning routine.
func Init(ctx context.Context, logger *log.Logger) (*GatewayManager, error) {
	scanner := &Scanner{log: logger}
	connector := &Connector{log: logger}
	vendorProcessor := &VendorElementProcessor{log: logger, connector: connector}
	networkMonitor := NewNetworkMonitor(logger, connector)

	gm := &GatewayManager{
		scanner:           scanner,
		connector:         connector,
		vendorProcessor:   vendorProcessor,
		networkMonitor:    networkMonitor,
		availableGateways: make(map[string]Gateway),
		knownNetworks:     make(map[string]KnownNetwork),
		currentHopCount:   math.MaxInt32,
		scanInterval:      30 * time.Second,
		stopChan:          make(chan struct{}),
		log:               logger,
	}

	if err := gm.loadKnownNetworks(); err != nil {
		// Log the error but don't fail initialization, as the file may not exist.
		logger.Printf("[wireless_gateway_manager] WARN: Could not load known networks: %v", err)
	}

	go gm.RunPeriodicScan(ctx)
	gm.networkMonitor.Start()

	// Set initial hop count state
	gm.updateHopCountAndAPSSID()

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
	gm.log.Println("[wireless_gateway_manager] Starting network scan for gateway selection")

	// Update current hop count based on current connection status before scanning
	gm.updateHopCountAndAPSSID()

	networks, err := gm.scanner.ScanNetworks()
	if err != nil {
		gm.log.Printf("[wireless_gateway_manager] ERROR: Failed to scan networks: %v", err)
		return
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	gm.log.Printf("[wireless_gateway_manager] Processing %d networks for gateway selection", len(networks))
	gm.availableGateways = make(map[string]Gateway)
	for _, network := range networks {
		vendorElements, score, err := gm.vendorProcessor.ExtractAndScore(network)
		if err != nil {
			gm.log.Printf("[wireless_gateway_manager] WARN: Failed to extract vendor elements for %s: %v", network.BSSID, err)
			gm.log.Printf("[wireless_gateway_manager] WARN: Failed to extract vendor elements for %s: %v", network.BSSID, err)
			continue
		}

		gateway := Gateway{
			BSSID:          network.BSSID,
			SSID:           network.SSID,
			Signal:         network.Signal,
			Encryption:     network.Encryption,
			HopCount:       network.HopCount,
			Score:          score,
			VendorElements: convertToStringMap(vendorElements),
		}

		gm.availableGateways[network.BSSID] = gateway
	}
	gm.log.Printf("[wireless_gateway_manager] Identified %d available gateways", len(gm.availableGateways))

	// Filter gateways by hop count
	var filteredGateways []Gateway
	for _, gateway := range gm.availableGateways {
		if gateway.HopCount < gm.currentHopCount {
			filteredGateways = append(filteredGateways, gateway)
		} else {
			gm.log.Printf("[wireless_gateway_manager] INFO: Filtering out gateway %s with hop count %d (our hop count is %d)", gateway.SSID, gateway.HopCount, gm.currentHopCount)
		}
	}
	gm.log.Printf("[wireless_gateway_manager] Found %d gateways with suitable hop count", len(filteredGateways))

	// Convert map to slice for sorting
	var sortedGateways []Gateway
	for _, gateway := range filteredGateways {
		sortedGateways = append(sortedGateways, gateway)
	}

	// Sort gateways by score in descending order
	sort.Slice(sortedGateways, func(i, j int) bool {
		return sortedGateways[i].Score > sortedGateways[j].Score
	})

	for i, gateway := range sortedGateways {
		if i >= 3 { // Limit to top 3 for logging
			break
		}
		gm.log.Printf("[wireless_gateway_manager] Top Gateway %d: BSSID=%s, SSID=%s, Signal=%d, Encryption=%s, HopCount=%d, Score=%d, VendorElements=%v",
			i+1, gateway.BSSID, gateway.SSID, gateway.Signal, gateway.Encryption, gateway.HopCount, gateway.Score, gateway.VendorElements)
	}

	if len(sortedGateways) > 0 {
		highestPriorityGateway := sortedGateways[0]

		currentSSID, err := gm.connector.GetConnectedSSID()
		if err != nil {
			gm.log.Printf("[wireless_gateway_manager] WARN: Could not determine current connected SSID: %v", err)
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
				gm.log.Printf("[wireless_gateway_manager] Found known network '%s'. Using stored password.", highestPriorityGateway.SSID)
			}

			// Attempt to connect if the network is open, or if it's a known network with a password.
			if highestPriorityGateway.Encryption == "Open" || highestPriorityGateway.Encryption == "" || isKnown {
				gm.log.Printf("[wireless_gateway_manager] Not connected to a top-3 gateway. Attempting to connect to highest priority gateway: BSSID=%s, SSID=%s",
					highestPriorityGateway.BSSID, highestPriorityGateway.SSID)
				err := gm.connector.Connect(highestPriorityGateway, password)
				if err != nil {
					gm.log.Printf("[wireless_gateway_manager] ERROR: Failed to connect to gateway %s: %v", highestPriorityGateway.SSID, err)
				} else {
					// Update hop count and SSID after successful connection
					gm.updateHopCountAndAPSSID()
				}
			} else {
				gm.log.Printf("[wireless_gateway_manager] Not connected to a top-3 gateway. Highest priority gateway '%s' is encrypted and not in known networks. Manual connection is required.", highestPriorityGateway.SSID)
			}
		} else {
			gm.log.Printf("[wireless_gateway_manager] Already connected to one of the top three gateways (SSID: %s). No action required.", currentSSID)
		}
	} else {
		gm.log.Println("[wireless_gateway_manager] No available gateways to connect to.")
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
		gm.updateHopCountAndAPSSID()
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
	gm.log.Println("[wireless_gateway_manager] Loading known networks from /etc/tollgate/known_networks.json")
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
		gm.log.Printf("[wireless_gateway_manager] Loaded known network: %s", network.SSID)
	}

	return nil
}

func (gm *GatewayManager) updateHopCountAndAPSSID() {
	connectedSSID, err := gm.connector.GetConnectedSSID()
	if err != nil {
		gm.log.Printf("[wireless_gateway_manager] WARN: could not get connected SSID to update hop count: %v", err)
		gm.currentHopCount = math.MaxInt32
		return
	}

	if connectedSSID == "" {
		gm.log.Println("[wireless_gateway_manager] Not connected to any network, setting hop count to max.")
		gm.currentHopCount = math.MaxInt32
		return
	}

	// Check if it's a known, non-TollGate network (which are our root connections)
	if _, isKnown := gm.knownNetworks[connectedSSID]; isKnown && !strings.HasPrefix(connectedSSID, "TollGate-") {
		gm.log.Printf("[wireless_gateway_manager] Connected to a root network '%s'. Setting hop count to 0.", connectedSSID)
		gm.currentHopCount = 0
	} else if strings.HasPrefix(connectedSSID, "TollGate-") {
		// It's a TollGate network, parse the hop count from its SSID
		parts := strings.Split(connectedSSID, "-")
		if len(parts) < 4 {
			gm.log.Printf("[wireless_gateway_manager] ERROR: TollGate SSID '%s' has unexpected format. Cannot determine hop count.", connectedSSID)
			gm.currentHopCount = math.MaxInt32 // Set to max as a safe default
		} else {
			hopCountStr := parts[len(parts)-1]
			hopCount, err := strconv.Atoi(hopCountStr)
			if err != nil {
				gm.log.Printf("[wireless_gateway_manager] ERROR: Could not parse hop count from SSID '%s': %v", connectedSSID, err)
				gm.currentHopCount = math.MaxInt32 // Set to max as a safe default
			} else {
				gm.currentHopCount = hopCount + 1
				gm.log.Printf("[wireless_gateway_manager] Connected to TollGate '%s' with hop count %d. Our new hop count is %d.", connectedSSID, hopCount, gm.currentHopCount)
			}
		}
	} else {
		gm.log.Printf("[wireless_gateway_manager] Connected to unknown network '%s'. Assuming max hop count.", connectedSSID)
		gm.currentHopCount = math.MaxInt32 // Unknown upstream, treat as disconnected for TollGate purposes
	}

	// Update the local AP's SSID to advertise the new hop count
	if err := gm.connector.UpdateLocalAPSSID(gm.currentHopCount); err != nil {
		gm.log.Printf("[wireless_gateway_manager] ERROR: Failed to update local AP SSID with new hop count: %v", err)
	}
}
