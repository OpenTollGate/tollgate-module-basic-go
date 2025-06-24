package crowsnest

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// UpstreamPricing holds pricing information from an upstream router
type UpstreamPricing struct {
	Metric           string            `json:"metric"`
	StepSize         uint64            `json:"step_size"`
	PricePerStep     map[string]uint64 `json:"price_per_step"`     // mint_url -> price
	PriceUnit        map[string]string `json:"price_unit"`         // mint_url -> unit
	MinPurchaseSteps map[string]uint64 `json:"min_purchase_steps"` // mint_url -> min_steps
	AcceptedMints    []string          `json:"accepted_mints"`
}

// Crowsnest handles upstream router detection and pricing information gathering
type Crowsnest struct {
	currentUpstreamURL string
	lastDiscoveryTime  time.Time
	discoveryInterval  time.Duration
}

// New creates a new Crowsnest instance
func New() *Crowsnest {
	return &Crowsnest{
		discoveryInterval: 30 * time.Second, // Check every 30 seconds
	}
}

// DiscoverUpstreamRouter dynamically discovers upstream router IP address
// Uses system commands to find the default gateway and check if it's a tollgate
func (c *Crowsnest) DiscoverUpstreamRouter() (string, error) {
	log.Printf("Crowsnest: Starting upstream router discovery...")

	// Get the default gateway IP address
	gatewayIP, err := c.getDefaultGateway()
	if err != nil {
		log.Printf("Crowsnest: Failed to get default gateway: %v", err)
		return "", fmt.Errorf("failed to get default gateway: %w", err)
	}

	log.Printf("Crowsnest: Found default gateway: %s", gatewayIP)

	// Check if the gateway is a tollgate router
	url := fmt.Sprintf("http://%s:2121", gatewayIP)
	log.Printf("Crowsnest: Checking if %s is a tollgate router", url)

	if c.isUpstreamTollgate(url) {
		c.SetUpstreamURL(url)
		log.Printf("Crowsnest: Discovered upstream tollgate at %s", url)
		return url, nil
	}

	log.Printf("Crowsnest: Gateway %s is not a tollgate router", gatewayIP)
	return "", fmt.Errorf("no upstream tollgate found at gateway %s", gatewayIP)
}

// getDefaultGateway retrieves the default gateway IP address using system commands
func (c *Crowsnest) getDefaultGateway() (string, error) {
	// Try different methods to get the default gateway

	// Method 1: Try using ip route (common on Linux)
	if gateway, err := c.executeCommand("ip", "route", "show", "default"); err == nil {
		if ip := c.parseIPRoute(gateway); ip != "" {
			return ip, nil
		}
	}

	// Method 2: Try using route command (OpenWrt/older systems)
	if gateway, err := c.executeCommand("route", "-n"); err == nil {
		if ip := c.parseRouteTable(gateway); ip != "" {
			return ip, nil
		}
	}

	// Method 3: Try using netstat (fallback)
	if gateway, err := c.executeCommand("netstat", "-rn"); err == nil {
		if ip := c.parseNetstat(gateway); ip != "" {
			return ip, nil
		}
	}

	return "", fmt.Errorf("unable to determine default gateway")
}

// executeCommand runs a system command and returns the output
func (c *Crowsnest) executeCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// parseIPRoute parses the output of "ip route show default"
func (c *Crowsnest) parseIPRoute(output string) string {
	// Expected format: "default via 192.168.1.1 dev eth0"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "default") && strings.Contains(line, "via") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "via" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return ""
}

// parseRouteTable parses the output of "route -n"
func (c *Crowsnest) parseRouteTable(output string) string {
	// Expected format: Gateway column in route table
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "0.0.0.0") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1] // Gateway is typically the second column
			}
		}
	}
	return ""
}

// parseNetstat parses the output of "netstat -rn"
func (c *Crowsnest) parseNetstat(output string) string {
	// Similar to route table parsing
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "0.0.0.0") || strings.Contains(line, "default") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return ""
}

// isUpstreamTollgate checks if a URL hosts a tollgate by trying to get its advertisement
func (c *Crowsnest) isUpstreamTollgate(url string) bool {
	_, err := c.GetUpstreamAdvertisement(url)
	return err == nil
}

// SetUpstreamURL sets the upstream URL (used by connection logic that's out of scope)
func (c *Crowsnest) SetUpstreamURL(url string) {
	c.currentUpstreamURL = url
	c.lastDiscoveryTime = time.Now()
	log.Printf("Crowsnest: Upstream URL set to %s", url)
}

// GetUpstreamPricing retrieves pricing information from discovered upstream
func (c *Crowsnest) GetUpstreamPricing(upstreamURL string) (*UpstreamPricing, error) {
	if upstreamURL == "" {
		return nil, fmt.Errorf("no upstream URL provided")
	}

	log.Printf("Crowsnest: Fetching pricing information from %s", upstreamURL)

	// Get advertisement event from upstream
	advertisement, err := c.GetUpstreamAdvertisement(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get upstream advertisement: %w", err)
	}

	// Parse pricing information from advertisement
	pricing, err := c.parseAdvertisementToPricing(advertisement)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pricing from advertisement: %w", err)
	}

	log.Printf("Crowsnest: Successfully parsed pricing - Metric: %s, StepSize: %d, Mints: %d",
		pricing.Metric, pricing.StepSize, len(pricing.AcceptedMints))

	return pricing, nil
}

// GetUpstreamAdvertisement fetches the advertisement event (Kind 10021) from upstream
func (c *Crowsnest) GetUpstreamAdvertisement(upstreamURL string) (*nostr.Event, error) {
	if upstreamURL == "" {
		return nil, fmt.Errorf("no upstream URL provided")
	}

	log.Printf("Crowsnest: Fetching advertisement from %s", upstreamURL)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Make GET request to upstream router (TIP-03)
	resp, err := client.Get(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to upstream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse as Nostr event
	var event nostr.Event
	err = json.Unmarshal(body, &event)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Nostr event: %w", err)
	}

	// Verify it's a discovery event (Kind 10021)
	if event.Kind != 10021 {
		return nil, fmt.Errorf("expected kind 10021 (discovery), got %d", event.Kind)
	}

	// Verify event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		return nil, fmt.Errorf("invalid event signature")
	}

	log.Printf("Crowsnest: Successfully fetched and verified advertisement event from %s", upstreamURL)
	return &event, nil
}

// MonitorUpstreamConnection checks if upstream is still available
func (c *Crowsnest) MonitorUpstreamConnection() error {
	if c.currentUpstreamURL == "" {
		return fmt.Errorf("no upstream URL configured")
	}

	// Try to fetch advertisement to verify connection
	_, err := c.GetUpstreamAdvertisement(c.currentUpstreamURL)
	if err != nil {
		log.Printf("Crowsnest: Upstream connection check failed: %v", err)
		// Clear upstream URL if connection fails
		c.currentUpstreamURL = ""
		return fmt.Errorf("upstream connection lost: %w", err)
	}

	log.Printf("Crowsnest: Upstream connection to %s is healthy", c.currentUpstreamURL)
	return nil
}

// GetCurrentUpstreamURL returns the currently discovered upstream URL
func (c *Crowsnest) GetCurrentUpstreamURL() string {
	return c.currentUpstreamURL
}

// IsUpstreamAvailable checks if an upstream router is currently available
func (c *Crowsnest) IsUpstreamAvailable() bool {
	return c.currentUpstreamURL != ""
}

// parseAdvertisementToPricing converts a Nostr advertisement event to UpstreamPricing
func (c *Crowsnest) parseAdvertisementToPricing(event *nostr.Event) (*UpstreamPricing, error) {
	pricing := &UpstreamPricing{
		PricePerStep:     make(map[string]uint64),
		PriceUnit:        make(map[string]string),
		MinPurchaseSteps: make(map[string]uint64),
		AcceptedMints:    make([]string, 0),
	}

	// Parse tags according to TIP-01 and TIP-02
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}

		switch tag[0] {
		case "metric":
			pricing.Metric = tag[1]

		case "step_size":
			if len(tag) >= 2 {
				var stepSize uint64
				if _, err := fmt.Sscanf(tag[1], "%d", &stepSize); err == nil {
					pricing.StepSize = stepSize
				}
			}

		case "price_per_step":
			// Format: ["price_per_step", "cashu", price, unit, mint_url, min_steps]
			if len(tag) >= 6 && tag[1] == "cashu" {
				mintURL := tag[4]
				var price, minSteps uint64

				if _, err := fmt.Sscanf(tag[2], "%d", &price); err == nil {
					pricing.PricePerStep[mintURL] = price
				}

				pricing.PriceUnit[mintURL] = tag[3]

				if _, err := fmt.Sscanf(tag[5], "%d", &minSteps); err == nil {
					pricing.MinPurchaseSteps[mintURL] = minSteps
				}

				// Add to accepted mints if not already present
				found := false
				for _, mint := range pricing.AcceptedMints {
					if mint == mintURL {
						found = true
						break
					}
				}
				if !found {
					pricing.AcceptedMints = append(pricing.AcceptedMints, mintURL)
				}
			}
		}
	}

	// Validate required fields
	if pricing.Metric == "" {
		return nil, fmt.Errorf("metric not specified in advertisement")
	}
	if pricing.StepSize == 0 {
		return nil, fmt.Errorf("step_size not specified in advertisement")
	}
	if len(pricing.AcceptedMints) == 0 {
		return nil, fmt.Errorf("no accepted mints found in advertisement")
	}

	return pricing, nil
}

// StartMonitoring starts periodic monitoring of upstream connection
func (c *Crowsnest) StartMonitoring() {
	go func() {
		ticker := time.NewTicker(c.discoveryInterval)
		defer ticker.Stop()

		for range ticker.C {
			if c.IsUpstreamAvailable() {
				err := c.MonitorUpstreamConnection()
				if err != nil {
					log.Printf("Crowsnest: Lost upstream connection: %v", err)
				}
			}
		}
	}()
	log.Printf("Crowsnest: Started upstream monitoring with %v interval", c.discoveryInterval)
}
