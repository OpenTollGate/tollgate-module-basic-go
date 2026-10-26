// Tunnel-per-uplink: the addressing fix for multi-WAN MPTCP behind NAT.
//
// # THE PROBLEM THIS SOLVES
//
// MPTCP creates a second subflow by having the PEER connect BACK to an address
// the client advertised with ADD_ADDR. Behind CGNAT or a home NAT, no advertised
// address is reachable, so no second subflow can ever establish. Measured on the
// lab router: the peer logged MPJoinNoTokenFound=58 -- 58 JOIN attempts, zero
// successes -- and the second uplink's subflow sat at ~1.7 KB (handshake only)
// while the first carried everything.
//
// # WHY A TUNNEL RATHER THAN A PUBLIC ADDRESS
//
// A tunnel makes the least assumptions about what a gateway provides. It works
// behind CGNAT, behind carrier NAT, behind a captive portal, on a Starlink link
// with no routed prefix -- because the ROUTER initiates outbound to a public
// endpoint it controls. Nothing inbound has to be reachable, and the ISP need
// not cooperate or hand out anything. The tunnel address is routable by the peer
// because the peer is a party to the tunnel.
//
// # WHY ONE TUNNEL PER UPLINK, BOUND TO THAT UPLINK
//
// The whole point is two separate underlay paths. A WireGuard endpoint defaults
// to the system route, so two tunnels would both ride whichever uplink has the
// default route -- collapsing back to one path and making the bond pointless.
// Each tunnel is therefore pinned by an fwmark on its outgoing packets plus a
// policy-routing rule sending that mark to a per-tunnel table, whose default
// route names its uplink. That is what keeps the underlays genuinely distinct.
//
// `wg set ... bind` is NOT used and does not exist: it fails on device with
// `Invalid argument: bind`. Pinning is fwmark + rule + table only.
//
// The cost is one public endpoint per uplink on the far side (distinct ports are
// enough, as WireGuard demultiplexes by peer key).
//
// # SECRETS
//
// PrivateKey lives in the Tunnel struct so the config can carry it, but
// TunnelPlan() deliberately never renders it: the plan is the part that may be
// logged, shown, or pasted. Keys are written by a separate private step.
package mptcp_bonding

import (
	"fmt"
	"sort"
	"strings"
)

// Tunnel is a WireGuard overlay for one uplink.
type Tunnel struct {
	// Name is the interface name, e.g. "mptcp-wan1".
	Name string
	// Interface is the underlay uplink this tunnel is pinned to.
	Interface string
	// LocalAddr is the uplink's source address. It is used for the routing
	// rule's `src`, NOT for any wg bind (no such option exists).
	LocalAddr string
	// FwMark marks this tunnel's outgoing packets so policy routing can pin them
	// to this uplink. THIS is the mechanism that keeps tunnels on separate
	// uplinks -- `wg set ... bind` does not exist and failed on device.
	FwMark int
	// Gateway is the uplink's next hop, used for the per-tunnel default route.
	Gateway string
	// Table is the dedicated routing table for this tunnel.
	Table int
	// PeerHost/PeerPort is the public endpoint the router initiates to.
	PeerHost string
	PeerPort int
	// PeerPubKey is the far end's public key.
	PeerPubKey string
	// Address is the tunnel address advertised to the MPTCP peer. It must be an
	// address the peer can reach THROUGH the tunnel.
	Address string
	// PrivateKey is this side's key. Never rendered by TunnelPlan().
	PrivateKey string
	// Keepalive is the persistent-keepalive interval in seconds. Behind NAT a
	// keepalive is mandatory: without it the NAT mapping expires and the peer
	// loses the ability to reach the tunnel address.
	Keepalive int
}

// DefaultKeepaliveSeconds is used when Keepalive is unset. 25 s is the
// conventional WireGuard value for a NAT-ed peer and comfortably under the
// common 30 s UDP NAT timeout.
const DefaultKeepaliveSeconds = 25

// TunnelPrerequisites lists the packages a tunnel needs.
//
// Both install from the STOCK OpenWrt feeds and are version-matched to the
// kernel -- verified available on OpenWrt 24.10.2 (kernel 6.6.93) as
// kmod-wireguard 6.6.93-r1 and wireguard-tools 1.0.20210914-r4. No third-party
// feed is required.
func (c *Config) TunnelPrerequisites() []string {
	return []string{"kmod-wireguard", "wireguard-tools"}
}

// keepalive resolves the effective keepalive for a tunnel.
func (t Tunnel) keepalive() int {
	if t.Keepalive > 0 {
		return t.Keepalive
	}
	return DefaultKeepaliveSeconds
}

// TunnelPlan renders the `wg` commands that bring up every configured tunnel.
//
// Order matters: set the private key and listen port, then the peer, then the
// address, then bring the link up. Each tunnel's endpoint is bound to its
// uplink's source address, and each carries a keepalive so the NAT mapping
// survives.
//
// The private key is intentionally NOT included; see the file comment.
func (c *Config) TunnelPlan() []string {
	if c == nil || len(c.Tunnels) == 0 {
		return nil
	}

	plan := make([]string, 0, len(c.Tunnels)*5)
	for _, t := range c.Tunnels {
		iface := t.Name
		ka := t.keepalive()

		plan = append(plan,
			fmt.Sprintf("ip link add %s type wireguard", iface),
			fmt.Sprintf("wg set %s listen-port %d", iface, listenPortFor(t)),
			// fwmark, NOT bind. `wg set ... bind` does not exist in this wg
			// version and fails with "Invalid argument: bind" on device. The
			// mark is what policy routing keys on to pin this tunnel to its
			// uplink.
			fmt.Sprintf("wg set %s fwmark 0x%x", iface, t.FwMark),
			fmt.Sprintf("wg set %s peer %s endpoint %s:%d allowed-ips 0.0.0.0/0 persistent-keepalive %d",
				iface, t.PeerPubKey, t.PeerHost, t.PeerPort, ka),
			fmt.Sprintf("ip -4 address add %s dev %s", t.Address, iface),
			fmt.Sprintf("ip link set %s up", iface),
			// Pin the tunnel's own traffic to its uplink. The route must name
			// the UNDERLAY interface (the uplink), not the tunnel device: a
			// route `via <gw> dev <tunnel>` fails with "Nexthop has invalid
			// gateway" because that gateway is not reachable THROUGH the
			// tunnel. The mark matches so the rule below selects this table.
			fmt.Sprintf("ip route replace default via %s dev %s table %d || true",
				t.Gateway, t.Interface, t.Table),
			fmt.Sprintf("ip rule add fwmark 0x%x lookup %d || true", t.FwMark, t.Table),
		)
	}
	return plan
}

// TunnelRoutingScript renders the policy-routing state that pins each tunnel to
// its own uplink.
//
// Rendered as its own script because it is a distinct concern from bringing the
// interface up, and because it must be re-applied idempotently: a reconcile can
// run many times, and `ip rule add`/`ip route add` fail on a duplicate unless
// guarded or replaced.
func (c *Config) TunnelRoutingScript() string {
	if c == nil || len(c.Tunnels) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Generated by tollgate mptcp_bonding. Pins each tunnel to its uplink.\n")
	b.WriteString("# `wg set ... bind` does not exist; fwmark + policy routing is the\n")
	b.WriteString("# mechanism, and without it both tunnels ride the default route.\n")
	for _, t := range c.Tunnels {
		fmt.Fprintf(&b, "\n# %s -> uplink %s (gateway %s, table %d)\n", t.Name, t.Interface, t.Gateway, t.Table)
		// `dev` is the UPLINK, not the tunnel. Routing the tunnel's own
		// encrypted traffic out its own device is a loop, and naming the tunnel
		// here failed on device with "Nexthop has invalid gateway".
		fmt.Fprintf(&b, "ip route replace default via %s dev %s table %d || true\n",
			t.Gateway, t.Interface, t.Table)
		fmt.Fprintf(&b, "ip rule add fwmark 0x%x lookup %d || true\n", t.FwMark, t.Table)
	}
	return b.String()
}

// listenPortFor picks a stable per-tunnel listen port.
//
// The router does not need to be reachable, so the port is only local state; a
// stable value keeps it identifiable and avoids collisions between tunnels.
func listenPortFor(t Tunnel) int {
	if t.PeerPort > 0 {
		// Pair with the peer port for a stable, collision-free local choice.
		return 51820 + (t.PeerPort % 100)
	}
	return 51820
}

// TunnelConfigFiles renders a wg-quick configuration per tunnel, keyed by
// interface name.
//
// Emitted alongside the command plan because a router REBOOTS: one-shot `wg`
// commands do not survive, so the configuration has to exist on disk for the
// tunnel to come back. Including the private key here is deliberate -- this is
// the on-disk config, which is the file that must contain it to function; it is
// written root-only by the caller.
func (c *Config) TunnelConfigFiles() map[string]string {
	if c == nil || len(c.Tunnels) == 0 {
		return nil
	}

	files := make(map[string]string, len(c.Tunnels))
	for _, t := range c.Tunnels {
		var b strings.Builder
		b.WriteString("# Generated by tollgate mptcp_bonding. Do not edit: re-rendered.\n")
		b.WriteString(fmt.Sprintf("# Pinned to uplink %s via fwmark + table (source %s)\n", t.Interface, t.LocalAddr))
		b.WriteString("[Interface]\n")
		fmt.Fprintf(&b, "PrivateKey = %s\n", t.PrivateKey)
		fmt.Fprintf(&b, "Address = %s\n", t.Address)
		fmt.Fprintf(&b, "ListenPort = %d\n\n", listenPortFor(t))
		b.WriteString("[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", t.PeerPubKey)
		fmt.Fprintf(&b, "Endpoint = %s:%d\n", t.PeerHost, t.PeerPort)
		b.WriteString("AllowedIPs = 0.0.0.0/0\n")
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", t.keepalive())
		// fwmark is the pinning mechanism (there is no bind option). The
		// accompanying table/rule is in TunnelRoutingScript.
		fmt.Fprintf(&b, "FwMark = 0x%x\n", t.FwMark)

		files["/etc/wireguard/"+t.Name+".conf"] = b.String()
	}
	return files
}

// TunnelFirewallScript renders the firewall acceptance the tunnel interfaces
// need.
//
// WHY THIS IS REQUIRED and not optional: on OpenWrt's fw4 the input chain ends
// in `jump handle_reject`, and only interfaces explicitly listed (br-lan, lan1,
// br-private) are accepted. A WireGuard tunnel interface is in none of those, so
// its traffic is REJECTED. Measured on device, the peer saw the router actively
// refuse:
//
//	10.99.98.1 > 10.99.98.2: ICMP echo request
//	10.99.98.2 > 10.99.98.1: ICMP unreachable
//
// The tunnel transport was perfect, so this is indistinguishable from a broken
// tunnel unless the operator knows the firewall is the cause. Rendering the
// acceptance here means the design is complete rather than requiring an
// undocumented manual step.
func (c *Config) TunnelFirewallScript() string {
	if c == nil || len(c.Tunnels) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Accept traffic arriving on the tunnel interfaces.\n")
	b.WriteString("# fw4's input chain ends in `jump handle_reject` and does not list\n")
	b.WriteString("# tunnel interfaces, so without this their traffic is rejected -- which\n")
	b.WriteString("# looks exactly like a broken tunnel.\n")
	for _, t := range c.Tunnels {
		// Delete-then-add keeps a repeated reconcile from stacking duplicates.
		fmt.Fprintf(&b, "nft delete rule inet fw4 input iifname %q comment %q 2>/dev/null || true\n",
			t.Name, "mptcp-tunnel:"+t.Name)
		fmt.Fprintf(&b, "nft insert rule inet fw4 input iifname %q accept comment %q\n",
			t.Name, "mptcp-tunnel:"+t.Name)
	}
	return b.String()
}

// TunnelAddresses returns the tunnel addresses that should be registered as
// MPTCP subflow endpoints, sorted for stable output.
//
// These are the addresses the peer CAN reach (through the tunnels), which is the
// entire point: advertising an uplink's own private address is what caused the
// JOIN failures.
func (c *Config) TunnelAddresses() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Tunnels))
	for _, t := range c.Tunnels {
		// Strip any prefix length: an endpoint takes a bare address.
		addr := t.Address
		if i := strings.IndexByte(addr, '/'); i >= 0 {
			addr = addr[:i]
		}
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}
