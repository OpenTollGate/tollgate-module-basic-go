package crowsnest

import (
	"time"
)

// NetworkEvent represents a network state change event
type NetworkEvent struct {
	Type          EventType
	InterfaceName string
	InterfaceInfo *InterfaceInfo
	GatewayIP     string
	Timestamp     time.Time
}

// EventType represents the type of network event
type EventType int

const (
	EventInterfaceUp EventType = iota
	EventInterfaceDown
	EventRouteAdded
	EventRouteDeleted
	EventAddressAdded
	EventAddressDeleted
)

// InterfaceInfo contains information about a network interface
type InterfaceInfo struct {
	Name           string
	MacAddress     string
	IPAddresses    []string
	IsUp           bool
	IsLoopback     bool
	IsPointToPoint bool
}

// CrowsnestError represents an error with additional context
type CrowsnestError struct {
	Type    ErrorType
	Code    string
	Message string
	Cause   error
	Context map[string]interface{}
}

// ErrorType represents different categories of errors
type ErrorType int

const (
	ErrorTypeNetwork ErrorType = iota
	ErrorTypeCommunication
	ErrorTypeValidation
	ErrorTypeIntegration
)

// Error implements the error interface
func (e *CrowsnestError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

// CrowsnestConfig holds configuration for the crowsnest module
type CrowsnestConfig struct {
	// Network monitoring settings
	MonitoringInterval time.Duration `json:"monitoring_interval"`

	// Probing settings
	ProbeTimeout    time.Duration `json:"probe_timeout"`
	ProbeRetryCount int           `json:"probe_retry_count"`
	ProbeRetryDelay time.Duration `json:"probe_retry_delay"`

	// Validation settings
	RequireValidSignature bool `json:"require_valid_signature"`

	// Logging settings
	LogLevel string `json:"log_level"`

	// Interface filtering
	IgnoreInterfaces []string `json:"ignore_interfaces"`
	OnlyInterfaces   []string `json:"only_interfaces"`

	// Discovery deduplication
	DiscoveryTimeout time.Duration `json:"discovery_timeout"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *CrowsnestConfig {
	return &CrowsnestConfig{
		MonitoringInterval:    5 * time.Second,
		ProbeTimeout:          10 * time.Second,
		ProbeRetryCount:       3,
		ProbeRetryDelay:       2 * time.Second,
		RequireValidSignature: true,
		LogLevel:              "INFO",
		IgnoreInterfaces:      []string{"lo", "docker0"},
		OnlyInterfaces:        []string{},
		DiscoveryTimeout:      300 * time.Second,
	}
}

// IsDebugLevel returns true if debug logging is enabled
func (c *CrowsnestConfig) IsDebugLevel() bool {
	return c.LogLevel == "DEBUG"
}

// DiscoveryAttempt tracks discovery attempts to prevent duplicates
type DiscoveryAttempt struct {
	InterfaceName string
	GatewayIP     string
	AttemptTime   time.Time
	Result        DiscoveryResult
}

// DiscoveryResult represents the result of a discovery attempt
type DiscoveryResult int

const (
	DiscoveryResultPending DiscoveryResult = iota
	DiscoveryResultSuccess
	DiscoveryResultNotTollGate
	DiscoveryResultValidationFailed
	DiscoveryResultError
)
