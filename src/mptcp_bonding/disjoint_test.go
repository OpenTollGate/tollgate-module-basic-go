package mptcp_bonding

import (
	"strings"
	"testing"
)

// The operator asked: can we ensure subflows are genuinely disjoint BEFORE
// using them? The honest answer splits in two, and this file encodes the half
// that is decidable from the booting node.
//
// Tier 1 (counters): did subflows actually form and carry payload?
// Tier 2 (local):    do they leave via different local interfaces?
// Tier 3 (upstream): do they stay disjoint downstream? -- NOT decidable from
//                    one node. That needs an external vantage, so the checker
//                    reports it as unknown rather than pretending.

func TestPathsVerdictRejectsSameInterface(t *testing.T) {
	// The failure this whole investigation kept hitting: two subflows that are
	// distinct at the socket level but leave through ONE interface. Distinct
	// local addresses are necessary but not sufficient.
	v := AssessPaths([]SubflowPath{
		{LocalPort: 51104, Interface: "enx6c6e0750c33b", Bytes: 869303545},
		{LocalPort: 51090, Interface: "enx6c6e0750c33b", Bytes: 725},
	})
	if v.Disjoint {
		t.Errorf("two subflows on the same interface must NOT be called disjoint: %+v", v)
	}
	if !strings.Contains(v.Reason, "same local interface") {
		t.Errorf("the reason must name the real cause, got %q", v.Reason)
	}
}

func TestPathsVerdictAcceptsDifferentInterfacesWithPayload(t *testing.T) {
	v := AssessPaths([]SubflowPath{
		{LocalPort: 35282, Interface: "wlp58s0", Bytes: 510695705},
		{LocalPort: 46249, Interface: "enx6c6e0750c33b", Bytes: 1256176480},
	})
	if !v.Disjoint {
		t.Errorf("different interfaces each carrying payload IS disjoint: %+v", v)
	}
	if v.UpstreamDisjointKnown {
		t.Error("downstream disjointness cannot be known from one node; must report unknown")
	}
}

func TestPathsVerdictFlagsHandshakeOnlySubflow(t *testing.T) {
	// The decisive symptom from the real run: the second subflow existed and
	// completed its handshake but carried ~725 bytes. A checker that only
	// compared interfaces would miss this.
	v := AssessPaths([]SubflowPath{
		{LocalPort: 51104, Interface: "enx6c6e0750c33b", Bytes: 869303545},
		{LocalPort: 51090, Interface: "wlp58s0", Bytes: 725},
	})
	if v.AllCarryPayload {
		t.Errorf("a handshake-only subflow must be reported as idle: %+v", v)
	}
	if v.Disjoint {
		t.Error("interfaces differ but one subflow is idle, so this is not a usable bond yet")
	}
}

func TestPathsVerdictSingleSubflowIsInsufficient(t *testing.T) {
	v := AssessPaths([]SubflowPath{
		{LocalPort: 51104, Interface: "wlp58s0", Bytes: 869303545},
	})
	if v.Disjoint {
		t.Error("a single subflow cannot be disjoint: nothing to be disjoint from")
	}
	if !strings.Contains(v.Reason, "fewer than two") {
		t.Errorf("reason must say there are too few subflows, got %q", v.Reason)
	}
}

func TestPathsVerdictEmptyIsInsufficient(t *testing.T) {
	v := AssessPaths(nil)
	if v.Disjoint {
		t.Error("no subflows cannot be disjoint")
	}
}

func TestPayloadFloorIsDocumentedConstant(t *testing.T) {
	// The floor must be explicit: a subflow that moved a handful of packets is
	// not carrying the connection. 64 KiB is comfortably above handshake noise
	// (the observed handshake-only flow moved 725 bytes) and far below any real
	// transfer.
	if PayloadFloorBytes != 64*1024 {
		t.Errorf("PayloadFloorBytes changed without review: %d", PayloadFloorBytes)
	}
}
