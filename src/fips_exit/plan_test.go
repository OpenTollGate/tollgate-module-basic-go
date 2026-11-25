package fips_exit

import (
	"net/netip"
	"reflect"
	"testing"
)

func readyFacts(c Config) Environment {
	outer, _ := Derive(c.LocalPublicKey)
	return Environment{Interfaces: map[string]int{c.GuestInterface: 1500, c.FIPSInterface: 1280}, LocalPublicKey: c.LocalPublicKey, LocalFIPSAddress: outer,
		Verified: true, TunnelNameUnused: true, TableUnused: true, RulePriorityUnused: true,
		AddressesConflictFree: true, PolicyOrderSafe: true, OuterRouteVerified: true}
}
func readyGuards() GuardReadiness {
	return GuardReadiness{Forwarding: true, DNS: true, NativeIPv6: true, NAT: true, MSS: true, PortalAuthorization: true}
}
func TestPlanDisabled(t *testing.T) {
	ops, err := BuildPlan(Config{}, Environment{}, GuardReadiness{})
	if err != nil || len(ops) != 0 {
		t.Fatalf("%v %v", ops, err)
	}
	if err := ValidateOperations(Config{}, GuardReadiness{}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestPlanExactOperations(t *testing.T) {
	c := validConfig()
	outer, inner := Derive(c.LocalPublicKey)
	exit, _ := Derive(c.ExitPublicKey)
	ops, err := BuildPlan(c, readyFacts(c), readyGuards())
	if err != nil {
		t.Fatal(err)
	}
	want := []Operation{
		{UnreachableDefault, "ip", []string{"-4", "route", "add", "unreachable", "default", "table", "200", "metric", "42760", "proto", "static"}},
		{CreateTunnel, "ip", []string{"-6", "tunnel", "add", "exit-test", "mode", "ipip6", "local", outer.String(), "remote", exit.String(), "dev", "mesh-test", "encaplimit", "none", "hoplimit", "64"}},
		{SetMTU, "ip", []string{"link", "set", "dev", "exit-test", "mtu", "1240"}},
		{AssignAddress, "ip", []string{"-4", "address", "add", inner.String() + "/32", "dev", "exit-test"}},
		{LinkUp, "ip", []string{"link", "set", "dev", "exit-test", "up"}},
		{TunnelDefault, "ip", []string{"-4", "route", "add", "default", "dev", "exit-test", "table", "200", "metric", "100", "proto", "static"}},
		{GuestRule, "ip", []string{"-4", "rule", "add", "priority", "10000", "iif", "guest-test", "lookup", "200"}},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("got %#v want %#v", ops, want)
	}
	if err := ValidateOperations(c, readyGuards(), ops); err != nil {
		t.Fatal(err)
	}
}
func TestPlanRejectsEnvironment(t *testing.T) {
	cases := map[string]func(*Environment){
		"unknown":         func(e *Environment) { e.Verified = false },
		"guest missing":   func(e *Environment) { delete(e.Interfaces, "guest-test") },
		"fips missing":    func(e *Environment) { delete(e.Interfaces, "mesh-test") },
		"mtu":             func(e *Environment) { e.Interfaces["mesh-test"] = 1279 },
		"identity":        func(e *Environment) { e.LocalPublicKey = [32]byte{3} },
		"address":         func(e *Environment) { e.LocalFIPSAddress = netip.Addr{} },
		"tunnel occupied": func(e *Environment) { e.TunnelNameUnused = false },
		"tunnel exists":   func(e *Environment) { e.Interfaces["exit-test"] = 1240 },
		"table":           func(e *Environment) { e.TableUnused = false },
		"priority":        func(e *Environment) { e.RulePriorityUnused = false },
		"conflict":        func(e *Environment) { e.AddressesConflictFree = false },
		"ordering":        func(e *Environment) { e.PolicyOrderSafe = false },
		"outer route":     func(e *Environment) { e.OuterRouteVerified = false },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			e := readyFacts(c)
			change(&e)
			ops, err := BuildPlan(c, e, readyGuards())
			if err == nil || len(ops) != 0 {
				t.Fatalf("accepted: %v %v", ops, err)
			}
		})
	}
	c := validConfig()
	c.MTU = 0
	if ops, err := BuildPlan(c, readyFacts(c), readyGuards()); err == nil || len(ops) != 0 {
		t.Fatal("invalid config accepted")
	}
}
func TestPlanRefusesAbsentGuards(t *testing.T) {
	for i := 0; i < 6; i++ {
		g := readyGuards()
		fields := []*bool{&g.Forwarding, &g.DNS, &g.NativeIPv6, &g.NAT, &g.MSS, &g.PortalAuthorization}
		*fields[i] = false
		c := validConfig()
		ops, _ := BuildPlan(c, readyFacts(c), readyGuards())
		if got, err := BuildPlan(c, readyFacts(c), g); err == nil || len(got) != 0 {
			t.Fatal("unguarded steering")
		}
		if err := ValidateOperations(c, g, ops); err == nil {
			t.Fatal("unguarded operations accepted")
		}
	}
}
func TestPlanRejectsUnsafeOperations(t *testing.T) {
	c := validConfig()
	g := readyGuards()
	for _, change := range []func([]Operation) []Operation{
		func(o []Operation) []Operation { o[0], o[6] = o[6], o[0]; return o }, // rule before unreachable
		func(o []Operation) []Operation { return o[1:] },                      // no unreachable default
		func(o []Operation) []Operation { o[0].Args[6] = "254"; return o },    // main-table mutation
		func(o []Operation) []Operation { o[1].Program = "sh"; return o },
	} {
		ops, _ := BuildPlan(c, readyFacts(c), g)
		if len(ops) != 7 {
			t.Fatal("missing plan")
		}
		if err := ValidateOperations(c, g, change(ops)); err == nil {
			t.Fatal("unsafe operations accepted")
		}
	}
	ops, _ := BuildPlan(c, readyFacts(c), g)
	if err := ValidateOperations(Config{}, g, ops); err == nil {
		t.Fatal("disabled operations accepted")
	}
	c.MTU = 0
	if err := ValidateOperations(c, g, ops); err == nil {
		t.Fatal("invalid config accepted")
	}
}
