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
// Each tunnel therefore binds its endpoint to its uplink's source address
// (`bind <LocalAddr>`), which is what keeps the underlays genuinely distinct.
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
	// LocalAddr is the uplink's source address; the endpoint is bound to it so
	// the tunnel cannot migrate to another uplink.
	LocalAddr string
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
			fmt.Sprintf("wg set %s peer %s endpoint %s:%d allowed-ips 0.0.0.0/0 persistent-keepalive %d bind %s",
				iface, t.PeerPubKey, t.PeerHost, t.PeerPort, ka, t.LocalAddr),
			fmt.Sprintf("ip -4 address add %s dev %s", t.Address, iface),
			fmt.Sprintf("ip link set %s up", iface),
		)
	}
	return plan
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
		b.WriteString(fmt.Sprintf("# Pinned to uplink %s via bind %s\n", t.Interface, t.LocalAddr))
		b.WriteString("[Interface]\n")
		fmt.Fprintf(&b, "PrivateKey = %s\n", t.PrivateKey)
		fmt.Fprintf(&b, "Address = %s\n", t.Address)
		fmt.Fprintf(&b, "ListenPort = %d\n\n", listenPortFor(t))
		b.WriteString("[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", t.PeerPubKey)
		fmt.Fprintf(&b, "Endpoint = %s:%d\n", t.PeerHost, t.PeerPort)
		b.WriteString("AllowedIPs = 0.0.0.0/0\n")
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", t.keepalive())
		// wg-quick supports binding the outgoing socket to an address, which is
		// what keeps this tunnel on its own uplink.
		fmt.Fprintf(&b, "BindAddress = %s\n", t.LocalAddr)

		files["/etc/wireguard/"+t.Name+".conf"] = b.String()
	}
	return files
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
