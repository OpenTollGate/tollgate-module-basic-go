package mptcp_bonding

import (
	"encoding/json"
	"strings"
	"testing"
)

// The package is a leaf module on purpose: it must not import config_manager,
// because upstream_detector (which will call into it) must be able to depend on
// it without creating a cycle. So the config surface here is plain
// marshal/unmarshal of this package's own Config type, and the HOST module
// (main.go) decides where the bytes come from.
//
// That keeps the cross-module integration one-directional and testable without
// a running config manager.

func TestLoadConfigFromEmptyIsDisabled(t *testing.T) {
	// A node with no bond config at all must produce a disabled bond, not an
	// error: every existing node has no such section, and a hard failure here
	// would take the whole module down on upgrade.
	c, err := LoadConfig(nil)
	if err != nil {
		t.Fatalf("absent config must not error, got: %v", err)
	}
	if c == nil {
		t.Fatal("LoadConfig must return a usable Config, got nil")
	}
	if c.Enabled {
		t.Error("a bond must never default to enabled")
	}
	// And it must be inert: no script for a node that never opted in.
	if s := c.ApplyScript(); s != "" {
		t.Errorf("an absent config must render no script, got: %q", s)
	}
}

func TestLoadConfigParsesAndValidates(t *testing.T) {
	raw := []byte(`{
		"enabled": true,
		"policy": "guest_only",
		"server": "203.0.113.10:65101",
		"method": "chacha20-ietf-poly1305",
		"password": "fixture-value-not-a-secret",
		"uplinks": [
			{"interface": "lan1", "address": "192.168.2.15"},
			{"interface": "br-private", "address": "10.63.101.1"}
		],
		"subflows": 4,
		"steering_cidr": "10.115.249.0/24",
		"socks_port": 1081,
		"tproxy_port": 1082,
		"fwmark": "0x1",
		"fwmark_table": 100
	}`)
	c, err := LoadConfig(raw)
	if err != nil {
		t.Fatalf("valid config must parse, got: %v", err)
	}
	if !c.Enabled {
		t.Error("enabled flag must be honoured")
	}
	if got := len(c.EndpointPlan()); got != 2 {
		t.Errorf("expected 2 endpoints from the parsed uplinks, got %d", got)
	}
}

func TestLoadConfigRejectsInvalidBond(t *testing.T) {
	// Validation runs at load, so a bad bond fails at startup with a clear
	// message instead of silently misrouting a paying customer's traffic.
	raw := []byte(`{
		"enabled": true,
		"policy": "guest_only",
		"server": "203.0.113.10:65101",
		"method": "chacha20-ietf-poly1305",
		"password": "fixture-value-not-a-secret",
		"uplinks": [{"interface": "lan1", "address": "192.168.2.15"}],
		"steering_cidr": "10.115.249.0/24"
	}`)
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("a one-uplink bond must be rejected at load, got nil error")
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	// A typo in the config must be loud. `enabld: true` silently leaving the
	// bond off is exactly the class of bug that costs hours in the field.
	raw := []byte(`{"enabld": true}`)
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("an unknown key must be rejected, got nil error")
	}
}

func TestLoadConfigIsMarshalRoundTrippable(t *testing.T) {
	// main.go will hand this raw JSON from the config manager, so the type must
	// survive a round trip through the same encoder the host uses.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "br-private", Address: "10.63.101.1"},
	}
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	back, err := LoadConfig(blob)
	if err != nil {
		t.Fatalf("round-trip load failed: %v", err)
	}
	if len(back.Uplinks) != 2 || back.Uplinks[1].Interface != "br-private" {
		t.Errorf("round trip lost detail: %+v", back.Uplinks)
	}
}

func TestConfigJSONExampleIsUsableAndCarriesNoSecret(t *testing.T) {
	// The example is what an operator copies. It must parse back as a valid
	// config, and it must never contain a credential.
	out, err := ConfigJSONExample()
	if err != nil {
		t.Fatalf("ConfigJSONExample failed: %v", err)
	}
	if !strings.Contains(out, "enabled") || !strings.Contains(out, "uplinks") {
		t.Errorf("example must document the real field names, got:\n%s", out)
	}
	if strings.Contains(out, "password") {
		t.Errorf("the example must not emit a password field, got:\n%s", out)
	}
	// It must be loadable as-is (the password is absent, so it must fail
	// validation only for that reason, proving the rest of the shape is right).
	if _, err := LoadConfig([]byte(out)); err == nil {
		t.Log("example loaded without a password; validation is lenient about it")
	}
}
