package crowsnest

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/chandler"
)

// Crowsnest defines the main interface for the crowsnest module
type Crowsnest interface {
	Start() error
	Stop() error
	SetChandler(chandler chandler.ChandlerInterface)
}

// NetworkMonitor defines the interface for network monitoring
type NetworkMonitor interface {
	Start() error
	Stop() error
	Events() <-chan NetworkEvent
}

// TollGateProber defines the interface for probing TollGate advertisements
type TollGateProber interface {
	ProbeGateway(gatewayIP string) ([]byte, error)
}

// DiscoveryTracker defines the interface for tracking discovery attempts
type DiscoveryTracker interface {
	ShouldAttemptDiscovery(interfaceName, gatewayIP string) bool
	RecordDiscovery(interfaceName, gatewayIP string, result DiscoveryResult)
	Cleanup()
}
