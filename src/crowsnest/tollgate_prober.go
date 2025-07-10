package crowsnest

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// tollGateProber implements the TollGateProber interface
type tollGateProber struct {
	config *CrowsnestConfig
	client *http.Client
}

// NewTollGateProber creates a new TollGate prober
func NewTollGateProber(config *CrowsnestConfig) TollGateProber {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: config.ProbeTimeout,
		// Don't follow redirects for security
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &tollGateProber{
		config: config,
		client: client,
	}
}

// ProbeGateway probes a gateway for TollGate advertisement on port 2121
func (tp *tollGateProber) ProbeGateway(gatewayIP string) ([]byte, error) {
	if gatewayIP == "" {
		return nil, fmt.Errorf("gateway IP is empty")
	}

	url := fmt.Sprintf("http://%s:2121/", gatewayIP)

	log.Printf("Probing gateway %s for TollGate advertisement", gatewayIP)

	var lastErr error

	// Retry logic
	for attempt := 0; attempt < tp.config.ProbeRetryCount; attempt++ {
		if attempt > 0 {
			log.Printf("Retry attempt %d for gateway %s", attempt, gatewayIP)
			time.Sleep(tp.config.ProbeRetryDelay)
		}

		data, err := tp.performRequest(url)
		if err == nil {
			log.Printf("Successfully received response from gateway %s", gatewayIP)
			return data, nil
		}

		lastErr = err
		log.Printf("Probe attempt %d failed for gateway %s: %v", attempt+1, gatewayIP, err)
	}

	return nil, fmt.Errorf("failed to probe gateway %s after %d attempts: %w",
		gatewayIP, tp.config.ProbeRetryCount, lastErr)
}

// performRequest performs a single HTTP request
func (tp *tollGateProber) performRequest(url string) ([]byte, error) {
	// Create request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set appropriate headers
	req.Header.Set("User-Agent", "TollGate-Crowsnest/1.0")
	req.Header.Set("Accept", "application/json")

	// Perform request
	resp, err := tp.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request returned status %d: %s", resp.StatusCode, resp.Status)
	}

	// Check content type (optional, but good practice)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && contentType != "application/json" {
		log.Printf("Warning: Gateway %s returned unexpected content type: %s", url, contentType)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if response is empty
	if len(body) == 0 {
		return nil, fmt.Errorf("received empty response from gateway")
	}

	// Basic size validation
	if len(body) > 1024*1024 { // 1MB limit
		return nil, fmt.Errorf("response too large: %d bytes", len(body))
	}

	return body, nil
}
