package fips_exit

import (
	"fmt"
	"net/netip"
	"strings"
)

// RenderBootstrapDNS returns a DNS-only dnsmasq configuration with one exact
// portal record and no upstream forwarding. Disabled ignores all other inputs.
// It performs no I/O and establishes no DNS isolation/readiness. A future caller
// must use an isolated --conf-file and dedicated user, without inherited options.
func RenderBootstrapDNS(enabled bool, listen netip.AddrPort, portalName string, portalIP netip.Addr) (string, error) {
	if !enabled {
		return "", nil
	}
	if !bootstrapUnicast(listen.Addr()) || listen.Port() < 1024 {
		return "", fmt.Errorf("bootstrap DNS listen requires an unzoned unicast IP and port 1024..65535")
	}
	if !bootstrapHostname(portalName) {
		return "", fmt.Errorf("bootstrap DNS portal requires an exact ASCII hostname")
	}
	if !bootstrapUnicast(portalIP) {
		return "", fmt.Errorf("bootstrap DNS portal requires an unzoned unicast IP")
	}
	return fmt.Sprintf("no-resolv\nno-hosts\nno-poll\nlocal=/#/\nlisten-address=%s\nport=%d\nbind-interfaces\nhost-record=%s,%s\n", listen.Addr().Unmap(), listen.Port(), portalName, portalIP.Unmap()), nil
}

func bootstrapUnicast(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func bootstrapHostname(name string) bool {
	// Deliberately reject trailing dots, empty labels, escapes and non-LDH bytes.
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
