package upstream_session_manager

import (
	"context"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/nbd-wtf/go-nostr"
)

// UpstreamTollgate represents a discovered upstream TollGate
type UpstreamTollgate struct {
	// Network interface information
	InterfaceName  string // e.g., "eth0", "wlan0"
	MacAddressSelf string // MAC address of our own interface
	GatewayIP      string // IP address of the upstream gateway

	// TollGate advertisement information
	Advertisement *nostr.Event // Complete TollGate advertisement event (kind 10021)

	// Discovery metadata
	DiscoveredAt time.Time // When this TollGate was discovered
}

// KnownGateway tracks a gateway we've discovered
type KnownGateway struct {
	InterfaceName  string
	MacAddress     string
	GatewayIP      string
	LastChecked    time.Time
	LastCheckError error
}

// UpstreamSessionManagerSession represents an active session with an upstream TollGate
type UpstreamSessionManagerSession struct {
	// Original upstream tollgate object (contains all network info)
	UpstreamTollgate *UpstreamTollgate // Original upstream tollgate discovery object

	// Customer identity for this session
	CustomerPrivateKey string // Unique private key for this upstream connection

	// Advertisement and pricing (extracted as single unit to maintain consistency)
	Advertisement     *nostr.Event                         // Current advertisement (kind 10021)
	AdvertisementInfo *tollgate_protocol.AdvertisementInfo // Extracted advertisement data
	SelectedPricing   *tollgate_protocol.PricingOption     // Currently selected pricing option

	// Session state
	SessionEvent   *nostr.Event // Active session event (kind 1022)
	TotalAllotment uint64       // Total allotment purchased

	// Usage tracking
	UsageTracker  UsageTrackerInterface // Active usage tracker
	RenewalOffset uint64                // Renewal offset from allotment (e.g., 5000 bytes before limit)
	LastRenewalAt time.Time             // Last renewal timestamp

	// Session metadata
	CreatedAt     time.Time     // Session creation time
	LastPaymentAt time.Time     // Last payment timestamp
	TotalSpent    uint64        // Total sats spent on this session
	PaymentCount  int           // Number of payments made
	Status        SessionStatus // Active, Paused, Expired, etc.

	// Mutex for thread safety
	mu sync.RWMutex
}

// SessionStatus represents the status of a session
type SessionStatus int

const (
	SessionActive SessionStatus = iota
	SessionPaused
	SessionExpired
	SessionError
)

// PaymentProposal represents a payment proposal for an upstream TollGate
type PaymentProposal struct {
	UpstreamPubkey     string                           // Target upstream TollGate
	Steps              uint64                           // Number of steps to purchase
	PricingOption      *tollgate_protocol.PricingOption // Selected pricing option
	Reason             string                           // "initial", "renewal", "extension"
	EstimatedAllotment uint64                           // Expected allotment
}

// UpstreamSessionManagerInterface defines the interface for the upstream_session_manager module
type UpstreamSessionManagerInterface interface {
	// HandleGatewayConnected is called when UpstreamDetector discovers a gateway
	HandleGatewayConnected(interfaceName, macAddress, gatewayIP string) error

	// HandleDisconnect is called when a network interface goes down
	HandleDisconnect(interfaceName string) error

	// Management methods
	GetActiveSessions() map[string]*UpstreamSession

	// Control methods
	Stop() error
}

// UsageTrackerInterface defines the interface for usage tracking
type UsageTrackerInterface interface {
	// Start monitoring usage for the given session
	Start(session *UpstreamSessionManagerSession, usm UpstreamSessionManagerInterface) error

	// Stop monitoring and cleanup
	Stop() error

	// Get current usage amount
	GetCurrentUsage() uint64

	// Update usage amount (for external updates)
	UpdateUsage(amount uint64) error

	// Set renewal offset
	SetRenewalOffset(offset uint64) error

	// sessionChanged is called when the session is updated
	SessionChanged(session *UpstreamSessionManagerSession) error
}

// TollGateProber defines the interface for probing TollGate advertisements
type TollGateProber interface {
	ProbeGatewayWithContext(ctx context.Context, interfaceName, gatewayIP string) ([]byte, error)
	CancelProbesForInterface(interfaceName string)
	TriggerCaptivePortalSession(ctx context.Context, gatewayIP string) error
}

// TimeUsageTracker tracks time-based usage
type TimeUsageTracker struct {
	upstreamPubkey   string
	usm              UpstreamSessionManagerInterface
	startTime        time.Time
	pausedTime       time.Duration
	renewalOffset    uint64
	timer            *time.Timer
	done             chan bool
	totalAllotment   uint64
	currentIncrement uint64
	mu               sync.RWMutex
}

// DataUsageTracker tracks data-based usage
type DataUsageTracker struct {
	upstreamPubkey    string
	usm               UpstreamSessionManagerInterface
	interfaceName     string
	startBytes        uint64
	currentBytes      uint64
	renewalOffset     uint64
	renewalInProgress bool
	ticker            *time.Ticker
	done              chan bool
	totalAllotment    uint64
	currentIncrement  uint64
	mu                sync.RWMutex

	// Upstream polling fields
	upstreamIP         string
	upstreamUsage      uint64
	upstreamAllotment  uint64
	lastInfoLog        time.Time
	sessionEnded       bool      // Tracks if session has ended (-1/-1)
	lastRenewalAttempt time.Time // Prevents renewal storm
}

// UpstreamSessionManagerError represents errors specific to the upstream_session_manager module
type UpstreamSessionManagerError struct {
	Type           ErrorType
	Code           string
	Message        string
	Cause          error
	UpstreamPubkey string
	Context        map[string]interface{}
}

// ErrorType represents the type of upstream_session_manager error
type ErrorType int

const (
	ErrorTypeDiscovery ErrorType = iota
	ErrorTypeTrust
	ErrorTypeBudget
	ErrorTypePayment
	ErrorTypeSession
	ErrorTypeUsageTracking
)

func (e *UpstreamSessionManagerError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}
