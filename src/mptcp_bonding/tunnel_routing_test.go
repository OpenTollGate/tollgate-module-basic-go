package mptcp_bonding

import (
	"fmt"
	"strings"
	"testing"
)

// Running the rendered plan on the device found a real design bug: `wg set ...
// bind <addr>` is NOT a supported option.
//
//   $ wg set --help
//   Usage: wg set <interface> [listen-port <port>] [fwmark <mark>]
//              [private-key <file path>] [peer <base64 key> [...]
//              [endpoint <ip>:<port>] [persistent-keepalive <n>]
//              [allowed-ips <...>] ]
//   $ wg set mptcp-wan1 peer <key> ... bind 192.168.2.15
//   Invalid argument: bind
//
// There is no per-peer source-address binding. The real mechanism for pinning a
// tunnel to one uplink is **fwmark + policy routing**: give the tunnel a mark,
// add a default route in a dedicated table via that uplink's gateway, and a rule
// that sends marked traffic to that table.
//
// These tests pin the CORRECT mechanism so the invalid one cannot come back.

func wgTestConfigFixed() *Config {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	c.Tunnels = []Tunnel{
		{
			Name: "mptcp-wan1", Interface: "lan1", LocalAddr: "192.168.2.15",
			PeerHost: "203.0.113.10", PeerPort: 51901,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.98.2/24", PrivateKey: "fixture-key",
			FwMark: 0x2001, Gateway: "192.168.2.1", Table: 201,
		},
		{
			Name: "mptcp-wan2", Interface: "phy0-sta0", LocalAddr: "10.2.1.122",
			PeerHost: "203.0.113.10", PeerPort: 51902,
			PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
			Address:    "10.99.97.2/24", PrivateKey: "fixture-key",
			FwMark: 0x2002, Gateway: "10.2.0.1", Table: 202,
		},
	}
	return c
}

func TestTunnelPlanNeverUsesTheInvalidBindOption(t *testing.T) {
	// The exact command that failed on device. It must not be rendered again.
	c := wgTestConfigFixed()
	for _, cmd := range c.TunnelPlan() {
		if strings.Contains(cmd, " bind ") {
			t.Errorf("`wg set ... bind` is invalid on this wg version and FAILED on device; got:\n%s", cmd)
		}
	}
}

func TestTunnelPlanPinsViaFwmark(t *testing.T) {
	// fwmark is the supported mechanism, and it is what keeps the two tunnels on
	// separate uplinks. Without it both tunnels follow the default route and the
	// bond collapses onto one path.
	c := wgTestConfigFixed()
	for _, t2 := range c.Tunnels {
		ifaceMark := fmt.Sprintf("fwmark 0x%x", t2.FwMark)
		found := false
		for _, cmd := range c.TunnelPlan() {
			if strings.Contains(cmd, t2.Name) && strings.Contains(cmd, ifaceMark) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tunnel %s must set %s to pin it to its uplink:\n%v",
				t2.Name, ifaceMark, c.TunnelPlan())
		}
	}
}

func TestTunnelRoutingRulesAreRendered(t *testing.T) {
	// fwmark alone does nothing without policy routing to act on it. Each tunnel
	// needs: a dedicated table, a default route in it via its own gateway, and a
	// rule that directs marked traffic there.
	c := wgTestConfigFixed()
	script := c.TunnelRoutingScript()
	if script == "" {
		t.Fatal("tunnel routing script must be non-empty when tunnels are configured")
	}
	for _, t2 := range c.Tunnels {
		for _, want := range []string{
			fmt.Sprintf("table %d", t2.Table),
			fmt.Sprintf("via %s", t2.Gateway),
			fmt.Sprintf("fwmark 0x%x", t2.FwMark),
			t2.Name,
			// The route must egress the UPLINK. Naming the tunnel device here
			// failed on device with "Nexthop has invalid gateway".
			"dev " + t2.Interface,
		} {
			if !strings.Contains(script, want) {
				t.Errorf("routing script must contain %q:\n%s", want, script)
			}
		}
	}
}

func TestTunnelRoutingScriptIsIdempotent(t *testing.T) {
	// Reconcile runs repeatedly, so adding a rule or route twice must not fail.
	// Same discipline as the endpoint applier.
	c := wgTestConfigFixed()
	script := c.TunnelRoutingScript()
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ip rule add") || strings.HasPrefix(trimmed, "ip route add") {
			if !strings.Contains(trimmed, "|| true") && !strings.Contains(trimmed, "replace") {
				t.Errorf("routing commands must be idempotent or guarded; got %q", trimmed)
			}
		}
	}
}

func TestTunnelRequiresFwmarkAndGatewayToPin(t *testing.T) {
	// A tunnel with no fwmark/gateway cannot be pinned. Validation must reject it
	// rather than silently rendering a tunnel that will take the default route --
	// which would be a bond that connects twice through the same uplink.
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
	c.Tunnels = []Tunnel{{
		Name: "mptcp-wan1", Interface: "lan1", LocalAddr: "192.168.2.15",
		PeerHost: "203.0.113.10", PeerPort: 51901,
		PeerPubKey: "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZmdoaWo=",
		Address:    "10.99.98.2/24", PrivateKey: "fixture",
		// FwMark / Gateway / Table deliberately unset.
	}}
	if err := c.Validate(); err == nil {
		t.Fatal("a tunnel without fwmark/gateway/table cannot be pinned and must be rejected")
	}
}
