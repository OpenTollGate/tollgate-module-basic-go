package mptcp_bonding

import (
	"strings"
	"testing"
)

// Running the tunnels on the device found a THIRD real defect: the tunnel
// interfaces were not accepted by the firewall, so the peer could not reach the
// tunnel addresses at all.
//
// Evidence from the router (OpenWrt 24.10.2, fw4/nftables):
//
//	chain input {
//	  type filter hook input priority filter; policy drop;
//	  iifname "br-lan"     jump input_lan
//	  iifname "lan1"       jump input_wan
//	  iifname "br-private" jump input_private
//	  jump handle_reject        <- anything else, including a wg tunnel, REJECTED
//	}
//
// On the wire this looked like the router actively refusing:
//
//	10.99.98.1 > 10.99.98.2: ICMP echo request
//	10.99.98.2 > 10.99.98.1: ICMP 10.99.98.2 protocol 1 port 47096 unreachable
//
// i.e. the tunnel transport was perfect and the router was replying
// "unreachable" -- indistinguishable, to the operator, from a broken tunnel.
// After adding an accept rule for the tunnel interfaces, both tunnel addresses
// answered 0% loss from the peer.
//
// The renderer must therefore emit the firewall acceptance, or the design
// silently requires an undocumented manual step.

func wgFirewallConfig() *Config {
	c := validConfig()
	c.Enabled = true
	c.Policy = DefaultPolicy
	c.Server = "203.0.113.10:65101"
	c.Method = "chacha20-ietf-poly1305"
	c.Password = "fixture"
	c.SteeringCIDR = "10.115.249.0/24"
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	c.Tunnels = []Tunnel{
		{Name: "mptcp-wan1", Interface: "lan1", LocalAddr: "192.168.2.15",
			PeerHost: "203.0.113.10", PeerPort: 51911,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.98.2/24", PrivateKey: "fixture",
			FwMark: 0x2001, Gateway: "192.168.2.1", Table: 201},
		{Name: "mptcp-wan2", Interface: "phy0-sta0", LocalAddr: "10.2.1.122",
			PeerHost: "203.0.113.10", PeerPort: 51912,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.97.2/24", PrivateKey: "fixture",
			FwMark: 0x2002, Gateway: "10.2.0.1", Table: 202},
	}
	return c
}

func TestTunnelFirewallScriptAcceptsEveryTunnel(t *testing.T) {
	// Every tunnel interface must be accepted, or its traffic is rejected and
	// the tunnel is useless despite a perfect handshake.
	c := wgFirewallConfig()
	script := c.TunnelFirewallScript()
	if script == "" {
		t.Fatal("a configured tunnel set must render firewall acceptance")
	}
	for _, tun := range c.Tunnels {
		if !strings.Contains(script, tun.Name) {
			t.Errorf("firewall script must accept the tunnel interface %s:\n%s", tun.Name, script)
		}
	}
}

func TestTunnelFirewallScriptIsIdempotent(t *testing.T) {
	// A reconcile can run many times; the rule must not accumulate duplicates.
	c := wgFirewallConfig()
	script := c.TunnelFirewallScript()
	if !strings.Contains(script, "delete") && !strings.Contains(script, "|| true") {
		t.Errorf("firewall commands must be idempotent or guarded:\n%s", script)
	}
}

func TestTunnelFirewallScriptEmptyWithoutTunnels(t *testing.T) {
	c := validConfig()
	if got := c.TunnelFirewallScript(); got != "" {
		t.Errorf("no tunnels must render no firewall script, got:\n%s", got)
	}
}
