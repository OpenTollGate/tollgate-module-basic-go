package lightning

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LNURLPayResponse represents the response from the LNURL-pay service
type LNURLPayResponse struct {
	Callback    string `json:"callback"`
	MaxSendable int64  `json:"maxSendable"` // millisatoshis
	MinSendable int64  `json:"minSendable"` // millisatoshis
	Metadata    string `json:"metadata"`
}

// LNURLInvoiceResponse is the response containing the invoice
type LNURLInvoiceResponse struct {
	PR            string        `json:"pr"` // Payment request (invoice)
	SuccessAction interface{}   `json:"successAction,omitempty"`
	Routes        []interface{} `json:"routes,omitempty"`
}

// GetInvoiceFromLightningAddress requests an invoice from a Lightning Address for a specific amount
func GetInvoiceFromLightningAddress(lightningAddr string, amountSats uint64) (string, error) {
	// 1. Parse the Lightning Address (user@domain.com)
	parts := strings.Split(lightningAddr, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Lightning Address format (expected user@domain.com): %s", lightningAddr)
	}
	username := parts[0]
	domain := parts[1]

	// 2. Form the well-known URL for Lightning Address
	wellKnownURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, username)

	// 3. Make initial request to the Lightning Address service
	resp, err := http.Get(wellKnownURL)
	if err != nil {
		return "", fmt.Errorf("failed to make request to Lightning Address service: %w", err)
	}
	defer resp.Body.Close()

	// 4. Parse the LNURL response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Lightning Address response: %w", err)
	}

	var lnurlPayResp LNURLPayResponse
	if err := json.Unmarshal(body, &lnurlPayResp); err != nil {
		return "", fmt.Errorf("failed to parse Lightning Address response: %w", err)
	}

	// 5. Check if amount is within allowed range
	amountMsat := int64(amountSats * 1000) // Convert to millisatoshis
	if amountMsat > lnurlPayResp.MaxSendable || amountMsat < lnurlPayResp.MinSendable {
		return "", fmt.Errorf("amount %d sats is outside allowed range (%d-%d msats)",
			amountSats, lnurlPayResp.MinSendable, lnurlPayResp.MaxSendable)
	}

	// 6. Request an invoice by calling the callback URL with the amount
	callbackURL, err := url.Parse(lnurlPayResp.Callback)
	if err != nil {
		return "", fmt.Errorf("invalid callback URL: %w", err)
	}

	// Add amount parameter
	q := callbackURL.Query()
	q.Set("amount", strconv.FormatInt(amountMsat, 10))
	callbackURL.RawQuery = q.Encode()

	// Make request to get the invoice
	invoiceResp, err := http.Get(callbackURL.String())
	if err != nil {
		return "", fmt.Errorf("failed to request invoice: %w", err)
	}
	defer invoiceResp.Body.Close()

	// 7. Parse the invoice response
	invoiceBody, err := io.ReadAll(invoiceResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read invoice response: %w", err)
	}

	var invoice LNURLInvoiceResponse
	if err := json.Unmarshal(invoiceBody, &invoice); err != nil {
		return "", fmt.Errorf("failed to parse invoice response: %w", err)
	}

	// 8. Return the payment request (invoice)
	if invoice.PR == "" {
		return "", fmt.Errorf("received empty invoice from Lightning Address service")
	}

	return invoice.PR, nil
}

// Capability represents a specific Lightning service capability
type Capability string

const (
	CapabilityLNURLPay       Capability = "lnurlpay"       // Basic LNURL-pay support
	CapabilityAmountRange    Capability = "amount_range"    // Supports min/max amount validation
	CapabilityMetadata       Capability = "metadata"        // Supports metadata field
	CapabilitySuccessAction  Capability = "success_action"  // Supports success actions
	CapabilityRouteHints     Capability = "route_hints"     // Provides route hints
	CapabilityFastResponse   Capability = "fast_response"   // Responds within acceptable time
	CapabilitySecureHTTPS    Capability = "secure_https"    // Uses HTTPS properly
)

// CapabilityLevel represents the level of capability support
type CapabilityLevel int

const (
	LevelUnsupported CapabilityLevel = iota
	LevelBasic
	LevelFull
)

// CapabilityResult represents the result of probing a specific capability
type CapabilityResult struct {
	Capability  Capability      `json:"capability"`
	Level       CapabilityLevel `json:"level"`
	Supported   bool            `json:"supported"`
	Details     string         `json:"details,omitempty"`
	ResponseTime int64         `json:"response_time_ms,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// CapabilityReport represents a complete capability probe report
type CapabilityReport struct {
	ServiceURL       string              `json:"service_url"`
	ServiceDomain    string              `json:"service_domain"`
	Username         string              `json:"username"`
	Timestamp        int64               `json:"timestamp"`
	OverallScore     float64             `json:"overall_score"`
	Capabilities     []CapabilityResult  `json:"capabilities"`
	GracefulFallback bool                `json:"graceful_fallback"`
	BasicSupport     bool                `json:"basic_support"`
	Recommendations  []string            `json:"recommendations"`
}

// ProbeOptions contains configuration for capability probing
type ProbeOptions struct {
	Timeout          int           `json:"timeout_ms"`           // Request timeout in milliseconds
	MaxResponseTime  int           `json:"max_response_time_ms"`  // Max acceptable response time
	TestAmount       uint64        `json:"test_amount_sats"`     // Amount to use for testing
	EnableDetailed   bool          `json:"enable_detailed"`      // Enable detailed capability testing
	AllowHTTP        bool          `json:"allow_http"`           // Allow non-HTTPS for testing
}

// DefaultProbeOptions returns sensible defaults for capability probing
func DefaultProbeOptions() *ProbeOptions {
	return &ProbeOptions{
		Timeout:         10000,  // 10 seconds
		MaxResponseTime: 5000,   // 5 seconds
		TestAmount:      1000,   // 1000 sats for testing
		EnableDetailed:  true,   // Enable detailed testing by default
		AllowHTTP:       false,  // Require HTTPS by default
	}
}

// ProbeCapabilities performs a comprehensive capability probe on a Lightning service
func ProbeCapabilities(lightningAddr string, options *ProbeOptions) (*CapabilityReport, error) {
	if options == nil {
		options = DefaultProbeOptions()
	}

	// Parse the Lightning Address
	parts := strings.Split(lightningAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid Lightning Address format (expected user@domain.com): %s", lightningAddr)
	}
	username := parts[0]
	domain := parts[1]

	report := &CapabilityReport{
		ServiceURL:       fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, username),
		ServiceDomain:    domain,
		Username:         username,
		Timestamp:        0, // Will be set during probe
		OverallScore:     0.0,
		Capabilities:     make([]CapabilityResult, 0),
		GracefulFallback: false,
		BasicSupport:     false,
		Recommendations:  make([]string, 0),
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: time.Duration(options.Timeout) * time.Millisecond,
	}

	// 1. Test basic LNURL-pay capability
	basicResult := probeBasicLNURLPay(report.ServiceURL, client)
	report.Capabilities = append(report.Capabilities, basicResult)
	
	if basicResult.Supported {
		report.BasicSupport = true
	} else {
		// If basic LNURL-pay fails, we can't proceed with other tests
		report.GracefulFallback = true
		report.Recommendations = append(report.Recommendations, 
			"Service does not support basic LNURL-pay - cannot proceed with Lightning payments")
		return report, nil
	}

	// 2. Parse the LNURL response for detailed testing
	lnurlResp, err := getLNURLResponse(report.ServiceURL, client)
	if err != nil {
		// Graceful degradation: we have basic support but can't test details
		report.GracefulFallback = true
		report.Recommendations = append(report.Recommendations, 
			fmt.Sprintf("Basic LNURL-pay supported, but detailed probing failed: %v", err))
		return report, nil
	}

	// 3. Test amount range capability
	amountResult := probeAmountRange(lnurlResp, options.TestAmount)
	report.Capabilities = append(report.Capabilities, amountResult)

	// 4. Test metadata capability
	metadataResult := probeMetadata(lnurlResp)
	report.Capabilities = append(report.Capabilities, metadataResult)

	// 5. Test secure HTTPS
	httpsResult := probeSecureHTTPS(report.ServiceURL)
	report.Capabilities = append(report.Capabilities, httpsResult)

	// 6. Test invoice generation (if detailed testing enabled)
	if options.EnableDetailed {
		invoiceResult := probeInvoiceGeneration(lnurlResp, options.TestAmount, client)
		report.Capabilities = append(report.Capabilities, invoiceResult)

		// Test success action and route hints from invoice response
		if invoiceResult.Supported {
			successActionResult := probeSuccessActionCapability(invoiceResult.Details)
			routeHintsResult := probeRouteHintsCapability(invoiceResult.Details)
			
			report.Capabilities = append(report.Capabilities, successActionResult, routeHintsResult)
		}
	}

	// Calculate overall score
	report.OverallScore = calculateOverallScore(report.Capabilities)
	
	// Determine graceful fallback recommendations
	report.GracefulFallback = shouldUseGracefulFallback(report.Capabilities)
	if report.GracefulFallback {
		report.Recommendations = append(report.Recommendations, 
			"Service has limited capabilities - consider graceful degradation approach")
	}

	return report, nil
}

// probeBasicLNURLPay tests if the service supports basic LNURL-pay
func probeBasicLNURLPay(serviceURL string, client *http.Client) CapabilityResult {
	start := time.Now()
	
	resp, err := client.Get(serviceURL)
	if err != nil {
		return CapabilityResult{
			Capability: CapabilityLNURLPay,
			Level:      LevelUnsupported,
			Supported:  false,
			ResponseTime: time.Since(start).Milliseconds(),
			Error:      fmt.Sprintf("HTTP request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	responseTime := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return CapabilityResult{
			Capability: CapabilityLNURLPay,
			Level:      LevelUnsupported,
			Supported:  false,
			ResponseTime: responseTime,
			Error:      fmt.Sprintf("HTTP status %d", resp.StatusCode),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CapabilityResult{
			Capability: CapabilityLNURLPay,
			Level:      LevelUnsupported,
			Supported:  false,
			ResponseTime: responseTime,
			Error:      fmt.Sprintf("Failed to read response: %v", err),
		}
	}

	var lnurlResp LNURLPayResponse
	if err := json.Unmarshal(body, &lnurlResp); err != nil {
		return CapabilityResult{
			Capability: CapabilityLNURLPay,
			Level:      LevelUnsupported,
			Supported:  false,
			ResponseTime: responseTime,
			Error:      fmt.Sprintf("Failed to parse JSON: %v", err),
		}
	}

	// Validate required fields
	if lnurlResp.Callback == "" || lnurlResp.MaxSendable == 0 || lnurlResp.MinSendable == 0 {
		return CapabilityResult{
			Capability: CapabilityLNURLPay,
			Level:      LevelUnsupported,
			Supported:  false,
			ResponseTime: responseTime,
			Details:    "Missing required fields in LNURL response",
		}
	}

	level := LevelBasic
	if responseTime < 2000 { // Fast response
		level = LevelFull
	}

	return CapabilityResult{
		Capability:  CapabilityLNURLPay,
		Level:       level,
		Supported:   true,
		ResponseTime: responseTime,
		Details:     fmt.Sprintf("Basic LNURL-pay supported, callback: %s", lnurlResp.Callback),
	}
}

// probeAmountRange tests if the service properly supports amount range validation
func probeAmountRange(lnurlResp *LNURLPayResponse, testAmount uint64) CapabilityResult {
	testAmountMsat := int64(testAmount * 1000)
	
	if testAmountMsat > lnurlResp.MaxSendable || testAmountMsat < lnurlResp.MinSendable {
		return CapabilityResult{
			Capability: CapabilityAmountRange,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    fmt.Sprintf("Test amount %d sats outside range (%d-%d msats)", 
				testAmount, lnurlResp.MinSendable, lnurlResp.MaxSendable),
		}
	}

	// Check for reasonable amount range
	rangeSize := lnurlResp.MaxSendable - lnurlResp.MinSendable
	minReasonableRange := int64(1000000) // 1000 sats minimum reasonable range
	
	if rangeSize < minReasonableRange {
		return CapabilityResult{
			Capability: CapabilityAmountRange,
			Level:      LevelBasic,
			Supported:  true,
			Details:    fmt.Sprintf("Limited amount range: %d-%d msats", lnurlResp.MinSendable, lnurlResp.MaxSendable),
		}
	}

	return CapabilityResult{
		Capability: CapabilityAmountRange,
		Level:      LevelFull,
		Supported:  true,
		Details:    fmt.Sprintf("Good amount range: %d-%d msats", lnurlResp.MinSendable, lnurlResp.MaxSendable),
	}
}

// probeMetadata tests if the service properly supports metadata
func probeMetadata(lnurlResp *LNURLPayResponse) CapabilityResult {
	if lnurlResp.Metadata == "" {
		return CapabilityResult{
			Capability: CapabilityMetadata,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "No metadata provided",
		}
	}

	// Check if metadata is valid JSON array
	var metadataArray []interface{}
	if err := json.Unmarshal([]byte("["+lnurlResp.Metadata+"]"), &metadataArray); err != nil {
		return CapabilityResult{
			Capability: CapabilityMetadata,
			Level:      LevelBasic,
			Supported:  true,
			Details:    "Metadata provided but format may be non-standard",
		}
	}

	return CapabilityResult{
		Capability: CapabilityMetadata,
		Level:      LevelFull,
		Supported:  true,
		Details:    "Valid metadata provided",
	}
}

// probeSecureHTTPS tests if the service uses proper HTTPS
func probeSecureHTTPS(serviceURL string) CapabilityResult {
	u, err := url.Parse(serviceURL)
	if err != nil {
		return CapabilityResult{
			Capability: CapabilitySecureHTTPS,
			Level:      LevelUnsupported,
			Supported:  false,
			Error:      fmt.Sprintf("Failed to parse URL: %v", err),
		}
	}

	if u.Scheme != "https" {
		return CapabilityResult{
			Capability: CapabilitySecureHTTPS,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "Service does not use HTTPS",
		}
	}

	return CapabilityResult{
		Capability: CapabilitySecureHTTPS,
		Level:      LevelFull,
		Supported:  true,
		Details:    "Service uses HTTPS",
	}
}

// probeInvoiceGeneration tests if the service can generate invoices
func probeInvoiceGeneration(lnurlResp *LNURLPayResponse, testAmount uint64, client *http.Client) CapabilityResult {
	start := time.Now()
	
	// Create callback URL with test amount
	callbackURL, err := url.Parse(lnurlResp.Callback)
	if err != nil {
		return CapabilityResult{
			Capability:  "invoice_generation", // Not in const list, using string
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: time.Since(start).Milliseconds(),
			Error:       fmt.Sprintf("Invalid callback URL: %v", err),
		}
	}

	testAmountMsat := int64(testAmount * 1000)
	q := callbackURL.Query()
	q.Set("amount", strconv.FormatInt(testAmountMsat, 10))
	callbackURL.RawQuery = q.Encode()

	// Make request to get invoice
	resp, err := client.Get(callbackURL.String())
	if err != nil {
		return CapabilityResult{
			Capability:  "invoice_generation",
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: time.Since(start).Milliseconds(),
			Error:       fmt.Sprintf("Failed to request invoice: %v", err),
		}
	}
	defer resp.Body.Close()

	responseTime := time.Since(start).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return CapabilityResult{
			Capability:  "invoice_generation",
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: responseTime,
			Error:       fmt.Sprintf("HTTP status %d when requesting invoice", resp.StatusCode),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CapabilityResult{
			Capability:  "invoice_generation",
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: responseTime,
			Error:       fmt.Sprintf("Failed to read invoice response: %v", err),
		}
	}

	var invoiceResp LNURLInvoiceResponse
	if err := json.Unmarshal(body, &invoiceResp); err != nil {
		return CapabilityResult{
			Capability:  "invoice_generation",
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: responseTime,
			Error:       fmt.Sprintf("Failed to parse invoice response: %v", err),
		}
	}

	if invoiceResp.PR == "" {
		return CapabilityResult{
			Capability:  "invoice_generation",
			Level:       LevelUnsupported,
			Supported:   false,
			ResponseTime: responseTime,
			Details:     "Empty payment request received",
		}
	}

	// Return the JSON response as details for further analysis
	jsonDetails := string(body)
	level := LevelBasic
	if responseTime < 3000 { // Fast invoice generation
		level = LevelFull
	}

	return CapabilityResult{
		Capability:  "invoice_generation",
		Level:       level,
		Supported:   true,
		ResponseTime: responseTime,
		Details:     jsonDetails,
	}
}

// probeSuccessActionCapability tests if the service supports success actions
func probeSuccessActionCapability(invoiceDetails string) CapabilityResult {
	var invoice LNURLInvoiceResponse
	if err := json.Unmarshal([]byte(invoiceDetails), &invoice); err != nil {
		return CapabilityResult{
			Capability: CapabilitySuccessAction,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "Failed to parse invoice details",
		}
	}

	if invoice.SuccessAction == nil {
		return CapabilityResult{
			Capability: CapabilitySuccessAction,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "No success action provided",
		}
	}

	return CapabilityResult{
		Capability: CapabilitySuccessAction,
		Level:      LevelFull,
		Supported:  true,
		Details:    "Success action supported",
	}
}

// probeRouteHintsCapability tests if the service provides route hints
func probeRouteHintsCapability(invoiceDetails string) CapabilityResult {
	var invoice LNURLInvoiceResponse
	if err := json.Unmarshal([]byte(invoiceDetails), &invoice); err != nil {
		return CapabilityResult{
			Capability: CapabilityRouteHints,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "Failed to parse invoice details",
		}
	}

	if len(invoice.Routes) == 0 {
		return CapabilityResult{
			Capability: CapabilityRouteHints,
			Level:      LevelUnsupported,
			Supported:  false,
			Details:    "No route hints provided",
		}
	}

	return CapabilityResult{
		Capability: CapabilityRouteHints,
		Level:      LevelFull,
		Supported:  true,
		Details:    fmt.Sprintf("Route hints provided: %d routes", len(invoice.Routes)),
	}
}

// calculateOverallScore calculates an overall capability score (0.0 to 1.0)
func calculateOverallScore(capabilities []CapabilityResult) float64 {
	if len(capabilities) == 0 {
		return 0.0
	}

	totalScore := 0.0
	weight := 1.0 / float64(len(capabilities))

	for _, cap := range capabilities {
		if !cap.Supported {
			continue // No points for unsupported capabilities
		}

		switch cap.Level {
		case LevelFull:
			totalScore += weight * 1.0
		case LevelBasic:
			totalScore += weight * 0.7
		}
	}

	return totalScore
}

// shouldUseGracefulFallback determines if graceful fallback should be used
func shouldUseGracefulFallback(capabilities []CapabilityResult) bool {
	criticalCapabilities := []Capability{
		CapabilityLNURLPay,
		CapabilityAmountRange,
		CapabilitySecureHTTPS,
	}

	supportedCritical := 0
	for _, criticalCap := range criticalCapabilities {
		for _, cap := range capabilities {
			if cap.Capability == criticalCap && cap.Supported {
				supportedCritical++
				break
			}
		}
	}

	// Use graceful fallback if less than 2 critical capabilities are supported
	return supportedCritical < 2
}

// getLNURLResponse is a helper function to get and parse LNURL response
func getLNURLResponse(serviceURL string, client *http.Client) (*LNURLPayResponse, error) {
	resp, err := client.Get(serviceURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var lnurlResp LNURLPayResponse
	if err := json.Unmarshal(body, &lnurlResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &lnurlResp, nil
}
