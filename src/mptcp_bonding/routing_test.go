package mptcp_bonding

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func routingFixture() *Config {
	return &Config{
		Enabled:      true,
		Policy:       DefaultPolicy,
		Server:       "203.0.113.10:65101",
		Method:       "chacha20-ietf-poly1305",
		Password:     "fixture",
		SteeringCIDR: "10.115.249.0/24",
		FwMarkTable:  100,
		Uplinks: []Uplink{
			{Interface: "eth1", Address: "192.0.2.10", Gateway: "192.0.2.1"},
			{Interface: "phy1-sta0", Address: "198.51.100.7", Gateway: "198.51.100.1"},
		},
	}
}

func TestPolicyRoutingPinsEveryIPv4Uplink(t *testing.T) {
	c := routingFixture()
	script := c.PolicyRoutingScript()
	if script == "" {
		t.Fatal("policy routing script must be rendered for a non-tunnel bond")
	}
	for _, want := range []string{
		"TABLE=101; IFACE=eth1; ADDR=192.0.2.10; PREF=100; METRIC=10",
		"TABLE=102; IFACE=phy1-sta0; ADDR=198.51.100.7; PREF=101; METRIC=11",
		`ip route replace default via "$GW" dev "$IFACE" table "$TABLE"`,
		`ip route replace default via "$GW" dev "$IFACE" metric "$METRIC"`,
		`ip route del default via "$GW" dev "$IFACE" metric 0`,
		`ip rule add from "$ADDR"/32 lookup "$TABLE" pref "$PREF"`,
		`ip route replace "$ADDR/32" dev "$IFACE" table "$TABLE"`,
		"scope link",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("policy routing script missing %q:\n%s", want, script)
		}
	}
}

func TestPolicyRoutingIsIdempotent(t *testing.T) {
	script := routingFixture().PolicyRoutingScript()
	// Every mutating command is either a replace or preceded by a delete.
	for _, want := range []string{
		`ip rule del from "$ADDR"/32 lookup "$TABLE"`,
		`ip route replace "$CONNECTED"`,
		`ip route replace "$ADDR/32"`,
		`ip route replace default`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("idempotence guard missing %q:\n%s", want, script)
		}
	}
}

func TestPolicyRoutingSkippedWhenTunnelsOwnThePinning(t *testing.T) {
	c := routingFixture()
	c.Tunnels = []Tunnel{{
		Name: "mptcp-wan1", Interface: "eth1", FwMark: 0x3001,
		Gateway: "192.0.2.1", Table: 301, PeerHost: "203.0.113.10", PeerPort: 51911,
		PeerPubKey: "peer", Address: "10.99.98.2/32",
	}}
	if got := c.PolicyRoutingScript(); got != "" {
		t.Fatalf("tunnel bonds already pin by fwmark; policy routing must not compete:\n%s", got)
	}
	if strings.Contains(c.ApplyScript(), "Per-uplink policy routing") {
		t.Fatal("ApplyScript must not embed policy routing when tunnels are configured")
	}
}

func TestPolicyRoutingSkipsAddresslessAndIPv6Uplinks(t *testing.T) {
	c := routingFixture()
	c.Uplinks = []Uplink{
		{Interface: "eth1"},
		{Interface: "eth2", Address: "2001:db8::2"},
	}
	if got := c.PolicyRoutingScript(); got != "" {
		t.Fatalf("only IPv4 uplinks with addresses can be pinned by this fragment, got:\n%s", got)
	}
}

func TestApplyScriptEmbedsPolicyRouting(t *testing.T) {
	script := routingFixture().ApplyScript()
	for _, want := range []string{"Per-uplink policy routing", "TABLE=101", "TABLE=102"} {
		if !strings.Contains(script, want) {
			t.Errorf("ApplyScript missing %q:\n%s", want, script)
		}
	}
}

func TestPolicyRoutingFlushTargetsTheSameTables(t *testing.T) {
	flush := routingFixture().PolicyRoutingFlushScript()
	for _, want := range []string{
		"ip route del default dev eth1 metric 10",
		"ip rule del from 192.0.2.10/32 lookup 101",
		"ip route flush table 101",
		"ip route del default dev phy1-sta0 metric 11",
		"ip rule del from 198.51.100.7/32 lookup 102",
		"ip route flush table 102",
	} {
		if !strings.Contains(flush, want) {
			t.Errorf("policy-route flush missing %q:\n%s", want, flush)
		}
	}
}

func TestPolicyRoutingScriptIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}
	path := filepath.Join(t.TempDir(), "policy-routing.sh")
	if err := os.WriteFile(path, []byte(routingFixture().PolicyRoutingScript()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("rendered policy routing is not valid shell: %v\n%s", err, out)
	}
}

func TestApplyScriptIsValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}
	path := filepath.Join(t.TempDir(), "apply.sh")
	if err := os.WriteFile(path, []byte(routingFixture().ApplyScript()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("rendered apply script is not valid shell: %v\n%s", err, out)
	}
}
