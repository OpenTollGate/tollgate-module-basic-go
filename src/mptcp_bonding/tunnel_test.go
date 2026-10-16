package mptcp_bonding

import (
	"strings"
	"testing"
)

// A tunnel per uplink turns the NAT problem into a solved one.
//
// MPTCP's second subflow needs the PEER to connect BACK to an advertised
// address. Behind CGNAT no advertised address is reachable, so the subflow
// never establishes (observed: MPJoinNoTokenFound=58 on the peer). A WireGuard
// tunnel fixes this WITHOUT depending on what the uplink hands out: the router
// INITIATES outbound to a public endpoint, and the tunnel address is what gets
// advertised -- reachable by the peer because the peer is a party to the tunnel.
//
// Each uplink gets its OWN tunnel, and the tunnel is pinned to that uplink by
// binding its endpoint to the uplink's source address. That is what keeps the
// two underlay paths genuinely separate: without the binding, both tunnels would
// take the default route and collapse onto one uplink, and the bond would be
// pointless.
//
// This file renders the config; it never executes wg. Same contract as the rest
// of the package: pure functions, testable, no router required.

func wgTestConfig() *Config {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	c.Tunnels = []Tunnel{
		{
			Name:       "mptcp-wan1",
			Interface:  "lan1",
			LocalAddr:  "192.168.2.15",
			PeerHost:   "203.0.113.10",
			PeerPort:   51821,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.99.101/32",
			PrivateKey: "Zml4dHVyZS1rZXktbm90LWEtc2VjcmV0",
		},
		{
			Name:       "mptcp-wan2",
			Interface:  "phy0-sta0",
			LocalAddr:  "10.2.1.122",
			PeerHost:   "203.0.113.10",
			PeerPort:   51822,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.99.102/32",
			PrivateKey: "Zml4dHVyZS1rZXktbm90LWEtc2VjcmV0",
		},
	}
	return c
}

func TestTunnelPlanEmitsOnePerUplink(t *testing.T) {
	c := wgTestConfig()
	plan := c.TunnelPlan()
	if len(plan) == 0 {
		t.Fatal("expected a non-empty tunnel plan")
	}
	// The plan is a SEQUENCE of commands per tunnel, not one line per tunnel,
	// so assert on the per-tunnel content rather than the raw count.
	joined := strings.Join(plan, "\n")
	for _, name := range []string{"mptcp-wan1", "mptcp-wan2"} {
		if !strings.Contains(joined, name) {
			t.Errorf("plan must bring up %s:\n%s", name, joined)
		}
	}
	// One `ip link add` per tunnel is the unambiguous count.
	adds := 0
	for _, cmd := range plan {
		if strings.Contains(cmd, "ip link add") {
			adds++
		}
	}
	if adds != 2 {
		t.Errorf("expected exactly one link created per tunnel, got %d", adds)
	}
}

func TestTunnelBindsEndpointToItsUplink(t *testing.T) {
	// THE critical property. Without `bind` to the uplink's own source address,
	// both tunnels would follow the default route and collapse onto one uplink,
	// which defeats the entire point of the bond.
	c := wgTestConfig()
	for i, t2 := range c.Tunnels {
		expected := t2.LocalAddr
		found := false
		for _, cmd := range c.TunnelPlan() {
			if strings.Contains(cmd, "bind "+expected) && strings.Contains(cmd, t2.Name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tunnel %d (%s) must bind its endpoint to %s",
				i, t2.Name, expected)
		}
	}
}

func TestTunnelUsesPersistentKeepalive(t *testing.T) {
	// Behind CGNAT there is no inbound path, so the tunnel must keep the NAT
	// mapping alive from our side. Without a keepalive the mapping expires and
	// the peer cannot reach the tunnel address -- the exact failure this design
	// exists to avoid.
	c := wgTestConfig()
	for _, cmd := range c.TunnelPlan() {
		// Only the peer command carries the keepalive, so check it there rather
		// than requiring it on every line of the plan.
		if !strings.Contains(cmd, "wg set") || !strings.Contains(cmd, "peer") {
			continue
		}
		if !strings.Contains(cmd, "persistent-keepalive") {
			t.Errorf("a NAT-ed tunnel needs persistent-keepalive, got:\n%s", cmd)
		}
	}
}

func TestTunnelPrivateKeyIsNotRenderedInPlan(t *testing.T) {
	// The plan is the part that may be logged or shown. Keys must be written by
	// a separate, private step so they never land in a log or a PR.
	c := wgTestConfig()
	for _, cmd := range c.TunnelPlan() {
		if strings.Contains(cmd, c.Tunnels[0].PrivateKey) {
			t.Errorf("the tunnel plan must NOT contain the private key:\n%s", cmd)
		}
	}
}

func TestTunnelRendersConfigFileNotJustCommands(t *testing.T) {
	// wg-quick needs the config on disk before `up`, and a router reboots. So the
	// design emits a real UCI-shaped config, not just one-shot commands.
	c := wgTestConfig()
	files := c.TunnelConfigFiles()
	if len(files) != 2 {
		t.Fatalf("expected one config file per tunnel, got %d", len(files))
	}
	for name, body := range files {
		if !strings.Contains(name, "mptcp-wan") {
			t.Errorf("config name should identify the tunnel, got %q", name)
		}
		for _, want := range []string{"[Interface]", "[Peer]", "PrivateKey", "PublicKey", "Endpoint", "AllowedIPs"} {
			if !strings.Contains(body, want) {
				t.Errorf("config %s missing %q:\n%s", name, want, body)
			}
		}
	}
}

func TestTunnelPrerequisitesAreStockPackages(t *testing.T) {
	// Must be installable from the official feeds, version-matched to the kernel.
	// Verified available on OpenWrt 24.10.2 / kernel 6.6.93.
	c := wgTestConfig()
	pkgs := c.TunnelPrerequisites()
	joined := strings.Join(pkgs, " ")
	for _, want := range []string{"kmod-wireguard", "wireguard-tools"} {
		if !strings.Contains(joined, want) {
			t.Errorf("prerequisites must include %s, got %v", want, pkgs)
		}
	}
}

func TestTunnelPlanEmptyWhenDisabled(t *testing.T) {
	// A node with no tunnel config must render nothing and not break the applier.
	c := validConfig()
	if got := c.TunnelPlan(); len(got) != 0 {
		t.Errorf("no tunnels configured must render no plan, got %v", got)
	}
	if got := c.TunnelConfigFiles(); len(got) != 0 {
		t.Errorf("no tunnels configured must render no config files, got %v", got)
	}
}

func TestTunnelAddressesAreBareAndSorted(t *testing.T) {
	// Tunnel addresses go into `ip mptcp endpoint add`, which takes a bare
	// address with NO prefix length. These are the addresses the peer can reach
	// through the tunnels -- the fix for the JOIN failures.
	c := wgTestConfig()
	got := c.TunnelAddresses()
	if len(got) != 2 {
		t.Fatalf("expected one address per tunnel, got %v", got)
	}
	for _, a := range got {
		if strings.Contains(a, "/") {
			t.Errorf("tunnel address must be bare (no prefix length), got %q", a)
		}
	}
	if got[0] != "10.99.99.101" || got[1] != "10.99.99.102" {
		t.Errorf("addresses must be sorted and stripped: %v", got)
	}
}

func TestListenPortsAreStableAndDistinct(t *testing.T) {
	// Stable so the tunnel is identifiable; distinct so two tunnels on one
	// router cannot fight over the same local port.
	c := wgTestConfig()
	a := listenPortFor(c.Tunnels[0])
	b := listenPortFor(c.Tunnels[1])
	if a != listenPortFor(c.Tunnels[0]) {
		t.Error("listen port must be stable for the same tunnel")
	}
	if a == b {
		t.Errorf("distinct tunnels must not share a listen port (%d == %d)", a, b)
	}
}

func TestKeepaliveDefaultsWhenUnset(t *testing.T) {
	tr := Tunnel{Name: "t", PeerPort: 51821}
	if tr.keepalive() != DefaultKeepaliveSeconds {
		t.Errorf("unset keepalive must default to %d, got %d",
			DefaultKeepaliveSeconds, tr.keepalive())
	}
	tr.Keepalive = 15
	if tr.keepalive() != 15 {
		t.Errorf("explicit keepalive must be honoured, got %d", tr.keepalive())
	}
}
