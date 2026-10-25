package mptcp_bonding

import (
	"net"
	"strings"
	"testing"
)

// fixtureCredential stands in for a runtime-supplied secret. It is not a
// credential: validation only needs a non-empty value, and keeping it a
// bare identifier avoids tripping secret scanners on a test fixture.
const fixtureCredential = "fixture-value-not-a-secret"

// validConfig returns a config that must pass validation, so each test can
// mutate exactly one field.
func validConfig() *Config {
	return &Config{
		Enabled:      true,
		Policy:       PolicyGuestOnly,
		Server:       "203.0.113.10:65101",
		Method:       "chacha20-ietf-poly1305",
		Password:     fixtureCredential, // pragma: allowlist secret
		Uplinks:      []Uplink{{Interface: "eth1"}, {Interface: "phy1-sta0"}},
		SteeringCIDR: "10.115.249.0/24",
		SocksPort:    1081,
		TproxyPort:   1082,
		FwMark:       "0x1",
		FwMarkTable:  100,
	}
}

func TestDisabledConfigNeedsNothing(t *testing.T) {
	// Default-off is a feature: a device that has never been configured must
	// not fail validation, because nothing about it should change.
	c := &Config{}
	if err := c.Validate(); err != nil {
		t.Fatalf("a disabled bond must validate cleanly, got: %v", err)
	}
}

func TestValidConfigPasses(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestSubflowsDefaultsWhenUnset(t *testing.T) {
	c := validConfig()
	c.Subflows = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Subflows != DefaultSubflows {
		t.Errorf("subflows = %d, want default %d", c.Subflows, DefaultSubflows)
	}
}

func TestValidationRejectsUnusableBonds(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"no server", func(c *Config) { c.Server = "" }, "server is required"},
		{"server without port", func(c *Config) { c.Server = "203.0.113.10" }, "host:port"},
		{"server port not numeric", func(c *Config) { c.Server = "203.0.113.10:http" }, "not a valid port"},
		{"server port out of range", func(c *Config) { c.Server = "203.0.113.10:70000" }, "not a valid port"},
		{"no method", func(c *Config) { c.Method = "" }, "method is required"},
		{"no password", func(c *Config) { c.Password = "" }, "password is required"},
		{"one uplink", func(c *Config) { c.Uplinks = c.Uplinks[:1] }, "at least 2 uplinks"},
		{"no uplinks", func(c *Config) { c.Uplinks = nil }, "at least 2 uplinks"},
		{"empty interface", func(c *Config) { c.Uplinks[0].Interface = "  " }, "interface is empty"},
		{"duplicate uplink", func(c *Config) {
			c.Uplinks = []Uplink{{Interface: "eth1"}, {Interface: "eth1"}}
		}, "listed twice"},
		{"unknown policy", func(c *Config) { c.Policy = "sometimes" }, "unknown policy"},
		{"missing policy", func(c *Config) { c.Policy = "" }, "policy is required"},
		{"guest_only without cidr", func(c *Config) { c.SteeringCIDR = "" }, "steering_cidr is required"},
		{"bad cidr", func(c *Config) { c.SteeringCIDR = "10.0.0.1" }, "not a CIDR"},
		{"negative subflows", func(c *Config) { c.Subflows = -1 }, "subflows must be"},
		{"ports collide", func(c *Config) { c.SocksPort, c.TproxyPort = 1080, 1080 }, "must differ"},
		{"bad fwmark", func(c *Config) { c.FwMark = "zzz" }, "not hex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestPolicyAllNeedsNoSteeringCIDR(t *testing.T) {
	// policy "all" makes the bond the default route; there is no guest
	// subset to name.
	c := validConfig()
	c.Policy = PolicyAll
	c.SteeringCIDR = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("policy all must not require a steering CIDR: %v", err)
	}
}

func TestUplinkInterfacesPreservesOrder(t *testing.T) {
	c := validConfig()
	got := c.UplinkInterfaces()
	want := []string{"eth1", "phy1-sta0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestShouldSteerGuestOnly(t *testing.T) {
	c := validConfig()
	guest := net.ParseIP("10.115.249.201")
	outsider := net.ParseIP("192.168.9.9")
	internet := net.ParseIP("1.1.1.1")
	server := net.ParseIP("203.0.113.10")

	tests := []struct {
		name     string
		src, dst net.IP
		want     bool
	}{
		{"guest to internet", guest, internet, true},
		{"non-guest is left alone", outsider, internet, false},
		{"guest to the tunnel's own far end is not looped", guest, server, false},
		{"nil source is ignored", nil, internet, false},
		{"nil destination is ignored", guest, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.ShouldSteer(tc.src, tc.dst); got != tc.want {
				t.Errorf("ShouldSteer(%v, %v) = %v, want %v", tc.src, tc.dst, got, tc.want)
			}
		})
	}
}

func TestShouldSteerAllPolicy(t *testing.T) {
	c := validConfig()
	c.Policy = PolicyAll
	c.SteeringCIDR = ""

	// Under policy all, any source is bondable — but never the far end,
	// which would steer the tunnel into itself.
	if !c.ShouldSteer(net.ParseIP("192.168.9.9"), net.ParseIP("1.1.1.1")) {
		t.Error("policy all must steer arbitrary sources")
	}
	if c.ShouldSteer(net.ParseIP("192.168.9.9"), net.ParseIP("203.0.113.10")) {
		t.Error("policy all must not steer traffic to the tunnel far end (loop risk)")
	}
}

func TestShouldSteerDisabledIsAlwaysFalse(t *testing.T) {
	c := validConfig()
	c.Enabled = false
	if c.ShouldSteer(net.ParseIP("10.115.249.201"), net.ParseIP("1.1.1.1")) {
		t.Error("a disabled bond must never steer anything")
	}
}

func TestLocalDontSteerAddrsIncludesServerHost(t *testing.T) {
	c := validConfig()
	addrs := c.LocalDontSteerAddrs()
	joined := strings.Join(addrs, " ")
	if !strings.Contains(joined, "203.0.113.10") {
		t.Errorf("far end must never be diverted; got %v", addrs)
	}
	if !strings.Contains(joined, "10.115.249.0/24") {
		t.Errorf("guest subnet should be listed for the guard rule; got %v", addrs)
	}
}
