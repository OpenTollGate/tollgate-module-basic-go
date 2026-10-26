package mptcp_bonding

import (
	"fmt"
	"testing"
)

// Feeds the REAL conntrack numbers observed on the lab router through
// AssessPaths, so the verdict is checked against measured bytes rather than
// fixtures.
//
// Observed during a live MPTCP transfer (port 7501):
//
//	sport=38100  1098570541 bytes   <- the working subflow
//	sport=38088         801 bytes   <- handshake only
//
// Both to the same peer address, over the same local interface (the client
// reached the router on one path in that run).
func TestAssessPathsOnRealLabData(t *testing.T) {
	// Real bytes from the router's conntrack.
	paths := []SubflowPath{
		{LocalPort: 38100, Interface: "enx6c6e0750c33b", Bytes: 1098570541},
		{LocalPort: 38088, Interface: "enx6c6e0750c33b", Bytes: 801},
	}

	v := AssessPaths(paths)

	if v.Disjoint {
		t.Errorf("this real capture must NOT be judged disjoint: %+v", v)
	}
	if v.AllCarryPayload {
		t.Error("801 bytes is below the payload floor; the idle subflow must be flagged")
	}
	if v.UpstreamDisjointKnown {
		t.Error("upstream disjointness must stay unknown")
	}
	fmt.Printf("real-data verdict: disjoint=%v payload=%v reason=%q\n",
		v.Disjoint, v.AllCarryPayload, v.Reason)

	// And the control: the same two subflows, had they used DIFFERENT
	// interfaces, would pass tiers 1 and 2. This pins that the checker
	// separates "same path" from "different paths" rather than just
	// counting subflows.
	ok := AssessPaths([]SubflowPath{
		{LocalPort: 38100, Interface: "enx6c6e0750c33b", Bytes: 1098570541},
		{LocalPort: 38088, Interface: "wlp58s0", Bytes: 801},
	})
	if ok.Disjoint {
		t.Error("still idle on the second subflow, so still not disjoint")
	}
	if len(ok.Interfaces) != 2 {
		t.Errorf("the two interfaces must both be recorded, got %v", ok.Interfaces)
	}
	fmt.Printf("two-interface but idle verdict: %q\n", ok.Reason)
}
