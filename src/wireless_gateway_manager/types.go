// Package wireless_gateway_manager defines types for managing Wi-Fi gateways and network operations.
package wireless_gateway_manager

import (
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest"
)

// Constants for network monitoring
const (
	pingTarget           = "8.8.8.8"
	consecutiveFailures  = 2
	consecutiveSuccesses = 1
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
}

// NetworkMonitor monitors network connectivity and manages AP state.
type NetworkMonitor struct {
	pingFailures  int
	pingSuccesses int
	isInSafeMode  bool
	ticker        *time.Ticker
	stopChan      chan struct{}
	forceScanChan chan struct{}
}

// Scanner handles Wi-Fi network scanning.
type Scanner struct {
	connector *Connector
}

// NetworkInfo represents information about a Wi-Fi network.
type NetworkInfo struct {
	BSSID        string
	SSID         string
	Signal       int
	Encryption   string
	PricePerStep int
	StepSize     int
	RawIEs       []byte
}

// VendorElementProcessor handles Bitcoin/Nostr related vendor elements.
type VendorElementProcessor struct {
	connector *Connector
}

// Gateway represents a Wi-Fi gateway with its details.
type Gateway struct {
	BSSID          string            `json:"bssid"`
	SSID           string            `json:"ssid"`
	Signal         int               `json:"signal"`
	Encryption     string            `json:"encryption"`
	PricePerStep   int               `json:"price_per_step"`
	StepSize       int               `json:"step_size"`
	Score          int               `json:"score"`
	VendorElements map[string]string `json:"vendor_elements"`
}

// GatewayManager orchestrates the gateway management operations.
type GatewayManager struct {
	scanner           ScannerInterface
	connector         ConnectorInterface
	vendorProcessor   VendorElementProcessorInterface
	networkMonitor    NetworkMonitorInterface
	configManager     *config_manager.ConfigManager
	crowsnest         crowsnest.Crowsnest
	mu                sync.RWMutex
	availableGateways map[string]Gateway
	currentHopCount   int
	scanInterval      time.Duration
	stopChan          chan struct{}
	forceScanChan     chan struct{}
}
