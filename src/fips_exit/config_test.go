package fips_exit

import (
	"encoding/binary"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{Enabled: true, Selection: GuestForwarding,
		LocalPublicKey: [32]byte{1}, ExitPublicKey: [32]byte{2},
		GuestInterface: "guest-test", FIPSInterface: "mesh-test", TunnelInterface: "exit-test",
		RoutingTable: 200, RulePriority: 10000, MTU: 1240}
}

func TestConfigDisabled(t *testing.T) {
	for _, c := range []Config{{}, {Selection: "proxy", MTU: -1}} {
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigValid(t *testing.T) {
	for _, mtu := range []int{576, 1200, 1240} {
		c := validConfig()
		c.MTU = mtu
		before := c
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		if c != before {
			t.Fatal("validation mutated config")
		}
	}
	for _, table := range []uint32{1, 252, 256, 4294967295} {
		c := validConfig()
		c.RoutingTable = table
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, priority := range []uint32{1, 32765} {
		c := validConfig()
		c.RulePriority = priority
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	c := validConfig()
	c.GuestInterface = "abcDEF012_.-xy9"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigRejectsInvalid(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*Config)
	}{
		{"missing selection", "selection", func(c *Config) { c.Selection = "" }},
		{"all", "selection", func(c *Config) { c.Selection = "all" }},
		{"all-traffic", "selection", func(c *Config) { c.Selection = "all-traffic" }},
		{"proxy", "selection", func(c *Config) { c.Selection = "proxy" }},
		{"unknown", "selection", func(c *Config) { c.Selection = "typo" }},
		{"missing local", "local public key", func(c *Config) { c.LocalPublicKey = [32]byte{} }},
		{"missing exit", "exit public key", func(c *Config) { c.ExitPublicKey = [32]byte{} }},
		{"same identity", "distinct", func(c *Config) { c.ExitPublicKey = c.LocalPublicKey }},
		{"inner collision", "derived addresses", func(c *Config) {
			c.LocalPublicKey = [32]byte{}
			c.ExitPublicKey = [32]byte{}
			binary.BigEndian.PutUint32(c.LocalPublicKey[28:], 3656)
			binary.BigEndian.PutUint32(c.ExitPublicKey[28:], 3989)
			// Fixed SHA256 collision in the 22-bit inner address, not identity validation.
			lo, li := Derive(c.LocalPublicKey)
			eo, ei := Derive(c.ExitPublicKey)
			if lo == eo || li != ei {
				t.Fatal("invalid collision fixture")
			}
		}},
		{"guest=fips", "distinct", func(c *Config) { c.GuestInterface = c.FIPSInterface }},
		{"guest=tunnel", "distinct", func(c *Config) { c.GuestInterface = c.TunnelInterface }},
		{"fips=tunnel", "distinct", func(c *Config) { c.FIPSInterface = c.TunnelInterface }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.change(&c)
			requireConfigError(t, c, tc.want)
		})
	}
	for _, name := range []string{"", ".", "..", "abcdefghijklmnop", "a/b", "a:b", "a b", "a\tb", "a\nb", "a\x00b", "é", "a;id", "-option"} {
		for _, field := range []string{"guest", "FIPS", "tunnel"} {
			t.Run(field+"/"+name, func(t *testing.T) {
				c := validConfig()
				switch field {
				case "guest":
					c.GuestInterface = name
				case "FIPS":
					c.FIPSInterface = name
				case "tunnel":
					c.TunnelInterface = name
				}
				requireConfigError(t, c, field+" interface")
			})
		}
	}
	for _, table := range []uint32{0, 253, 254, 255} {
		c := validConfig()
		c.RoutingTable = table
		requireConfigError(t, c, "routing table")
	}
	for _, priority := range []uint32{0, 32766, 32767, 32768, 4294967295} {
		c := validConfig()
		c.RulePriority = priority
		requireConfigError(t, c, "priority")
	}
	for _, mtu := range []int{-1, 0, 575, 1241, 1500} {
		c := validConfig()
		c.MTU = mtu
		requireConfigError(t, c, "MTU")
	}
}

func requireConfigError(t *testing.T, c Config, want string) {
	t.Helper()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Validate() = %v, want %q", err, want)
	}
}
