package chandler

import (
	"time"

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

// ChandlerInterface defines the interface for the chandler module
type ChandlerInterface interface {
	// HandleUpstreamTollgate is called when Crowsnest discovers a new upstream TollGate
	// The chandler should:
	// - Store the upstream TollGate information
	// - Establish connection management
	// - Handle session management with the upstream TollGate
	// - Return error if the upstream TollGate cannot be handled
	HandleUpstreamTollgate(upstream *UpstreamTollgate) error

	// HandleDisconnect is called when a network interface goes down
	// The chandler should:
	// - Clean up any sessions or connections for this interface
	// - Release any resources associated with this interface
	// - Update connection state tracking
	HandleDisconnect(interfaceName string) error
}
