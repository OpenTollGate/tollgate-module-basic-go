package chandler

import (
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/nbd-wtf/go-nostr"
)

// UpstreamTollgate represents a discovered upstream TollGate
type UpstreamTollgate struct {
	// Network interface information
	InterfaceName string // e.g., "eth0", "wlan0"
	MacAddress    string // MAC address of local interface
	GatewayIP     string // IP address of the upstream gateway

	// TollGate advertisement information
	Advertisement *nostr.Event // Complete TollGate advertisement event (kind 10021)

	// Discovery metadata
	DiscoveredAt time.Time // When this TollGate was discovered
}

// ChandlerSession represents an active session with an upstream TollGate
type ChandlerSession struct {
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
	UsageTracker      UsageTrackerInterface // Active usage tracker
	RenewalThresholds []float64             // Renewal thresholds (e.g., 0.8)
	LastRenewalAt     time.Time             // Last renewal timestamp

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

// ChandlerInterface defines the interface for the chandler module
type ChandlerInterface interface {
	// HandleUpstreamTollgate is called when Crowsnest discovers a new upstream TollGate
	HandleUpstreamTollgate(upstream *UpstreamTollgate) error

	// HandleDisconnect is called when a network interface goes down
	HandleDisconnect(interfaceName string) error

	// HandleUpcomingRenewal is called by UsageTracker when renewal threshold reached
	HandleUpcomingRenewal(upstreamPubkey string, currentUsage uint64) error

	// Management methods
	GetActiveSessions() map[string]*ChandlerSession
	GetSessionByPubkey(pubkey string) (*ChandlerSession, error)

	// Control methods
	PauseSession(pubkey string) error
	ResumeSession(pubkey string) error
	TerminateSession(pubkey string) error
}

// UsageTrackerInterface defines the interface for usage tracking
type UsageTrackerInterface interface {
	// Start monitoring usage for the given session
	Start(session *ChandlerSession, chandler ChandlerInterface) error

	// Stop monitoring and cleanup
	Stop() error

	// Get current usage amount
	GetCurrentUsage() uint64

	// Update usage amount (for external updates)
	UpdateUsage(amount uint64) error

	// Set renewal thresholds
	SetRenewalThresholds(thresholds []float64) error
}

// TimeUsageTracker tracks time-based usage
type TimeUsageTracker struct {
	session    *ChandlerSession
	chandler   ChandlerInterface
	startTime  time.Time
	pausedTime time.Duration
	thresholds []float64
	timers     []*time.Timer
	done       chan bool
	mu         sync.RWMutex
}

// DataUsageTracker tracks data-based usage
type DataUsageTracker struct {
	session       *ChandlerSession
	chandler      ChandlerInterface
	interfaceName string
	startBytes    uint64
	currentBytes  uint64
	thresholds    []float64
	triggered     map[float64]bool
	ticker        *time.Ticker
	done          chan bool
	mu            sync.RWMutex
}

// ChandlerError represents errors specific to the chandler module
type ChandlerError struct {
	Type           ErrorType
	Code           string
	Message        string
	Cause          error
	UpstreamPubkey string
	Context        map[string]interface{}
}

// ErrorType represents the type of chandler error
type ErrorType int

const (
	ErrorTypeDiscovery ErrorType = iota
	ErrorTypeTrust
	ErrorTypeBudget
	ErrorTypePayment
	ErrorTypeSession
	ErrorTypeUsageTracking
)

func (e *ChandlerError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}
