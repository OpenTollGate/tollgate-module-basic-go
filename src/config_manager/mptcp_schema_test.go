package config_manager

import "testing"

// The mptcp_bonding section must exist in the schema, must default to DISABLED,
// and must not be marked Required -- because every node deployed before this
// feature lacks the section, and a required field would fail validation on
// upgrade.

func findField(fields []FieldSchema, jsonKey string) *FieldSchema {
	for i := range fields {
		if fields[i].JSONKey == jsonKey {
			return &fields[i]
		}
	}
	return nil
}

func TestMPTCPBondingSectionExists(t *testing.T) {
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("schema must declare an mptcp_bonding section")
	}
	if f.Type != "object" {
		t.Errorf("mptcp_bonding must be an object, got %q", f.Type)
	}
}

func TestMPTCPBondingIsNotRequired(t *testing.T) {
	// Hard requirement: an existing node has no such key. If this is Required,
	// upgrading a node fails validation and the module refuses to start.
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	if f.Required {
		t.Error("mptcp_bonding must NOT be required: pre-existing configs lack it")
	}
}

func TestMPTCPBondingDefaultsToDisabled(t *testing.T) {
	// Opt-in only. A bond that silently enables itself would reconfigure
	// networking on every node that upgrades.
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	en := findField(f.Children, "enabled")
	if en == nil {
		t.Fatal("mptcp_bonding must have an `enabled` child")
	}
	if en.Default != false {
		t.Errorf("enabled must default to false, got %v", en.Default)
	}
}

func TestMPTCPBondingUplinksIsAnArray(t *testing.T) {
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	up := findField(f.Children, "uplinks")
	if up == nil {
		t.Fatal("mptcp_bonding must have an `uplinks` child")
	}
	if up.Type != "array" {
		t.Errorf("uplinks must be an array, got %q", up.Type)
	}
	ifce := findField(up.Children, "interface")
	if ifce == nil {
		t.Fatal("uplink entries must have an `interface` field")
	}
}

func TestMPTCPBondingTransportIsConfigurable(t *testing.T) {
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	transport := findField(f.Children, "transport")
	if transport == nil {
		t.Fatal("mptcp_bonding must expose `transport`; without it the NAT-safe proxy-terminated shape cannot be selected through the CLI")
	}
	got := map[string]bool{}
	for _, v := range transport.Enum {
		got[v] = true
	}
	if !got["advertised-address"] || !got["proxy-terminated"] {
		t.Fatalf("transport enum must allow advertised-address and proxy-terminated, got %v", transport.Enum)
	}
}

func TestMPTCPBondingTunnelsAreConfigurable(t *testing.T) {
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	tunnels := findField(f.Children, "tunnels")
	if tunnels == nil {
		t.Fatal("mptcp_bonding must expose `tunnels`")
	}
	if tunnels.Type != "array" {
		t.Fatalf("tunnels must be an array, got %q", tunnels.Type)
	}
	for _, key := range []string{"name", "interface", "peer_host", "peer_port", "peer_pubkey", "address"} {
		if findField(tunnels.Children, key) == nil {
			t.Errorf("tunnel entries must have a %q field", key)
		}
	}
	pk := findField(tunnels.Children, "private_key")
	if pk == nil {
		t.Fatal("tunnel entries must have a `private_key` field")
	}
	if !pk.Secret {
		t.Error("tunnel private_key must be marked Secret so it is never rendered back")
	}
}

func TestMPTCPBondingPasswordIsSecret(t *testing.T) {
	// The bond carries a shared secret. It must be flagged Secret so the API
	// and CLI never echo it back in rendered config.
	f := findField(GetConfigSchema(), "mptcp_bonding")
	if f == nil {
		t.Fatal("mptcp_bonding section missing")
	}
	pw := findField(f.Children, "password")
	if pw == nil {
		t.Fatal("mptcp_bonding must have a `password` child")
	}
	if !pw.Secret {
		t.Error("password must be marked Secret so it is never rendered back")
	}
}
