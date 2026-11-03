package upstream_session_manager

import (
	"fmt"
	"net"
)

// gatewayURL builds http://<host>:<port>/<path> with IPv6 literals bracketed.
//
// The FIPS-mesh deployment carries IPv6 ULA gateway addresses, and an
// unbracketed literal produces "http://fd42::1:2121/" — an invalid URL whose
// host parses wrong. Every URL built from a gateway IP must go through this.
func gatewayURL(gatewayIP, port, path string) string {
	return fmt.Sprintf("http://%s:%s/%s", bracketHost(gatewayIP), port, path)
}

// bracketHost brackets a bare IPv6 literal; IPv4 and hostnames pass through.
func bracketHost(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "[" + host + "]"
	}
	return host
}
