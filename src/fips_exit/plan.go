package fips_exit

import (
	"fmt"
	"net/netip"
	"reflect"
	"strconv"
)

type OperationKind string

const (
	UnreachableDefault OperationKind = "unreachable-default"
	CreateTunnel       OperationKind = "create-tunnel"
	SetMTU             OperationKind = "set-mtu"
	AssignAddress      OperationKind = "assign-address"
	LinkUp             OperationKind = "link-up"
	TunnelDefault      OperationKind = "tunnel-default"
	GuestRule          OperationKind = "guest-rule"
)

// Operation is an argv vector, never a shell command. Resolve ip from a trusted
// executable path in the future applier; do not concatenate Args into a shell.
type Operation struct {
	Kind    OperationKind
	Program string
	Args    []string
}

// Environment contains caller-observed facts, not user configuration. Verified
// attests to complete, current namespace/interface/identity inspection. The
// other booleans are explicit positive attestations; zero values fail closed.
// See README for checks that cannot be inferred from this pure data structure.
type Environment struct {
	Interfaces                                                  map[string]int
	LocalPublicKey                                              [32]byte
	LocalFIPSAddress                                            netip.Addr
	Verified, TunnelNameUnused, TableUnused, RulePriorityUnused bool
	AddressesConflictFree, PolicyOrderSafe, OuterRouteVerified  bool
}

// GuardReadiness attests that separately implemented protections are effective
// for this config. It does not implement or install any of these protections.
type GuardReadiness struct{ Forwarding, DNS, NativeIPv6, NAT, MSS, PortalAuthorization bool }

// BuildPlan constructs a fresh-install plan only. No commands are executed.
// Environment attestations and guard readiness must describe this exact config
// in the same network namespace, obtained under the future applier's lock.
// Existing resources (even previously owned ones) are refused: reconciliation
// and teardown are deliberately outside this planner's contract.
func BuildPlan(c Config, e Environment, g GuardReadiness) ([]Operation, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, nil
	}
	if !e.Verified {
		return nil, fmt.Errorf("current environment verification required")
	}
	if e.Interfaces[c.GuestInterface] <= 0 || e.Interfaces[c.FIPSInterface] <= 0 {
		return nil, fmt.Errorf("guest and FIPS interfaces must exist with observed MTUs")
	}
	if c.MTU > e.Interfaces[c.FIPSInterface]-40 {
		return nil, fmt.Errorf("inner MTU exceeds observed FIPS MTU minus 40")
	}
	outer, _ := Derive(c.LocalPublicKey)
	if e.LocalPublicKey != c.LocalPublicKey || e.LocalFIPSAddress != outer {
		return nil, fmt.Errorf("FIPS local identity/address mismatch")
	}
	_, exists := e.Interfaces[c.TunnelInterface]
	if exists || !e.TunnelNameUnused || !e.TableUnused || !e.RulePriorityUnused {
		return nil, fmt.Errorf("tunnel name, table and rule priority must be verified unused")
	}
	if !e.AddressesConflictFree || !e.PolicyOrderSafe || !e.OuterRouteVerified {
		return nil, fmt.Errorf("address conflicts, policy ordering and outer IPv6 route require verification")
	}
	if !g.ready() {
		return nil, fmt.Errorf("forwarding/DNS/IPv6/NAT/MSS/portal guards must be ready before steering")
	}
	return operations(c), nil
}

func (g GuardReadiness) ready() bool {
	return g.Forwarding && g.DNS && g.NativeIPv6 && g.NAT && g.MSS && g.PortalAuthorization
}

// ValidateOperations rejects reordered, missing, edited or extra operations.
// This is a pure structural gate, NOT live authorization to execute: the caller
// must revalidate environment/guards immediately before application and abort
// on any command failure. Never install the final rule after a failed step.
func ValidateOperations(c Config, g GuardReadiness, ops []Operation) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Enabled {
		if len(ops) != 0 {
			return fmt.Errorf("disabled config must have zero operations")
		}
		return nil
	}
	if !g.ready() {
		return fmt.Errorf("prerequisite guards are not ready")
	}
	if !reflect.DeepEqual(ops, operations(c)) {
		return fmt.Errorf("operations differ from safe ordered plan (unreachable first, guest rule last)")
	}
	return nil
}

func operations(c Config) []Operation {
	outer, inner := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	table := strconv.FormatUint(uint64(c.RoutingTable), 10)
	priority := strconv.FormatUint(uint64(c.RulePriority), 10)
	return []Operation{
		{UnreachableDefault, "ip", []string{"-4", "route", "add", "unreachable", "default", "table", table, "metric", "42760", "proto", "static"}},
		{CreateTunnel, "ip", []string{"-6", "tunnel", "add", c.TunnelInterface, "mode", "ipip6", "local", outer.String(), "remote", exit.String(), "dev", c.FIPSInterface, "encaplimit", "none", "hoplimit", "64"}},
		{SetMTU, "ip", []string{"link", "set", "dev", c.TunnelInterface, "mtu", strconv.Itoa(c.MTU)}},
		{AssignAddress, "ip", []string{"-4", "address", "add", inner.String() + "/32", "dev", c.TunnelInterface}},
		{LinkUp, "ip", []string{"link", "set", "dev", c.TunnelInterface, "up"}},
		{TunnelDefault, "ip", []string{"-4", "route", "add", "default", "dev", c.TunnelInterface, "table", table, "metric", "100", "proto", "static"}},
		{GuestRule, "ip", []string{"-4", "rule", "add", "priority", priority, "iif", c.GuestInterface, "lookup", table}},
	}
}
