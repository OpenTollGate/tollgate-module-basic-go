package fips_exit

import (
	"net/netip"
	"reflect"
	"testing"
)

// PRTA 67d4468: down removes metric-100 default; up leaves only
// unreachable metric 42760. Fresh installation cannot recover owned resources.
func TestRecoveryMeasuredDownUp(t *testing.T) {
	c := validConfig()
	e := recoveryFacts(c)
	ops, err := BuildRecoveryPlan(c, e, readyGuards())
	want := []Operation{{TunnelDefault, "ip", []string{"-4", "route", "add", "default", "dev", "exit-test", "table", "200", "metric", "100", "proto", "static"}}}
	if err != nil || !reflect.DeepEqual(ops, want) {
		t.Fatalf("owned tunnel after down/up needs only missing default restored: ops=%v err=%v", ops, err)
	}
}

func recoveryFacts(c Config) RecoveryEvidence {
	env := readyFacts(c)
	env.Interfaces[c.TunnelInterface] = c.MTU
	env.TunnelNameUnused, env.TableUnused, env.RulePriorityUnused = false, false, false
	outer, inner := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	return RecoveryEvidence{Verified: true, OwnershipVerified: true, OwnedConfig: c, Environment: env,
		Tunnel:    RecoveryTunnel{Name: c.TunnelInterface, Mode: "ipip6", Device: c.FIPSInterface, Local: outer, Remote: exit, Inner: netip.PrefixFrom(inner, 32), MTU: c.MTU, Up: true, ExactParameters: true},
		GuestRule: RecoveryRule{Priority: c.RulePriority, Table: c.RoutingTable, InputInterface: c.GuestInterface, Exact: true},
		Routes:    []RecoveryRoute{{Type: "unreachable", Destination: "default", Protocol: "static", Table: c.RoutingTable, Metric: 42760, Exact: true}}}
}
func TestRecoveryHealthyAndDisabled(t *testing.T) {
	c := validConfig()
	e := recoveryFacts(c)
	e.Routes = append(e.Routes, RecoveryRoute{Type: "unicast", Destination: "default", Device: c.TunnelInterface, Protocol: "static", Table: c.RoutingTable, Metric: 100, Exact: true})
	if ops, err := BuildRecoveryPlan(c, e, readyGuards()); err != nil || len(ops) != 0 {
		t.Fatalf("healthy: %v %v", ops, err)
	}
	if ops, err := BuildRecoveryPlan(Config{}, RecoveryEvidence{}, GuardReadiness{}); err != nil || len(ops) != 0 {
		t.Fatalf("disabled: %v %v", ops, err)
	}
	// Fresh installation must still refuse existing resources.
	if _, err := BuildPlan(c, e.Environment, readyGuards()); err == nil {
		t.Fatal("fresh planner accepted existing resources")
	}
	c.MTU = 0
	if ops, err := BuildRecoveryPlan(c, e, readyGuards()); err == nil || len(ops) != 0 {
		t.Fatal("invalid config accepted")
	}
}
func TestRecoveryRefusesUncertainty(t *testing.T) {
	cases := map[string]func(*RecoveryEvidence){
		"unverified":        func(e *RecoveryEvidence) { e.Verified = false },
		"unowned":           func(e *RecoveryEvidence) { e.OwnershipVerified = false },
		"config identity":   func(e *RecoveryEvidence) { e.OwnedConfig.ExitPublicKey = [32]byte{3} },
		"config table":      func(e *RecoveryEvidence) { e.OwnedConfig.RoutingTable = 201 },
		"config mtu":        func(e *RecoveryEvidence) { e.OwnedConfig.MTU = 1200 },
		"env":               func(e *RecoveryEvidence) { e.Environment.Verified = false },
		"local identity":    func(e *RecoveryEvidence) { e.Environment.LocalPublicKey = [32]byte{3} },
		"local address":     func(e *RecoveryEvidence) { e.Environment.LocalFIPSAddress = netip.Addr{} },
		"guest missing":     func(e *RecoveryEvidence) { delete(e.Environment.Interfaces, "guest-test") },
		"outer mtu":         func(e *RecoveryEvidence) { e.Environment.Interfaces["mesh-test"] = 1279 },
		"inner mtu":         func(e *RecoveryEvidence) { e.Environment.Interfaces["exit-test"] = 1200 },
		"conflicts":         func(e *RecoveryEvidence) { e.Environment.AddressesConflictFree = false },
		"policy":            func(e *RecoveryEvidence) { e.Environment.PolicyOrderSafe = false },
		"outer route":       func(e *RecoveryEvidence) { e.Environment.OuterRouteVerified = false },
		"down":              func(e *RecoveryEvidence) { e.Tunnel.Up = false },
		"rebound":           func(e *RecoveryEvidence) { e.Tunnel.Device = "other" },
		"remote":            func(e *RecoveryEvidence) { e.Tunnel.Remote = netip.Addr{} },
		"tunnel mtu":        func(e *RecoveryEvidence) { e.Tunnel.MTU = 1200 },
		"tunnel attributes": func(e *RecoveryEvidence) { e.Tunnel.ExactParameters = false },
		"rule missing":      func(e *RecoveryEvidence) { e.GuestRule = RecoveryRule{} },
		"rule table":        func(e *RecoveryEvidence) { e.GuestRule.Table = 254 },
		"rule priority":     func(e *RecoveryEvidence) { e.GuestRule.Priority++ },
		"rule ingress":      func(e *RecoveryEvidence) { e.GuestRule.InputInterface = "other" },
		"rule uncertain":    func(e *RecoveryEvidence) { e.GuestRule.Exact = false },
		"safety missing":    func(e *RecoveryEvidence) { e.Routes = nil },
		"safety metric":     func(e *RecoveryEvidence) { e.Routes[0].Metric = 100 },
		"safety type":       func(e *RecoveryEvidence) { e.Routes[0].Type = "throw" },
		"route uncertain":   func(e *RecoveryEvidence) { e.Routes[0].Exact = false },
		"main route":        func(e *RecoveryEvidence) { e.Routes[0].Table = 254 },
		"duplicate":         func(e *RecoveryEvidence) { e.Routes = append(e.Routes, e.Routes[0]) },
		"foreign default": func(e *RecoveryEvidence) {
			e.Routes = append(e.Routes, RecoveryRoute{Type: "unicast", Destination: "default", Device: "foreign", Table: 200, Metric: 100, Protocol: "static", Exact: true})
		},
		"specific route": func(e *RecoveryEvidence) {
			e.Routes = append(e.Routes, RecoveryRoute{Type: "unicast", Destination: "10.0.0.0/8", Table: 200, Exact: true})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			e := recoveryFacts(c)
			change(&e)
			if ops, err := BuildRecoveryPlan(c, e, readyGuards()); err == nil || len(ops) != 0 {
				t.Fatalf("accepted %v %v", ops, err)
			}
		})
	}
	for i := 0; i < 6; i++ {
		c := validConfig()
		g := readyGuards()
		fields := []*bool{&g.Forwarding, &g.DNS, &g.NativeIPv6, &g.NAT, &g.MSS, &g.PortalAuthorization}
		*fields[i] = false
		if ops, err := BuildRecoveryPlan(c, recoveryFacts(c), g); err == nil || len(ops) != 0 {
			t.Fatal("missing guard accepted")
		}
	}
}
