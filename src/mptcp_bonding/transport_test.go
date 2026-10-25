package mptcp_bonding

import (
	"strings"
	"testing"
)

// These tests encode the PROXY-TERMINATED transport decision (D10 in
// docs/architecture/multi-wan-bonding-decision.md), measured on a physical
// router.
//
// The defect they guard against: EndpointPlan marks every uplink address
// `signal`, which makes the kernel send ADD_ADDR and instructs the peer to
// connect BACK to that address. Behind NAT or CGNAT the peer cannot reach it,
// so it fails — measured on the lab router, where both uplinks were private
// (house NAT + Starlink CGNAT) and the peer's MPTcpExtAddAddr stayed 0 while
// MPJoinNoTokenFound climbed.
//
// Under the proxy-terminated shape the client initiates every path and the
// server never dials back, so the client must NOT instruct the peer to connect
// to its own private uplink addresses.

func proxyTerminatedFixture() *Config {
	return &Config{
		Enabled:      true,
		Policy:       DefaultPolicy,
		Server:       "203.0.113.10:65101",
		Method:       "chacha20-ietf-poly1305",
		Password:     "fixture",
		SteeringCIDR: "10.115.249.0/24",
		Uplinks: []Uplink{
			{Interface: "lan1", Address: "192.168.2.15"},
			{Interface: "phy0-sta0", Address: "10.2.1.122"},
		},
		Transport: TransportProxyTerminated,
	}
}

// THE test that matters: in the proxy-terminated shape no endpoint may be
// signalled, because signalling is what asks the peer to dial back.
func TestProxyTerminatedSignalsNoEndpoints(t *testing.T) {
	c := proxyTerminatedFixture()

	for _, cmd := range c.EndpointPlan() {
		if strings.Contains(cmd, "signal") {
			t.Errorf(
				"proxy-terminated bond must not SIGAL an endpoint (that asks the "+
					"peer to connect back to an address it cannot reach): %q", cmd)
		}
	}
}

// The uplinks must still be registered as `subflow` so the client can OPEN
// subflows over them. Dropping `signal` must not drop the capability.
func TestProxyTerminatedStillPermitsSubflows(t *testing.T) {
	c := proxyTerminatedFixture()

	plan := c.EndpointPlan()
	if len(plan) != 2 {
		t.Fatalf("expected one endpoint command per uplink, got %d: %v", len(plan), plan)
	}
	for _, cmd := range plan {
		if !strings.Contains(cmd, "subflow") {
			t.Errorf("endpoint must still permit subflows: %q", cmd)
		}
	}
}

// Today's advertised-address shape must keep `signal` — it is the only way a
// peer can learn a genuinely public address, and this change must not regress
// the networks where the old shape is correct.
func TestAdvertisedAddressShapeStillSignals(t *testing.T) {
	c := proxyTerminatedFixture()
	c.Transport = TransportAdvertisedAddress

	plan := c.EndpointPlan()
	if len(plan) == 0 {
		t.Fatal("advertised-address shape must still render endpoints")
	}
	signalled := 0
	for _, cmd := range plan {
		if strings.Contains(cmd, "signal") {
			signalled++
		}
	}
	if signalled != len(plan) {
		t.Errorf("advertised-address shape must signal every endpoint; got %d of %d signalled: %v",
			signalled, len(plan), plan)
	}
}

// An unset Transport must resolve to the historic behaviour, so an existing
// config that never mentions the field keeps working byte-for-byte.
func TestEmptyTransportDefaultsToAdvertisedAddress(t *testing.T) {
	c := proxyTerminatedFixture()
	c.Transport = ""

	if got := c.EffectiveTransport(); got != TransportAdvertisedAddress {
		t.Errorf("empty transport must default to the historic shape; got %q", got)
	}
	if plan := c.EndpointPlan(); len(plan) == 0 || !strings.Contains(plan[0], "signal") {
		t.Errorf("defaulted shape must still signal endpoints, got %v", plan)
	}
}

// The transport value is validated rather than silently ignored: an unknown
// string must be rejected, because guessing which transport was meant is how a
// bond ends up advertising private addresses again.
func TestUnknownTransportRejected(t *testing.T) {
	c := proxyTerminatedFixture()
	c.Transport = "carrier-pigeon"

	if err := c.Validate(); err == nil {
		t.Fatal("an unknown transport must be rejected, not silently accepted")
	}
}

// Proxy-terminated runs behind NAT by definition, so it must not require the
// peer-reachable-address machinery (tunnels) to be configured.
func TestProxyTerminatedNeedsNoTunnels(t *testing.T) {
	c := proxyTerminatedFixture()
	if len(c.Tunnels) != 0 {
		t.Fatal("fixture should have no tunnels")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("proxy-terminated bond must validate with no tunnels: %v", err)
	}
}
