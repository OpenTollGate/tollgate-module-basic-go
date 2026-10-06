package upstream_detector

import (
	"net"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
	"github.com/vishvananda/netlink"
)

// recordingUSM records every gateway the detector reports. The embedded
// interface supplies the remaining methods; only the ones the detector
// calls are overridden.
type recordingUSM struct {
	upstream_session_manager.UpstreamSessionManagerInterface
	reported []string
}

func (r *recordingUSM) HandleGatewayConnected(interfaceName, macAddress, gatewayIP string) error {
	r.reported = append(r.reported, interfaceName+"|"+gatewayIP)
	return nil
}

// fakeMonitor serves a fixed interface list and gateway map, so the scan
// loops can be exercised without a live netlink socket.
type fakeMonitor struct {
	NetworkMonitor
	ifaces []*InterfaceInfo
	gws    map[string]string
}

func (f *fakeMonitor) GetCurrentInterfaces() ([]*InterfaceInfo, error) { return f.ifaces, nil }

func (f *fakeMonitor) GetGatewayForInterface(name string) string { return f.gws[name] }

// TestIsUpstreamGatewayCandidate pins the contract that decides whether an
// interface's gateway is worth probing for an upstream TollGate.
//
// Regression: the detector used to consider every up interface with an
// address, including the bridges netifd creates for us. br-private
// (10.60.32.1/24) has no default route, so gateway selection fell through
// to netmask inference, which fabricates 10.60.32.254 — a host that does
// not exist. The session manager then probed
// http://10.60.32.254:2121/ three times per cycle forever
// ("no route to host") and kept a phantom gateway entry whose interface
// was the owner LAN.
func TestIsUpstreamGatewayCandidate(t *testing.T) {
	cases := []struct {
		name  string
		iface *InterfaceInfo
		want  bool
	}{
		{
			name:  "owner bridge br-private is not an upstream",
			iface: &InterfaceInfo{Name: "br-private", IsUp: true, IsBridge: true, IPAddresses: []string{"10.60.32.1"}},
			want:  false,
		},
		{
			name:  "captive bridge br-lan is not an upstream",
			iface: &InterfaceInfo{Name: "br-lan", IsUp: true, IsBridge: true, IPAddresses: []string{"192.168.1.1"}},
			want:  false,
		},
		{
			name:  "loopback is not an upstream",
			iface: &InterfaceInfo{Name: "lo", IsUp: true, IsLoopback: true, IPAddresses: []string{"127.0.0.1"}},
			want:  false,
		},
		{
			name:  "uplink STA with an address is an upstream",
			iface: &InterfaceInfo{Name: "phy1-sta0", IsUp: true, IPAddresses: []string{"192.168.2.24"}},
			want:  true,
		},
		{
			name:  "wan with an address is an upstream",
			iface: &InterfaceInfo{Name: "eth0", IsUp: true, IPAddresses: []string{"10.0.0.7"}},
			want:  true,
		},
		{
			name:  "down interface is not probed",
			iface: &InterfaceInfo{Name: "wan", IsUp: false, IPAddresses: []string{"10.0.0.7"}},
			want:  false,
		},
		{
			name:  "interface without an address is not probed",
			iface: &InterfaceInfo{Name: "eth1", IsUp: true},
			want:  false,
		},
		{
			name:  "nil interface is not probed",
			iface: nil,
			want:  false,
		},
	}
	for _, c := range cases {
		if got := c.iface.IsUpstreamGatewayCandidate(); got != c.want {
			t.Errorf("%s: IsUpstreamGatewayCandidate() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestGatewayInferenceOnBridgesYieldsPhantom documents *why* bridges must be
// excluded: netmask inference on a bridge's own address fabricates the .254
// host of its subnet, which nothing serves.
func TestGatewayInferenceOnBridgesYieldsPhantom(t *testing.T) {
	nm := &networkMonitor{}
	_, ipnet, err := net.ParseCIDR("10.60.32.1/24")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	got := nm.inferGatewayFromIP(net.ParseIP("10.60.32.1"), ipnet.Mask)
	if got != "10.60.32.254" {
		t.Fatalf("inferGatewayFromIP(10.60.32.1/24) = %q, want the phantom 10.60.32.254 (docs drift?)", got)
	}
}

// TestIsLANFabricLink covers the link-level guard used by
// getGatewayForInterface, which also protects the netlink event path (interfaces
// that come up get a gateway computed for them outside the scan loops).
func TestIsLANFabricLink(t *testing.T) {
	cases := []struct {
		name string
		link netlink.Link
		want bool
	}{
		{"bridge device (br-lan/br-private)", &netlink.Bridge{}, true},
		{"loopback", &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Flags: net.FlagLoopback}}, true},
		{"nil link", nil, true},
		{"uplink STA / ethernet", &netlink.Dummy{}, false},
	}
	for _, c := range cases {
		if got := isLANFabricLink(c.link); got != c.want {
			t.Errorf("%s: isLANFabricLink() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestScanSkipsBridgeGateways is the behavioural half: the detector's scan
// must report the real uplink gateway and nothing for our own bridges.
func TestScanSkipsBridgeGateways(t *testing.T) {
	mon := &fakeMonitor{
		ifaces: []*InterfaceInfo{
			{Name: "br-private", IsUp: true, IsBridge: true, MacAddress: "94:83:c4:8c:59:c3", IPAddresses: []string{"10.60.32.1"}},
			{Name: "br-lan", IsUp: true, IsBridge: true, MacAddress: "94:83:c4:8c:59:c2", IPAddresses: []string{"192.168.1.1"}},
			{Name: "phy1-sta0", IsUp: true, MacAddress: "92:83:c4:8c:59:c2", IPAddresses: []string{"192.168.2.24"}},
		},
		gws: map[string]string{
			"br-private": "10.60.32.254",
			"br-lan":     "192.168.1.254",
			"phy1-sta0":  "192.168.2.1",
		},
	}
	usm := &recordingUSM{}
	ud := &upstreamDetector{networkMonitor: mon}
	ud.SetUpstreamSessionManager(usm)

	ud.reportGatewaysForInterfaces()

	want := []string{"phy1-sta0|192.168.2.1"}
	if len(usm.reported) != len(want) || usm.reported[0] != want[0] {
		t.Fatalf("reported %v, want %v", usm.reported, want)
	}
}
