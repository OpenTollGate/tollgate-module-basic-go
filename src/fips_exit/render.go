package fips_exit

import "fmt"

// RenderFW4 renders a table-body include, not a ruleset or runtime commands.
// portalInterface is the caller's resolved value of $tg_portal_if from
// 00-tollgate-defs.nft; it must match the planner's ingress selector. This
// equality is a static consistency check, NOT observation of installed rules.
// No guard readiness is established, and no files or kernel state are changed.
func RenderFW4(c Config, portalInterface string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if !c.Enabled {
		return "", nil
	}
	if portalInterface != c.GuestInterface {
		return "", fmt.Errorf("resolved tg_portal_if must match selected guest interface")
	}
	_, inner := Derive(c.LocalPublicKey)
	// -2 precedes the existing NDS bridge (-1) and fw4 filter (0). We only
	// restrict; return/policy accept continues through those authorization gates.
	// 99 precedes fw4 srcnat (100), scoped by BOTH ingress and tunnel egress.
	// INPUT allows only related IPv4 ICMP (e.g. errors/PMTU), not tunnel services.
	// FORWARD allows guest return flows only; all other tunnel ingress is dropped.
	// Match decapsulated tunnel ingress, never the outer FIPS control interface.
	// MSS comparison never increases a smaller advertised value.
	return fmt.Sprintf(`# FIPS exit guard: include inside table inet fw4 after 00-tollgate-defs.nft.
define tg_fexit_tunnel_if = %q

chain tg_fexit_input {
    type filter hook input priority -2; policy accept;
    meta nfproto ipv4 iifname $tg_fexit_tunnel_if ip protocol icmp ct state related return
    iifname $tg_fexit_tunnel_if counter drop
}

chain tg_fexit_forward {
    type filter hook forward priority -2; policy accept;
    meta nfproto ipv6 iifname $tg_portal_if counter drop
    iifname $tg_portal_if oifname != $tg_fexit_tunnel_if counter drop
    meta nfproto ipv4 iifname $tg_portal_if oifname $tg_fexit_tunnel_if tcp flags & syn == syn tcp option maxseg size > %[2]d tcp option maxseg size set %[2]d
    meta nfproto ipv4 iifname $tg_fexit_tunnel_if oifname $tg_portal_if ct state established,related tcp flags & syn == syn tcp option maxseg size > %[2]d tcp option maxseg size set %[2]d
    meta nfproto ipv4 iifname $tg_fexit_tunnel_if oifname $tg_portal_if ct state established,related return
    iifname $tg_fexit_tunnel_if counter drop
}

chain tg_fexit_srcnat {
    type nat hook postrouting priority 99; policy accept;
    meta nfproto ipv4 iifname $tg_portal_if oifname $tg_fexit_tunnel_if snat ip to %[3]s
}
`, c.TunnelInterface, c.MTU-40, inner), nil
}
