package fips_exit

import (
	"fmt"
	"net/netip"
)

// RenderDNSOutput renders an inet fw4 table-body OUTPUT restriction, not an
// authorization grant. It performs no I/O. uid must belong exclusively to the
// unprivileged DNS process; callers must verify account ownership and key curve
// membership. Install and verify confinement before starting that process.
func RenderDNSOutput(uid int64, fipsInterface, guestInterface string, listen netip.AddrPort, localKey, exitKey [32]byte, resolver netip.AddrPort) (string, error) {
	if uid <= 0 || uid >= 4294967295 {
		return "", fmt.Errorf("DNS UID must be nonroot and below the invalid uint32 sentinel")
	}
	if !interfaceName.MatchString(fipsInterface) || !interfaceName.MatchString(guestInterface) || fipsInterface == guestInterface || fipsInterface == "lo" || guestInterface == "lo" {
		return "", fmt.Errorf("DNS FIPS and guest interfaces must be valid, distinct and non-loopback")
	}
	if !bootstrapUnicast(listen.Addr()) || !listen.Addr().Unmap().Is4() || listen.Addr().Unmap().IsLoopback() || listen.Port() < 1024 {
		return "", fmt.Errorf("DNS guest listener requires non-loopback unicast IPv4 and a high port")
	}
	if _, err := RenderUpstreamDNS(localKey, exitKey, resolver); err != nil {
		return "", err
	}
	localIP, _ := Derive(localKey)
	exitIP, _ := Derive(exitKey)
	if localIP == exitIP {
		return "", fmt.Errorf("DNS derived source and exit must be distinct")
	}
	family := "ip"
	if resolver.Addr().Unmap().Is6() {
		family = "ip6"
	}
	// return ends this base chain only: subsequent fw4 OUTPUT chains still apply.
	// Conntrack reply direction prevents established outbound guest connections
	// from becoming an escape through the listener's source port.
	return fmt.Sprintf(`# DNS output confinement: include inside table inet fw4.
chain tg_fexit_dns_output {
    type filter hook output priority -2; policy accept;
    meta skuid != %d return
    oifname %q ip6 saddr %s ip6 daddr %s udp dport 53 return
    oifname "lo" %s daddr %s meta l4proto { udp, tcp } th dport %d return
    oifname %q ip saddr %s ct state established ct direction reply meta l4proto { udp, tcp } th sport %d return
    counter drop
}
`, uid, fipsInterface, localIP, exitIP, family, resolver.Addr().Unmap(), resolver.Port(), guestInterface, listen.Addr().Unmap(), listen.Port()), nil
}
