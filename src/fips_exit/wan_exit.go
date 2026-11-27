package fips_exit

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// WANExitConfig describes an exit that sells transit on an ordinary IP network,
// with no FIPS tunnel at all.
//
// This is the alternative enforcement point. A FIPS exit can only be built once
// FIPS can shape a peer's transit rate; until then, an exit that fronts a plain
// LAN or WAN route can enforce with nftables, which the kernel already does well.
// The trade is real and worth stating: this exit can only see peers that reach it
// as ordinary IP addresses, so it cannot serve a peer several mesh hops away
// without a tunnel. What it can do is work today.
type WANExitConfig struct {
	// Enabled gates the whole render. A disabled config renders nothing.
	Enabled bool
	// WANInterface is the one interface this exit forwards toward. Every rule is
	// scoped to it, so the exit cannot accidentally forward between two other
	// networks it happens to route.
	WANInterface string
	// LANInterface is where paying clients arrive. Traffic from anywhere else is
	// not this exit's business and is left alone.
	LANInterface string
}

// WANPeer is one paid client, as a carve-out in the drop rule.
type WANPeer struct {
	// Address is the peer's IP as this exit sees it. This is the identity a
	// payment was bound to.
	Address netip.Addr
	// BytesGranted is what it bought, for the comment. The rule itself does not
	// meter: nftables counts, and the reader decides when the count exceeds this.
	BytesGranted uint64
}

// Validate checks only static configuration, never live state.
//
// Success does not authorize applying anything: the caller must still verify the
// interfaces exist and have the roles it assumes, because a rule scoped to the
// wrong interface is a rule that does not enforce.
func (c WANExitConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if err := validInterfaceName(c.WANInterface); err != nil {
		return fmt.Errorf("wan interface: %w", err)
	}
	if err := validInterfaceName(c.LANInterface); err != nil {
		return fmt.Errorf("lan interface: %w", err)
	}
	if c.WANInterface == c.LANInterface {
		return fmt.Errorf("wan and lan must be different interfaces; %q would forward to itself",
			c.WANInterface)
	}
	return nil
}

// validInterfaceName refuses anything that is not a plausible interface name.
//
// This matters more than it looks: the name is interpolated into a rule, so a
// value carrying a quote or a newline would let a config edit inject rules. Names
// are checked against the character set Linux permits rather than escaped, because
// an interface with an unexpected character is a deployment error worth reporting,
// not something to work around.
func validInterfaceName(name string) error {
	if name == "" {
		return fmt.Errorf("is required")
	}
	if len(name) > 15 {
		return fmt.Errorf("%q is longer than 15 characters, which no interface name is", name)
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '@'
		if !ok {
			return fmt.Errorf("%q contains %q, which is not an interface-name character",
				name, r)
		}
	}
	return nil
}

// RenderWANExit renders a table-body include for an exit that forwards paying
// clients to the internet over an ordinary interface.
//
// The shape is a deny-by-default forward chain in which every paid peer is one
// carve-out, plus a counter per peer so usage can be read back. Two properties are
// deliberate:
//
//   - The default is drop. A peer that stops paying is removed from the carve-out
//     list and falls to the drop rule, so revocation needs no second rule and
//     cannot be forgotten.
//   - Every rule is scoped by BOTH interfaces. Scoping by the LAN alone would
//     forward whatever arrived there to wherever the routing table said, including
//     back out another LAN the operator added later.
//
// This is an include for `table inet fw4`, not a ruleset and not runtime commands.
// `nft add` is never used: `fw4 reload` rebuilds from files and would silently wipe
// a runtime rule, so firewall state lives in files and this function writes one.
func RenderWANExit(c WANExitConfig, peers []WANPeer) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if !c.Enabled {
		return "", nil
	}

	seen := make(map[netip.Addr]bool, len(peers))
	for _, p := range peers {
		if !p.Address.IsValid() {
			return "", fmt.Errorf("a peer has no address, so no rule can name it")
		}
		if !p.Address.Is4() {
			// v4 only, and not as a limitation of the design: this exit transports
			// IPv4 internet traffic, so a v6 peer address would produce a rule that
			// matches nothing and a counter that reads zero forever.
			return "", fmt.Errorf("peer %s is not IPv4; this exit forwards IPv4 traffic",
				p.Address)
		}
		if p.Address.IsLoopback() || p.Address.IsUnspecified() || p.Address.IsMulticast() {
			return "", fmt.Errorf("peer %s is not an address traffic arrives from",
				p.Address)
		}
		if seen[p.Address] {
			return "", fmt.Errorf("peer %s appears twice; two rules for one peer would "+
				"split its counter in two", p.Address)
		}
		seen[p.Address] = true
	}

	// Sorted, so the rendered file is stable across runs and a diff of two renders
	// shows a real change rather than a map-ordering shuffle.
	ordered := make([]WANPeer, len(peers))
	copy(ordered, peers)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Address.Less(ordered[j].Address)
	})

	var b strings.Builder
	b.WriteString("# TollGate WAN exit: include inside table inet fw4.\n")
	b.WriteString("# Deny by default; each paid peer is one carve-out with its own counter.\n")
	fmt.Fprintf(&b, "define tg_wan_exit_wan = %q\n", c.WANInterface)
	fmt.Fprintf(&b, "define tg_wan_exit_lan = %q\n\n", c.LANInterface)

	b.WriteString("chain tg_wan_exit_forward {\n")
	b.WriteString("    type filter hook forward priority -2; policy accept;\n")
	// Count everything the paid set sends and receives, whether or not it is
	// admitted, so an operator can see a peer that is being refused rather than
	// only hearing that it complained.
	for _, p := range ordered {
		fmt.Fprintf(&b, "    iifname $tg_wan_exit_lan oifname $tg_wan_exit_wan "+
			"ip saddr %s counter accept comment \"granted %d bytes\"\n",
			p.Address, p.BytesGranted)
		fmt.Fprintf(&b, "    iifname $tg_wan_exit_wan oifname $tg_wan_exit_lan "+
			"ip daddr %s ct state established,related counter accept\n", p.Address)
	}
	// The default. Scoped to the LAN so traffic between other networks this exit
	// may also route is untouched.
	b.WriteString("    iifname $tg_wan_exit_lan oifname $tg_wan_exit_wan counter drop\n")
	b.WriteString("}\n\n")

	// Masquerade what the paid set sends, so replies come back to this exit.
	b.WriteString("chain tg_wan_exit_srcnat {\n")
	b.WriteString("    type nat hook postrouting priority 99; policy accept;\n")
	if len(ordered) > 0 {
		fmt.Fprintf(&b, "    iifname $tg_wan_exit_lan oifname $tg_wan_exit_wan "+
			"ip saddr { %s } masquerade\n", addressSet(ordered))
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// addressSet renders peers as one nft set literal, so the NAT rule is a single
// rule that stays small as peers are added.
func addressSet(peers []WANPeer) string {
	parts := make([]string, 0, len(peers))
	for _, p := range peers {
		parts = append(parts, p.Address.String())
	}
	return strings.Join(parts, ", ")
}
