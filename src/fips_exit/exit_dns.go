package fips_exit

import (
	"fmt"
	"net/netip"
)

// RenderExitDNS composes a DNS-only dnsmasq configuration for the selected exit.
// Disabled ignores all other inputs. This pure, config-only helper performs no
// I/O and configures no firewall, routing, payment or runtime services. It proves
// neither DNS isolation nor exit readiness. Callers must use an isolated
// --conf-file and dedicated user, without inherited options, and validate key
// curve membership as required by RenderUpstreamDNS.
func RenderExitDNS(enabled bool, listen netip.AddrPort, portalName string, portalIP netip.Addr, localKey, exitKey [32]byte, resolver netip.AddrPort) (string, error) {
	if !enabled {
		return "", nil
	}
	// Reuse bootstrap input validation, but never its no-forwarding config.
	if _, err := RenderBootstrapDNS(true, listen, portalName, portalIP); err != nil {
		return "", err
	}
	upstream, err := RenderUpstreamDNS(localKey, exitKey, resolver)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("no-resolv\nno-hosts\nno-poll\nedns-packet-max=1232\nlisten-address=%s\nport=%d\nbind-interfaces\nhost-record=%s,%s\n", listen.Addr().Unmap(), listen.Port(), portalName, portalIP.Unmap()) + upstream, nil
}
