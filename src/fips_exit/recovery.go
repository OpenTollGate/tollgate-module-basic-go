package fips_exit

import (
	"fmt"
	"net/netip"
)

// RecoveryEvidence is supplied by a trusted caller, not by user configuration.
// Verified attests complete, current observations under the caller's namespace
// lock, including no unknown route/rule/tunnel attributes. OwnedConfig records
// the exact previously installed configuration with verified ownership of the
// tunnel, private table and guest rule (matching names alone is NOT ownership).
// Environment reattests identity, addresses, policy ordering and outer routing;
// its unused-resource flags are irrelevant to recovery. Guards must be freshly
// checked for this same config. No existing collector produces this evidence.
// A future executor must revalidate immediately before applying, retain the
// lock, abort on failure, and never substitute route replace for route add.
type RecoveryEvidence struct {
	Verified, OwnershipVerified bool
	OwnedConfig                 Config
	Environment                 Environment
	Tunnel                      RecoveryTunnel
	GuestRule                   RecoveryRule
	// Routes is the COMPLETE IPv4 inventory of OwnedConfig.RoutingTable.
	Routes []RecoveryRoute
}

type RecoveryTunnel struct {
	Name, Mode, Device string
	Local, Remote      netip.Addr
	Inner              netip.Prefix
	MTU                int
	Up                 bool
	// ExactParameters attests encaplimit none, hoplimit 64, no extra addresses,
	// bindings, or unsupported tunnel parameters, and unchanged interface identity.
	ExactParameters bool
}
type RecoveryRule struct {
	Priority, Table uint32
	InputInterface  string
	// Exact attests a single IPv4 rule, from all, without other selectors.
	Exact bool
}
type RecoveryRoute struct {
	Type, Destination, Device, Protocol string
	Table, Metric                       uint32
	// Exact excludes gateways, nexthops, selectors and unknown attributes.
	Exact bool
}

// BuildRecoveryPlan only restores a missing owned IPv4 tunnel default after
// link-up. It never creates/rebinds links, changes rules/guards, or repairs a
// missing safety route. This pure planner is NOT an autonomous reconciler.
func BuildRecoveryPlan(c Config, e RecoveryEvidence, g GuardReadiness) ([]Operation, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, nil
	}
	if !e.Verified || !e.OwnershipVerified || e.OwnedConfig != c {
		return nil, fmt.Errorf("current complete evidence and exact config ownership required")
	}
	env := e.Environment
	outer, inner := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	if !env.Verified || env.LocalPublicKey != c.LocalPublicKey || env.LocalFIPSAddress != outer || env.Interfaces[c.GuestInterface] <= 0 || env.Interfaces[c.FIPSInterface] < c.MTU+40 || env.Interfaces[c.TunnelInterface] != c.MTU || !env.AddressesConflictFree || !env.PolicyOrderSafe || !env.OuterRouteVerified {
		return nil, fmt.Errorf("recovery environment/identity/MTU/policy not verified")
	}
	wantTunnel := RecoveryTunnel{Name: c.TunnelInterface, Mode: "ipip6", Device: c.FIPSInterface, Local: outer, Remote: exit, Inner: netip.PrefixFrom(inner, 32), MTU: c.MTU, Up: true, ExactParameters: true}
	if e.Tunnel != wantTunnel {
		return nil, fmt.Errorf("owned tunnel must match config and be confirmed up")
	}
	if e.GuestRule != (RecoveryRule{Priority: c.RulePriority, Table: c.RoutingTable, InputInterface: c.GuestInterface, Exact: true}) {
		return nil, fmt.Errorf("owned guest rule mismatch")
	}
	if !g.ready() {
		return nil, fmt.Errorf("unchanged forwarding/DNS/IPv6/NAT/MSS/portal guards required")
	}
	safety := RecoveryRoute{Type: "unreachable", Destination: "default", Protocol: "static", Table: c.RoutingTable, Metric: 42760, Exact: true}
	tunnel := RecoveryRoute{Type: "unicast", Destination: "default", Device: c.TunnelInterface, Protocol: "static", Table: c.RoutingTable, Metric: 100, Exact: true}
	haveSafety, haveTunnel := false, false
	for _, r := range e.Routes {
		switch {
		case r == safety && !haveSafety:
			haveSafety = true
		case r == tunnel && !haveTunnel:
			haveTunnel = true
		default:
			return nil, fmt.Errorf("foreign, conflicting, duplicate or uncertain private-table route")
		}
	}
	if !haveSafety {
		return nil, fmt.Errorf("unreachable safety default missing; refuse recovery")
	}
	if haveTunnel {
		return nil, nil
	}
	return []Operation{operations(c)[5]}, nil
}
