// Package mptcp_bonding wires an MPTCP multi-WAN bond into TollGate as an
// *uplink* concern.
//
// Design intent (see docs/MPTCP-BONDING.md):
//
//	TollGate's portal, payment gate and session accounting are untouched.
//	Bonding replaces only how packets leave the box: instead of one uplink,
//	a kernel MPTCP meta-connection spreads them over several uplinks and a
//	remote de-aggregation server reassembles them.
//
// This file is the pure, testable core: config validation and rendering of
// the platform artefacts. It performs no I/O, so it can be unit-tested
// anywhere — including on a workstation with no router attached.
package mptcp_bonding

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Policy decides which traffic may use the bond.
type Policy string

const (
	// PolicyGuestOnly steers only forwarded guest traffic. The router's own
	// management traffic (SSH, portal admin, payments) stays on the primary
	// uplink, so a broken bond can never lock the operator out of the box.
	// This is the default and the recommended first deployment.
	PolicyGuestOnly Policy = "guest_only"

	// PolicyAll makes the bonded path the default route for everything.
	PolicyAll Policy = "all"
)

// Uplink names one member of the bond.
type Uplink struct {
	// Interface is the network interface that carries this subflow,
	// e.g. "phy1-sta0" (a wireless STA) or "eth1" (a physical port).
	Interface string `json:"interface"`
	// Address, when set, is the local address advertised for this subflow.
	// Empty means "discover at runtime".
	Address string `json:"address,omitempty"`
}

// Config is the bond configuration.
//
// Enabled defaults to false everywhere. A paid device must never silently
// change its connectivity model: an operator opts in explicitly.
type Config struct {
	Enabled bool   `json:"enabled"`
	Policy  Policy `json:"policy"`

	// Server is the remote MPTCP de-aggregation endpoint, host:port.
	// The far side must speak MPTCP; a plain endpoint would silently
	// produce a single-path tunnel (see docs/MPTCP-BONDING.md).
	Server string `json:"server"`

	// Method is the shadowsocks cipher used by the transport.
	Method string `json:"method"`
	// Password is supplied at runtime and must never be persisted in the
	// repo. TollGate stores it via its existing config manager.
	Password string `json:"password,omitempty"`

	// Uplinks are the bond members. At least two make a bond meaningful.
	Uplinks []Uplink `json:"uplinks"`

	// Subflows is the kernel MPTCP subflow ceiling.
	Subflows int `json:"subflows"`

	// SteeringCIDR, for PolicyGuestOnly, is the guest network whose traffic
	// is steered into the bond.
	SteeringCIDR string `json:"steering_cidr,omitempty"`

	// ProxyPorts are the local ports the transport listens on.
	SocksPort  int `json:"socks_port"`
	TproxyPort int `json:"tproxy_port"`

	// FwMark / FwMarkTable are the policy-routing handles. They must not
	// collide with TollGate's own; the defaults are chosen to sit clear of
	// the values the module already uses.
	FwMark      string `json:"fwmark"`
	FwMarkTable int    `json:"fwmark_table"`
}

// DefaultSubflows is the kernel default and a sane ceiling for a mobile box.
const DefaultSubflows = 8

// DefaultPolicy is deliberately the conservative one.
const DefaultPolicy = PolicyGuestOnly

// Validate reports the first problem that would make the bond unusable or
// dangerous. It is strict on purpose: a half-configured bond is worse than
// no bond, because it silently changes how a paying customer's traffic
// leaves the device.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Server == "" {
		return fmt.Errorf("mptcp_bonding: server is required when enabled")
	}
	host, portStr, err := net.SplitHostPort(c.Server)
	if err != nil {
		return fmt.Errorf("mptcp_bonding: server must be host:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("mptcp_bonding: server host is empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("mptcp_bonding: server port %q is not a valid port", portStr)
	}

	if c.Method == "" {
		return fmt.Errorf("mptcp_bonding: method is required")
	}
	if c.Password == "" {
		return fmt.Errorf("mptcp_bonding: password is required")
	}

	// Two uplinks is the point of bonding. One uplink is a configuration
	// error, not a degenerate bond: it would rewrite the routing table for
	// no benefit while adding all of the failure modes.
	if len(c.Uplinks) < 2 {
		return fmt.Errorf("mptcp_bonding: at least 2 uplinks are required, got %d", len(c.Uplinks))
	}
	seen := make(map[string]bool, len(c.Uplinks))
	for i, u := range c.Uplinks {
		if strings.TrimSpace(u.Interface) == "" {
			return fmt.Errorf("mptcp_bonding: uplinks[%d].interface is empty", i)
		}
		if seen[u.Interface] {
			return fmt.Errorf("mptcp_bonding: uplink %q listed twice", u.Interface)
		}
		seen[u.Interface] = true
	}

	switch c.Policy {
	case PolicyGuestOnly, PolicyAll:
	case "":
		return fmt.Errorf("mptcp_bonding: policy is required")
	default:
		return fmt.Errorf("mptcp_bonding: unknown policy %q", c.Policy)
	}

	if c.Policy == PolicyGuestOnly {
		if c.SteeringCIDR == "" {
			return fmt.Errorf("mptcp_bonding: steering_cidr is required for policy %q", PolicyGuestOnly)
		}
		if _, _, err := net.ParseCIDR(c.SteeringCIDR); err != nil {
			return fmt.Errorf("mptcp_bonding: steering_cidr %q is not a CIDR: %w", c.SteeringCIDR, err)
		}
	}

	if c.Subflows == 0 {
		c.Subflows = DefaultSubflows
	}
	if c.Subflows < 1 {
		return fmt.Errorf("mptcp_bonding: subflows must be >= 1, got %d", c.Subflows)
	}

	if c.SocksPort != 0 && (c.SocksPort < 1 || c.SocksPort > 65535) {
		return fmt.Errorf("mptcp_bonding: socks_port %d out of range", c.SocksPort)
	}
	if c.TproxyPort != 0 && (c.TproxyPort < 1 || c.TproxyPort > 65535) {
		return fmt.Errorf("mptcp_bonding: tproxy_port %d out of range", c.TproxyPort)
	}
	if c.SocksPort != 0 && c.SocksPort == c.TproxyPort {
		return fmt.Errorf("mptcp_bonding: socks_port and tproxy_port must differ")
	}

	if c.FwMark != "" {
		if _, err := strconv.ParseUint(strings.TrimPrefix(c.FwMark, "0x"), 16, 32); err != nil {
			return fmt.Errorf("mptcp_bonding: fwmark %q is not hex: %w", c.FwMark, err)
		}
	}

	return nil
}

// UplinkInterfaces returns the member interface names, in order.
func (c *Config) UplinkInterfaces() []string {
	out := make([]string, 0, len(c.Uplinks))
	for _, u := range c.Uplinks {
		out = append(out, u.Interface)
	}
	return out
}

// ShouldSteer reports whether a packet from srcIP should be diverted into
// the bond. Keeping this decision in one tested function means the nft rule
// and the Go code cannot drift apart in intent.
//
// Router-local destinations are never steered: doing so redirects the box's
// own management traffic into the proxy, which is how an operator loses SSH
// to a device in the field.
func (c *Config) ShouldSteer(srcIP, dstIP net.IP) bool {
	if !c.Enabled {
		return false
	}
	if srcIP == nil || dstIP == nil {
		return false
	}

	switch c.Policy {
	case PolicyAll:
		// Everything except the tunnel's own far end (steering the tunnel
		// into itself would loop).
		return !c.isServerIP(dstIP)
	case PolicyGuestOnly:
		if c.isServerIP(dstIP) {
			return false
		}
		_, netw, err := net.ParseCIDR(c.SteeringCIDR)
		if err != nil {
			return false
		}
		return netw.Contains(srcIP)
	default:
		return false
	}
}

// isServerIP reports whether ip is the configured far end.
func (c *Config) isServerIP(ip net.IP) bool {
	host, _, err := net.SplitHostPort(c.Server)
	if err != nil {
		return false
	}
	servers := net.ParseIP(host)
	return servers != nil && servers.Equal(ip)
}

// LocalDontSteerAddrs returns addresses that must never be diverted, for
// rendering into the firewall rule. Callers should also add the box's own
// interface addresses.
func (c *Config) LocalDontSteerAddrs() []string {
	var out []string
	if host, _, err := net.SplitHostPort(c.Server); err == nil && host != "" {
		out = append(out, host)
	}
	if c.Policy == PolicyGuestOnly && c.SteeringCIDR != "" {
		if _, netw, err := net.ParseCIDR(c.SteeringCIDR); err == nil {
			out = append(out, netw.String())
		}
	}
	return out
}
