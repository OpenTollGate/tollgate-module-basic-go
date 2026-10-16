// MPTCP endpoint and limits provisioning for the bond.
//
// This file renders the kernel-side MPTCP configuration that makes the bond
// actually aggregate. It exists because the lab proved that having MPTCP
// enabled is NOT the same as having a working bond: the single most common
// silent failure is an endpoint registered without the `signal` flag, which
// looks configured and never advertises the address, so the peer never opens
// a second subflow. See docs/MPTCP-BONDING.md.
//
// Like config.go this file is pure: it renders command strings and performs
// no I/O, so the plan can be asserted in a unit test on any machine.
package mptcp_bonding

import (
	"fmt"
	"strings"
)

// PrerequisitePackages lists the packages that must be installed on the
// device for the rendered commands to work at all.
//
// ip-full is not optional: OpenWrt's default BusyBox `ip` has no `mptcp`
// subcommand, so every endpoint/limits command below fails with a usage
// error on a stock image. This is a package-installation concern, which is
// why it is declared here rather than hidden in the renderer.
func PrerequisitePackages() []string {
	return []string{"ip-full"}
}

// EndpointPlan returns one `ip mptcp endpoint add` command per uplink that
// has a known local address.
//
// Every endpoint is marked BOTH `signal` and `subflow`:
//
//	signal  — make the kernel send ADD_ADDR for this address, so the peer
//	          learns it. Without this, no second subflow can ever form.
//	subflow — permit this address to be used for a subflow.
//
// Uplinks with no address are skipped rather than rendered with an empty
// address, because the latter is a shell error on the device. An addressless
// uplink is not fatal: the address may be discovered at runtime.
func (c *Config) EndpointPlan() []string {
	if !c.Enabled {
		return nil
	}
	out := make([]string, 0, len(c.Uplinks))
	for _, u := range c.Uplinks {
		addr := strings.TrimSpace(u.Address)
		iface := strings.TrimSpace(u.Interface)
		if addr == "" || iface == "" {
			continue
		}
		out = append(out, fmt.Sprintf("ip mptcp endpoint add %s dev %s signal subflow", addr, iface))
	}
	return out
}

// LimitsSetCommand returns the `ip mptcp limits set` command for this bond.
//
// Two ceilings matter:
//
//   - subflow: how many subflows this host may open. It must be at least the
//     number of uplinks, or the extra uplinks are configured but never used —
//     a silent no-op that looks like a working bond.
//   - add_addr_accepted: how many peer-supplied addresses this host will accept.
//     It must cover the peer's uplink count or the peer's ADD_ADDR is refused
//     and the bond collapses to one path.
//
// The kernel defaults (2 subflows) are too small for a multi-WAN box, which is
// exactly why this must be rendered explicitly rather than left to default.
func (c *Config) LimitsSetCommand() string {
	if !c.Enabled {
		return ""
	}

	uplinks := len(c.Uplinks)

	subflows := c.Subflows
	if subflows == 0 {
		subflows = DefaultSubflows
	}
	if subflows < uplinks {
		subflows = uplinks
	}

	// We cannot know the peer's uplink count, so accept at least as many
	// addresses as we ourselves advertise: symmetric bonds are the common
	// case, and accepting more than needed costs nothing.
	accept := uplinks
	if accept < 1 {
		accept = 1
	}

	return fmt.Sprintf("ip mptcp limits set subflow %d add_addr_accepted %d", subflows, accept)
}

// LimitsSetCommandString is the shell form used when composing a script.
func (c *Config) LimitsSetCommandString() string {
	if cmd := c.LimitsSetCommand(); cmd != "" {
		return cmd + "\n"
	}
	return ""
}
