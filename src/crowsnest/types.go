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

