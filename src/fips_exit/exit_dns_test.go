package fips_exit

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestRenderExitDNSDisabled(t *testing.T) {
	got, err := RenderExitDNS(false, netip.AddrPort{}, "*\nserver=evil", netip.Addr{}, [32]byte{}, [32]byte{}, netip.AddrPort{})
	if got != "" || err != nil {
		t.Fatalf("disabled: %q, %v", got, err)
	}
}

func TestRenderExitDNSReject(t *testing.T) {
	for _, field := range []string{"listen", "port", "name", "portal", "local", "exit", "same keys", "resolver", "resolver port"} {
		t.Run(field, func(t *testing.T) {
			listen := netip.MustParseAddrPort("192.0.2.1:5353")
			portal := netip.MustParseAddr("192.0.2.2")
			resolver := netip.MustParseAddrPort("127.0.0.1:5354")
			name := "portal.example"
			local, exit := [32]byte{1}, [32]byte{2}
			switch field {
			case "listen":
				listen = netip.MustParseAddrPort("0.0.0.0:5353")
			case "port":
				listen = netip.MustParseAddrPort("192.0.2.1:53")
			case "name":
				name = "portal\nserver=evil"
			case "portal":
				portal = netip.Addr{}
			case "local":
				local = [32]byte{}
			case "exit":
				exit = [32]byte{}
			case "same keys":
				exit = local
			case "resolver":
				resolver = netip.MustParseAddrPort("192.0.2.3:53")
			case "resolver port":
				resolver = netip.MustParseAddrPort("127.0.0.1:0")
			}
			if got, err := RenderExitDNS(true, listen, name, portal, local, exit, resolver); err == nil || got != "" {
				t.Fatalf("got %q, %v; want empty and error", got, err)
			}
		})
	}
}

func TestRenderExitDNSExact(t *testing.T) {
	local, exit := [32]byte{1}, [32]byte{2}
	localIP, _ := Derive(local)
	exitIP, _ := Derive(exit)
	for _, tc := range []struct{ listen, portal, resolver string }{
		{"192.0.2.1:5353", "192.0.2.2", "127.0.0.1:5354"},
		{"[fd00::1]:65535", "fd00::2", "[::1]:53"},
		{"[::ffff:192.0.2.1]:1024", "::ffff:192.0.2.2", "[::ffff:127.0.0.1]:1"},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			listen := netip.MustParseAddrPort(tc.listen)
			portal := netip.MustParseAddr(tc.portal)
			resolver := netip.MustParseAddrPort(tc.resolver)
			got, err := RenderExitDNS(true, listen, "portal.example", portal, local, exit, resolver)
			want := fmt.Sprintf("no-resolv\nno-hosts\nno-poll\nedns-packet-max=1232\nlisten-address=%s\nport=%d\nbind-interfaces\nhost-record=portal.example,%s\nserver=%s#53@%s\nserver=/fips/%s#%d\n", listen.Addr().Unmap(), listen.Port(), portal.Unmap(), exitIP, localIP, resolver.Addr().Unmap(), resolver.Port())
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
			if strings.Contains(got, "local=/#/") {
				t.Fatal("bootstrap catch-all would suppress exit forwarding")
			}
		})
	}
}
