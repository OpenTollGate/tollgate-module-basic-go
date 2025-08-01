// Package wireless_gateway_manager defines types for managing Wi-Fi gateways and network operations.
package wireless_gateway_manager

import (
	"sync"
	"time"
)

// Constants for network monitoring
const (
	pingTarget           = "8.8.8.8"
	consecutiveFailures  = 5
	consecutiveSuccesses = 5
)

// Connector manages OpenWRT network configurations via UCI commands.
type Connector struct {
}

// NetworkMonitor monitors network connectivity and manages AP state.
type NetworkMonitor struct {
	connector     *Connector
	pingFailures  int
	pingSuccesses int
	isAPDisabled  bool
	ticker        *time.Ticker
	stopChan      chan struct{}
}

// Scanner handles Wi-Fi network scanning.
type Scanner struct {
	connector *Connector
}

// NetworkInfo represents information about a Wi-Fi network.
type NetworkInfo struct {
	BSSID      string
	SSID       string
	Signal     int
	Encryption string
	HopCount   int
	RawIEs     []byte
}

// VendorElementProcessor handles Bitcoin/Nostr related vendor elements.
type VendorElementProcessor struct {
	connector *Connector
}

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
}
