package fips_exit

import (
	"fmt"
	"net/netip"
)

// RenderUpstreamDNS renders only two dnsmasq upstream server directives: public
// DNS via the selected exit's FIPS IPv6 address, bound to the local FIPS source,
// and the fips domain via an explicit loopback FIPS resolver.
// Keys are decoded x-only public keys; callers must validate curve membership.
// These directives alone do NOT confine DNS or create paid access. They neither
// disable inherited resolvers nor configure listeners, firewall rules, payment,
// routing or services. This pure helper performs no I/O or readiness checks.
func RenderUpstreamDNS(localKey, exitKey [32]byte, resolver netip.AddrPort) (string, error) {
	if localKey == ([32]byte{}) || exitKey == ([32]byte{}) || localKey == exitKey {
		return "", fmt.Errorf("DNS upstream requires distinct nonzero local and exit keys")
	}
	if !bootstrapUnicast(resolver.Addr()) || !resolver.Addr().Unmap().IsLoopback() || resolver.Port() == 0 {
		return "", fmt.Errorf("FIPS resolver requires an unzoned loopback IP and nonzero port")
	}
	exitIP, _ := Derive(exitKey)
	localIP, _ := Derive(localKey)
	return fmt.Sprintf("server=%s#53@%s\nserver=/fips/%s#%d\n", exitIP, localIP, resolver.Addr().Unmap(), resolver.Port()), nil
}
