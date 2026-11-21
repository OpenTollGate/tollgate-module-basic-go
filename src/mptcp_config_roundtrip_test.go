package main

import (
	"encoding/json"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/mptcp_bonding"
)

// main.bondConfigJSON marshals the typed config_manager value and hands the
// bytes to mptcp_bonding.LoadConfig, whose decoder rejects unknown fields.
// That makes the two packages' JSON tags a contract: a field exposed in the
// schema but absent (or spelled differently) in the bond package is silently
// dropped before the bond ever sees it. This test is the fence.
func TestMPTCPBondingConfigRoundTripsIntoBondLoader(t *testing.T) {
	cfg := config_manager.MPTCPBondingConfig{
		Enabled:      true,
		Policy:       "guest_only",
		Server:       "203.0.113.10:65101",
		Method:       "chacha20-ietf-poly1305",
		Password:     "fixture",
		SteeringCIDR: "10.115.249.0/24",
		Transport:    "proxy-terminated",
		Uplinks: []config_manager.MPTCPUplinkConfig{
			{Interface: "phy0-sta0", Address: "192.168.2.15"},
			{Interface: "phy1-sta0", Address: "10.2.1.122"},
		},
		Tunnels: []config_manager.MPTCPTunnelConfig{{
			Name:       "mptcp-wan2",
			Interface:  "phy1-sta0",
			LocalAddr:  "10.2.1.122",
			FwMark:     0x3002,
			Gateway:    "10.2.0.1",
			Table:      302,
			PeerHost:   "203.0.113.10",
			PeerPort:   51912,
			PeerPubKey: "peer-public-key",
			Address:    "10.99.98.2/32",
			PrivateKey: "private-key",
			Keepalive:  25,
		}},
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config_manager value: %v", err)
	}

	loaded, err := mptcp_bonding.LoadConfig(raw)
	if err != nil {
		t.Fatalf("bond loader rejected the config_manager JSON: %v\n%s", err, raw)
	}
	if got, want := loaded.EffectiveTransport(), mptcp_bonding.TransportProxyTerminated; got != want {
		t.Errorf("transport did not survive the round trip: got %q, want %q", got, want)
	}
	if len(loaded.Tunnels) != 1 {
		t.Fatalf("tunnels did not survive the round trip: got %d", len(loaded.Tunnels))
	}
	tun := loaded.Tunnels[0]
	if tun.Name != "mptcp-wan2" || tun.Interface != "phy1-sta0" || tun.Table != 302 {
		t.Errorf("tunnel fields changed in the round trip: %+v", tun)
	}
	if tun.PeerPubKey != "peer-public-key" || tun.PrivateKey != "private-key" {
		t.Errorf("tunnel key material changed in the round trip: %+v", tun)
	}
}
