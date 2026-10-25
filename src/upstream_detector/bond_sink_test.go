package upstream_detector

import "testing"

// The detector already tells the upstream session manager about interface
// events. The MPTCP bond needs the same events so it can add a subflow when an
// uplink appears and withdraw it when it goes away.
//
// The bond is wired in through a narrow local interface (InterfaceEventSink)
// rather than importing mptcp_bonding directly, so:
//   * upstream_detector keeps no dependency on the bonding module,
//   * the call-through is testable with a fake sink,
//   * a node without a bond passes no sink and nothing changes.

type fakeSink struct {
	ups     []string
	downs   []string
	addrs   []string
	deleted []string
}

func (f *fakeSink) HandleInterfaceUp(iface, addr string) error {
	f.ups = append(f.ups, iface+"|"+addr)
	return nil
}
func (f *fakeSink) HandleInterfaceDown(iface string) error {
	f.downs = append(f.downs, iface)
	return nil
}
func (f *fakeSink) HandleAddressAdded(iface, addr string) error {
	f.addrs = append(f.addrs, iface+"|"+addr)
	return nil
}
func (f *fakeSink) HandleAddressDeleted(iface, addr string) error {
	f.deleted = append(f.deleted, iface+"|"+addr)
	return nil
}

func newTestDetector(t *testing.T, sink InterfaceEventSink) *upstreamDetector {
	t.Helper()
	return &upstreamDetector{bondSink: sink}
}

func TestDetectorForwardsInterfaceUp(t *testing.T) {
	fs := &fakeSink{}
	d := newTestDetector(t, fs)
	d.handleInterfaceUp(NetworkEvent{
		InterfaceName: "lan1",
		GatewayIP:     "192.168.2.1",
		InterfaceInfo: &InterfaceInfo{},
	})
	if len(fs.ups) != 1 || fs.ups[0] != "lan1|192.168.2.1" {
		t.Errorf("interface-up must reach the bond sink with its address, got %v", fs.ups)
	}
}

func TestDetectorForwardsInterfaceDown(t *testing.T) {
	fs := &fakeSink{}
	d := newTestDetector(t, fs)
	d.handleInterfaceDown(NetworkEvent{InterfaceName: "phy0-sta0"})
	if len(fs.downs) != 1 || fs.downs[0] != "phy0-sta0" {
		t.Errorf("interface-down must reach the bond sink, got %v", fs.downs)
	}
}

func TestDetectorForwardsAddressAdded(t *testing.T) {
	fs := &fakeSink{}
	d := newTestDetector(t, fs)
	d.handleAddressAdded(NetworkEvent{
		InterfaceName: "phy0-sta0",
		GatewayIP:     "10.2.0.1",
		InterfaceInfo: &InterfaceInfo{},
	})
	if len(fs.addrs) != 1 || fs.addrs[0] != "phy0-sta0|10.2.0.1" {
		t.Errorf("address-added must reach the bond sink, got %v", fs.addrs)
	}
}

func TestDetectorForwardsAddressDeleted(t *testing.T) {
	fs := &fakeSink{}
	d := newTestDetector(t, fs)
	d.handleAddressDeleted(NetworkEvent{InterfaceName: "phy0-sta0"})
	if len(fs.deleted) != 1 {
		t.Errorf("address-deleted must reach the bond sink, got %v", fs.deleted)
	}
}

func TestDetectorWithoutSinkIsInert(t *testing.T) {
	// Every node that has not opted into a bond passes no sink. The detector
	// must behave exactly as before -- no nil panic on any event path.
	d := newTestDetector(t, nil)
	d.handleInterfaceUp(NetworkEvent{InterfaceName: "lan1", GatewayIP: "192.168.2.1", InterfaceInfo: &InterfaceInfo{}})
	d.handleInterfaceDown(NetworkEvent{InterfaceName: "lan1"})
	d.handleAddressAdded(NetworkEvent{InterfaceName: "lan1", GatewayIP: "192.168.2.1", InterfaceInfo: &InterfaceInfo{}})
	d.handleAddressDeleted(NetworkEvent{InterfaceName: "lan1"})
}

func TestSetBondReconcilerIsExposed(t *testing.T) {
	// The interface must offer a setter, mirroring SetUpstreamSessionManager,
	// so main.go can wire the bond without a constructor change.
	var d UpstreamDetector
	d = &upstreamDetector{}
	d.SetBondReconciler(&fakeSink{})
}
