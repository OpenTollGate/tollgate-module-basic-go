package upstream_detector

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
)

// UpstreamDetector defines the main interface for the upstream_detector module
type UpstreamDetector interface {
	Start() error
	Stop() error
	SetUpstreamSessionManager(usm upstream_session_manager.UpstreamSessionManagerInterface)
}

// NetworkMonitor defines the interface for network monitoring
type NetworkMonitor interface {
	Start() error
	Stop() error
	Events() <-chan NetworkEvent
	GetCurrentInterfaces() ([]*InterfaceInfo, error)
	GetGatewayForInterface(interfaceName string) string
}
