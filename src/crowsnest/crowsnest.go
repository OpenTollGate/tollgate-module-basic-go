package crowsnest

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/jackpal/gateway"
	"github.com/nbd-wtf/go-nostr"
	"github.com/vishvananda/netlink"
)

// NetworkInterface represents a network interface with its properties
type NetworkInterface struct {
	Name          string   // Name of the interface (e.g., "wlan0")
	IsTollgate    bool     // Whether the interface is a tollgate
	IsAvailable   bool     // Whether the interface is available for connection
	Metric        string   // Metric used for pricing (e.g., "milliseconds")
	StepSize      uint64   // Size of each pricing step
	PricePerStep  uint64   // Price per step in satoshis
	URL           string   // URL for the tollgate (only for tollgates)
	AcceptedMints []string // List of accepted mints
}

// Crowsnest manages network interfaces and their details
type Crowsnest struct {
	config     *config_manager.Config
	interfaces []NetworkInterface
}

// New creates a new Crowsnest instance
func New(configManager *config_manager.ConfigManager) (*Crowsnest, error) {
	log.Printf("=== Crowsnest Initializing ===")

	config, err := configManager.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	crowsnest := &Crowsnest{
		config:     config,
		interfaces: make([]NetworkInterface, 0),
	}

	log.Printf("=== Crowsnest ready ===")
	return crowsnest, nil
}

// GetConnected returns a list of available network interfaces
func (c *Crowsnest) GetConnected() ([]NetworkInterface, error) {
	// Get available interfaces that are already connected
	connectedInterfaces, err := c.getConnectedInterfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get connected interfaces: %w", err)
	}

	// Reset interfaces list
	c.interfaces = make([]NetworkInterface, 0)

	// Process each connected interface
	for _, iface := range connectedInterfaces {
		// Skip loopback
		if iface.Name == "lo" {
			continue
		}

		// Skip interface that is hosting our tollgate
		// This is typically br-lan on OpenWRT
		if iface.Name == "br-lan" {
			continue
		}

		// Check if the interface connects to a tollgate
		isTollgate, merchantURL := c.checkForTollgate(iface.Name)

		if isTollgate {
			// If it's a tollgate, get the merchant advertisement and parse it
			advertisement, err := c.fetchMerchantAdvertisement(merchantURL)
			if err != nil {
				log.Printf("Error fetching merchant advertisement from %s: %v", merchantURL, err)
				continue
			}

			networkInterface, err := c.parseMerchantAdvertisement(advertisement, iface.Name)
			if err != nil {
				log.Printf("Error parsing merchant advertisement: %v", err)
				continue
			}

			networkInterface.URL = merchantURL
			c.interfaces = append(c.interfaces, networkInterface)
		} else {
			// If it's a free network, add it to the list
			c.interfaces = append(c.interfaces, NetworkInterface{
				Name:        iface.Name,
				IsTollgate:  false,
				IsAvailable: true,
			})
		}
	}

	return c.interfaces, nil
}

// getConnectedInterfaces returns a list of network interfaces that are connected
func (c *Crowsnest) getConnectedInterfaces() ([]net.Interface, error) {
	// Get all network interfaces
	allInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	// Filter to only include up interfaces
	var upInterfaces []net.Interface
	for _, iface := range allInterfaces {
		// Check if interface is up and not a loopback
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			upInterfaces = append(upInterfaces, iface)
		}
	}

	return upInterfaces, nil
}

// getInterfaceGateway returns the gateway IP for the given interface
func (c *Crowsnest) getInterfaceGateway(ifaceName string) (string, error) {
	// Get all routes
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return "", fmt.Errorf("failed to get routes: %w", err)
	}

	// Get the interface index
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("interface not found: %w", err)
	}

	// Find the default gateway for this interface
	for _, route := range routes {
		if route.LinkIndex == iface.Index && route.Dst == nil {
			return route.Gw.String(), nil
		}
	}

	// If no default route found, try to use the jackpal/gateway package as fallback
	defaultGateway, err := gateway.DiscoverGateway()
	if err != nil {
		return "", fmt.Errorf("no gateway found: %w", err)
	}

	return defaultGateway.String(), nil
}

// checkForTollgate determines if an interface connects to a tollgate
func (c *Crowsnest) checkForTollgate(ifaceName string) (bool, string) {
	// Get the gateway IP address for the interface
	gatewayIP, err := c.getInterfaceGateway(ifaceName)
	if err != nil {
		log.Printf("Error getting gateway for interface %s: %v", ifaceName, err)
		return false, ""
	}

	if gatewayIP == "" {
		log.Printf("No gateway found for interface %s", ifaceName)
		return false, ""
	}

	// Try to access the merchant API at the gateway IP
	merchantURL := fmt.Sprintf("http://%s:2121", gatewayIP)

	// Create a client with a short timeout
	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Try to fetch the merchant details
	resp, err := client.Get(merchantURL)
	if err != nil {
		log.Printf("Failed to connect to merchant API at %s: %v", merchantURL, err)
		return false, ""
	}
	defer resp.Body.Close()

	// Check if we got a valid response
	if resp.StatusCode != http.StatusOK {
		log.Printf("Merchant API at %s returned status %d", merchantURL, resp.StatusCode)
		return false, ""
	}

	// Read a small portion of the response to verify it's a valid merchant API
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading merchant API response: %v", err)
		return false, ""
	}

	// Check if the response looks like a nostr event
	if !strings.Contains(string(body), "pubkey") || !strings.Contains(string(body), "kind") {
		log.Printf("Response from %s doesn't look like a merchant API", merchantURL)
		return false, ""
	}

	log.Printf("Found tollgate merchant API at %s", merchantURL)
	return true, merchantURL
}

// fetchMerchantAdvertisement fetches the merchant advertisement from a URL
func (c *Crowsnest) fetchMerchantAdvertisement(merchantURL string) (string, error) {
	// Create a client with a timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Fetch the merchant advertisement
	resp, err := client.Get(merchantURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch merchant advertisement: %w", err)
	}
	defer resp.Body.Close()

	// Check if we got a valid response
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("merchant API returned status %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading merchant API response: %w", err)
	}

	return string(body), nil
}

// parseMerchantAdvertisement parses a merchant advertisement to extract pricing and mint information
func (c *Crowsnest) parseMerchantAdvertisement(advertisementJSON string, ifaceName string) (NetworkInterface, error) {
	var event nostr.Event
	err := json.Unmarshal([]byte(advertisementJSON), &event)
	if err != nil {
		return NetworkInterface{}, fmt.Errorf("failed to parse advertisement JSON: %w", err)
	}

	if event.Kind != 21021 {
		return NetworkInterface{}, fmt.Errorf("invalid event kind: %d, expected 21021", event.Kind)
	}

	// Extract information from tags
	networkInterface := NetworkInterface{
		Name:          ifaceName,
		IsTollgate:    true,
		IsAvailable:   true,
		AcceptedMints: make([]string, 0),
	}

	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}

		switch tag[0] {
		case "metric":
			if len(tag) >= 2 {
				networkInterface.Metric = tag[1]
			}
		case "step_size":
			if len(tag) >= 2 {
				var stepSize uint64
				_, err := fmt.Sscanf(tag[1], "%d", &stepSize)
				if err == nil {
					networkInterface.StepSize = stepSize
				}
			}
		case "price_per_step":
			if len(tag) >= 2 {
				var pricePerStep uint64
				_, err := fmt.Sscanf(tag[1], "%d", &pricePerStep)
				if err == nil {
					networkInterface.PricePerStep = pricePerStep
				}
			}
		case "mint":
			if len(tag) >= 2 {
				networkInterface.AcceptedMints = append(networkInterface.AcceptedMints, tag[1])
			}
		}
	}

	return networkInterface, nil
}
