package crowsnest

import (
	"context"

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
	GetCurrentInterfaces() ([]*InterfaceInfo, error)
	GetGatewayForInterface(interfaceName string) string
}

// TollGateProber defines the interface for probing TollGate advertisements
type TollGateProber interface {
	ProbeGatewayWithContext(ctx context.Context, interfaceName, gatewayIP string) ([]byte, error)
	CancelProbesForInterface(interfaceName string)
}

// DiscoveryTracker defines the interface for tracking discovery attempts
type DiscoveryTracker interface {
	ShouldAttemptDiscovery(interfaceName, gatewayIP string) bool
	RecordDiscovery(interfaceName, gatewayIP string, result DiscoveryResult)
	ClearInterface(interfaceName string)
	Cleanup()
}
