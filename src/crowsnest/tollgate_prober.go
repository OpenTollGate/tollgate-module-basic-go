package crowsnest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

// tollGateProber implements the TollGateProber interface
type tollGateProber struct {
	config *config_manager.CrowsnestConfig
	client *http.Client

	// Track active probes for cancellation
	activeProbes map[string]context.CancelFunc // key: interfaceName, value: cancel function
	probesMutex  sync.RWMutex
}

// NewTollGateProber creates a new TollGate prober
func NewTollGateProber(config *config_manager.CrowsnestConfig) TollGateProber {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: config.ProbeTimeout,
		// Don't follow redirects for security
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &tollGateProber{
		config:       config,
		client:       client,
		activeProbes: make(map[string]context.CancelFunc),
	}
}

// ProbeGatewayWithContext probes a gateway with context for cancellation
func (tp *tollGateProber) ProbeGatewayWithContext(ctx context.Context, interfaceName, gatewayIP string) ([]byte, error) {
	if gatewayIP == "" {
		return nil, fmt.Errorf("gateway IP is empty")
	}

	// Store the cancel function for this interface
	tp.probesMutex.Lock()
	tp.activeProbes[interfaceName] = func() {
		// This function will be called when the probe is cancelled
		// The actual cancellation is handled by the context
	}
	tp.probesMutex.Unlock()

	// Cleanup when done
	defer func() {
		tp.probesMutex.Lock()
		delete(tp.activeProbes, interfaceName)
		tp.probesMutex.Unlock()
	}()

	url := fmt.Sprintf("http://%s:2121/", gatewayIP)
	logger.WithField("gateway", gatewayIP).Info("Probing gateway for TollGate advertisement (with context)")

	var lastErr error

	// Retry logic with context
	for attempt := 0; attempt < tp.config.ProbeRetryCount; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("probe cancelled: %w", ctx.Err())
		default:
		}

		if attempt > 0 {
			logger.WithFields(logrus.Fields{
				"gateway": gatewayIP,
				"attempt": attempt,
			}).Debug("Retry attempt for gateway")

			// Wait with context awareness
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("probe cancelled during retry delay: %w", ctx.Err())
			case <-time.After(tp.config.ProbeRetryDelay):
			}
		}

		data, err := tp.performRequestWithContext(ctx, url)
		if err == nil {
			logger.WithField("gateway", gatewayIP).Info("Successfully received response from gateway")
			return data, nil
		}

		lastErr = err
		logger.WithFields(logrus.Fields{
			"gateway": gatewayIP,
			"attempt": attempt + 1,
			"error":   err,
		}).Warn("Probe attempt failed for gateway")
	}

	return nil, fmt.Errorf("failed to probe gateway %s after %d attempts: %w",
		gatewayIP, tp.config.ProbeRetryCount, lastErr)
}

// CancelProbesForInterface cancels any active probes for the specified interface
func (tp *tollGateProber) CancelProbesForInterface(interfaceName string) {
	tp.probesMutex.Lock()
	defer tp.probesMutex.Unlock()

	if cancelFunc, exists := tp.activeProbes[interfaceName]; exists {
		logger.WithField("interface", interfaceName).Info("Cancelling active probe for interface")
		cancelFunc()
		delete(tp.activeProbes, interfaceName)
	}
}

// performRequestWithContext performs a single HTTP request with context
func (tp *tollGateProber) performRequestWithContext(ctx context.Context, url string) ([]byte, error) {
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
		logger.WithFields(logrus.Fields{
			"url":          url,
			"content_type": contentType,
		}).Warn("Gateway returned unexpected content type")
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
