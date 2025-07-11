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
