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

			// TEMPORARY WORKAROUND: Trigger captive portal session after successful probe
			// This ensures ndsctl creates a client session for our device
			go func() {
				err := tp.TriggerCaptivePortalSession(ctx, gatewayIP)
				if err != nil {
					logger.WithFields(logrus.Fields{
						"gateway": gatewayIP,
						"error":   err,
					}).Debug("Captive portal trigger failed (non-critical)")
				}
			}()

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

// TriggerCaptivePortalSession makes an HTTP GET request to port 80 to trigger ndsctl session creation
//
// TEMPORARY WORKAROUND: This is a temporary measure to ensure the upstream TollGate's ndsctl
// creates a client session for our device. This is NOT a long-term solution as it goes against
// the TollGate protocol specification.
//
// Background: After successful payment (port 2121), the upstream TollGate should automatically
// create the session. However, some implementations require a captive portal request (port 80)
// to trigger the ndsctl session creation.
//
// TODO: Remove this workaround once upstream TollGate implementations properly handle
// automatic session creation after payment validation.
func (tp *tollGateProber) TriggerCaptivePortalSession(ctx context.Context, gatewayIP string) error {
	if gatewayIP == "" {
		return fmt.Errorf("gateway IP is empty")
	}

	// Make HTTP GET request to port 80 (standard captive portal)
	url := fmt.Sprintf("http://%s:80/", gatewayIP)

	logger.WithFields(logrus.Fields{
		"gateway_ip":    gatewayIP,
		"url":           url,
		"purpose":       "trigger_ndsctl_session",
		"protocol_note": "TEMPORARY WORKAROUND - not part of TollGate protocol",
	}).Info("🚨 TEMPORARY: Triggering captive portal session for ndsctl")

	// Create request with context and short timeout
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create captive portal request: %w", err)
	}

	// Set headers that mimic a typical browser request
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TollGate-Client/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Connection", "close")

	// Use a separate client with shorter timeout for captive portal
	captiveClient := &http.Client{
		Timeout: 10 * time.Second, // Short timeout for captive portal
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow redirects for captive portal (common behavior)
			return nil
		},
	}

	// Perform the request
	resp, err := captiveClient.Do(req)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"gateway_ip": gatewayIP,
			"error":      err,
		}).Warn("Captive portal request failed (this is expected and non-critical)")
		// Don't return error - this is a best-effort attempt
		return nil
	}
	defer resp.Body.Close()

	// Read and discard response body (we don't need the content)
	_, _ = io.ReadAll(resp.Body)

	logger.WithFields(logrus.Fields{
		"gateway_ip":   gatewayIP,
		"status_code":  resp.StatusCode,
		"content_type": resp.Header.Get("Content-Type"),
	}).Info("✅ Captive portal request completed - ndsctl session should be triggered")

	return nil
}
