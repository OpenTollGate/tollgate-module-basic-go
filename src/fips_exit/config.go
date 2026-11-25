package fips_exit

import (
	"fmt"
	"regexp"
)

// Deliberately narrower than Linux dev_valid_name: safe portable ASCII names,
// IFNAMSIZ-1 bytes, with no option-like leading hyphen or dot-only names.
var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,14}$`)

// GuestForwarding selects downstream IPv4 ingress, never local proxy output.
const GuestForwarding = "guest-forwarding"

// Config describes a consumer without enabling any runtime behavior. Its zero
// value is disabled. Keys must be decoded and identity-validated by the caller.
// Interface names, routing table, priority and inner MTU must be explicit.
type Config struct {
	Enabled                                        bool
	Selection                                      string
	LocalPublicKey, ExitPublicKey                  [32]byte
	GuestInterface, FIPSInterface, TunnelInterface string
	RoutingTable, RulePriority                     uint32
	MTU                                            int
}

// Validate checks only static configuration, not live environment readiness.
// Disabled configurations ignore all other fields. Success does not authorize
// application: the future integration must verify interface roles/existence,
// identity ownership, address and table/rule conflicts, policy ordering, and
// MTU <= observed FIPS MTU - 40 before making any changes (see README.md).
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Selection != GuestForwarding {
		return fmt.Errorf("unsupported selection %q: only guest-forwarding is supported (not all-traffic or proxy output)", c.Selection)
	}
	if c.LocalPublicKey == ([32]byte{}) {
		return fmt.Errorf("local public key is required")
	}
	if c.ExitPublicKey == ([32]byte{}) {
		return fmt.Errorf("exit public key is required")
	}
	if c.LocalPublicKey == c.ExitPublicKey {
		return fmt.Errorf("local and exit identities must be distinct")
	}
	localOuter, localInner := Derive(c.LocalPublicKey)
	exitOuter, exitInner := Derive(c.ExitPublicKey)
	if localOuter == exitOuter || localInner == exitInner {
		return fmt.Errorf("local and exit derived addresses must be distinct")
	}
	for _, iface := range []struct{ role, name string }{
		{"guest", c.GuestInterface}, {"FIPS", c.FIPSInterface}, {"tunnel", c.TunnelInterface},
	} {
		if !interfaceName.MatchString(iface.name) {
			return fmt.Errorf("invalid %s interface %q", iface.role, iface.name)
		}
	}
	if c.GuestInterface == c.FIPSInterface || c.GuestInterface == c.TunnelInterface || c.FIPSInterface == c.TunnelInterface {
		return fmt.Errorf("guest, FIPS and tunnel interfaces must be distinct")
	}
	// Linux RT_TABLE_UNSPEC, DEFAULT, MAIN and LOCAL are not dedicated tables.
	switch c.RoutingTable {
	case 0, 253, 254, 255:
		return fmt.Errorf("routing table must be dedicated, not unspecified/default/main/local")
	}
	// Conservative policy: after the standard local rule (0), before main
	// (32766). This is not proof that the slot is unused or correctly ordered.
	if c.RulePriority == 0 || c.RulePriority >= 32766 {
		return fmt.Errorf("rule priority must be in 1..32765")
	}
	// 576 is our IPv4 minimum policy, not an upstream lower bound. The cap is
	// fips2go v0.11.0 shim/src/exit.rs: 1280 mesh bytes - 40 outer IPv6 bytes.
	if c.MTU < 576 || c.MTU > 1240 {
		return fmt.Errorf("inner MTU must be in 576..1240")
	}
	return nil
}
