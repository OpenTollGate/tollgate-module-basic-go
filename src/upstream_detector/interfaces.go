package upstream_detector

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
)

// UpstreamDetector defines the main interface for the upstream_detector module
type UpstreamDetector interface {
	Start() error
	Stop() error
	SetUpstreamSessionManager(usm upstream_session_manager.UpstreamSessionManagerInterface)
	// SetBondReconciler wires the MPTCP bond to the interface events this
	// detector already observes. Pass nil for a node without a bond.
	SetBondReconciler(sink InterfaceEventSink)
}

// InterfaceEventSink receives the interface/address events this detector
// observes, so the MPTCP bond can keep its endpoints in step with reality: add
// a subflow when an uplink appears, withdraw it when the uplink goes away.
//
// It is a local interface rather than a concrete *mptcp_bonding.Reconciler for
// two reasons: upstream_detector then carries no dependency on the bonding
// module, and the call-through is testable with a plain fake. The method set
// deliberately matches mptcp_bonding.Reconciler's exactly -- including the
// error return, because applying the bond can fail (missing `ip-full`, a
// rejected endpoint) and the detector must log that rather than drop it.
type InterfaceEventSink interface {
	HandleInterfaceUp(iface, addr string) error
	HandleInterfaceDown(iface string) error
	HandleAddressAdded(iface, addr string) error
	HandleAddressDeleted(iface, addr string) error
}

// NetworkMonitor defines the interface for network monitoring
type NetworkMonitor interface {
	Start() error
	Stop() error
	Events() <-chan NetworkEvent
	GetCurrentInterfaces() ([]*InterfaceInfo, error)
	GetGatewayForInterface(interfaceName string) string
}
