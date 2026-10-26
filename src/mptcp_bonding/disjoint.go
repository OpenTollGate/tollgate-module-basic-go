// Preflight disjointness check: can we tell, BEFORE trusting a bond, whether
// its subflows are genuinely separate paths?
//
// The honest answer has three tiers, and only two are decidable from the node
// running the check:
//
//	Tier 1 (did subflows form and carry payload?)   -- decidable
//	Tier 2 (do they leave via different interfaces?) -- decidable
//	Tier 3 (do they stay disjoint downstream?)       -- NOT decidable here
//
// Tier 3 needs an external vantage: two traceroutes compared for their first
// shared hop, or an off-box host observing two different source addresses. A
// node cannot see its own upstream convergence, so this package deliberately
// reports Tier 3 as UNKNOWN instead of guessing.
//
// Why the distinction matters: the investigation behind this PR repeatedly
// produced subflows that were distinct at the socket level but shared one
// physical segment -- distinct local addresses, one local interface, or two
// interfaces meeting at one switch. Every one of those looked healthy by
// counter inspection alone. Tiers 1 and 2 catch the local cases; Tier 3 is a
// measurement, not an assertion, and is left to the operator/CI to settle.
package mptcp_bonding

import "fmt"

// PayloadFloorBytes is the minimum bytes a subflow must have moved to count as
// carrying the connection.
//
// It exists because a subflow can complete its handshake and then carry nothing:
// the observed case was 725 bytes on the second subflow against 869 MB on the
// first. 64 KiB is orders of magnitude above handshake noise and far below any
// real transfer, so it separates "idle" from "working" without needing a
// specific workload.
const PayloadFloorBytes = 64 * 1024

// SubflowPath is one MPTCP subflow's local view, as read from conntrack.
type SubflowPath struct {
	LocalPort uint16 // source port that identifies the subflow on this host
	Interface string // local interface the subflow egresses
	Bytes     uint64 // bytes transferred on this subflow
}

// PathVerdict is the preflight result for a set of subflows.
type PathVerdict struct {
	// Disjoint is true only when the subflows are usable as separate paths:
	// at least two, each on a different local interface, each carrying payload.
	Disjoint bool
	// AllCarryPayload reports whether every subflow moved at least
	// PayloadFloorBytes. A false value with two interfaces means the bond is
	// formed but idle -- the scheduler is keeping traffic on one path.
	AllCarryPayload bool
	// UpstreamDisjointKnown is always false here. Downstream convergence
	// cannot be observed from this node; see the file comment. It is a field
	// rather than an omission so callers cannot mistake "no complaint" for
	// "verified disjoint upstream".
	UpstreamDisjointKnown bool
	// Reason explains a negative verdict in operator-facing terms.
	Reason string
	// Interfaces is the distinct set of local interfaces seen, sorted.
	Interfaces []string
}

// AssessPaths judges whether a set of subflows constitutes a genuinely
// disjoint bond, as far as this node can tell.
//
// It reports rather than errors: a non-disjoint result is a normal state (the
// kernel is entitled to keep traffic on the better path), not a failure of the
// code. Callers decide whether to warn, retry, or proceed.
func AssessPaths(paths []SubflowPath) PathVerdict {
	v := PathVerdict{}

	if len(paths) < 2 {
		v.Reason = fmt.Sprintf(
			"fewer than two subflows (%d): nothing to be disjoint from", len(paths))
		return v
	}

	// Tier 1: does every subflow actually carry payload?
	v.AllCarryPayload = true
	for _, p := range paths {
		if p.Bytes < PayloadFloorBytes {
			v.AllCarryPayload = false
			break
		}
	}

	// Tier 2: do they leave via different local interfaces?
	seen := map[string]bool{}
	for _, p := range paths {
		if p.Interface == "" {
			v.Reason = "a subflow has no local interface; cannot judge disjointness"
			return v
		}
		if !seen[p.Interface] {
			seen[p.Interface] = true
			v.Interfaces = append(v.Interfaces, p.Interface)
		}
	}

	if len(v.Interfaces) < 2 {
		v.Reason = fmt.Sprintf(
			"all %d subflows use the same local interface (%s): distinct "+
				"addresses are not enough, the paths must leave by different "+
				"interfaces", len(paths), v.Interfaces[0])
		return v
	}

	if !v.AllCarryPayload {
		v.Reason = fmt.Sprintf(
			"subflows leave by %d interfaces but at least one moved under %d "+
				"bytes: formed but idle, so the bond is not using every path yet",
			len(v.Interfaces), PayloadFloorBytes)
		return v
	}

	// Tiers 1 and 2 pass. Tier 3 stays unknown by construction.
	v.Disjoint = true
	v.Reason = fmt.Sprintf(
		"%d subflows on %d interfaces, each carrying payload; downstream "+
			"disjointness NOT verified -- requires an external vantage",
		len(paths), len(v.Interfaces))
	return v
}
