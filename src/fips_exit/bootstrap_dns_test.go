package fips_exit

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRenderBootstrapDNSDisabled(t *testing.T) {
	got, err := RenderBootstrapDNS(false, netip.AddrPort{}, "*\nserver=evil", netip.Addr{})
	if got != "" || err != nil {
		t.Fatalf("disabled: %q, %v", got, err)
	}
}

func TestRenderBootstrapDNSExact(t *testing.T) {
	for _, tc := range []struct{ listen, name, ip string }{
		{"192.0.2.1:1024", "portal.example", "192.0.2.2"},
		{"[fd00::1]:65535", "Portal-1.example", "fd00::2"},
		{"127.0.0.1:5353", "portal", "::1"},
		{"[fe80::1]:5353", strings.Repeat("a", 63) + ".example", "fe80::2"},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			listen := netip.MustParseAddrPort(tc.listen)
			got, err := RenderBootstrapDNS(true, listen, tc.name, netip.MustParseAddr(tc.ip))
			want := "no-resolv\nno-hosts\nno-poll\nlocal=/#/\nlisten-address=" + listen.Addr().String() + "\nport=" + strings.TrimPrefix(tc.listen[strings.LastIndex(tc.listen, ":"):], ":") + "\nbind-interfaces\nhost-record=" + tc.name + "," + tc.ip + "\n"
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestRenderBootstrapDNSReject(t *testing.T) {
	goodListen := netip.MustParseAddrPort("192.0.2.1:5353")
	goodIP := netip.MustParseAddr("192.0.2.2")
	check := func(t *testing.T, listen netip.AddrPort, name string, ip netip.Addr) {
		t.Helper()
		if got, err := RenderBootstrapDNS(true, listen, name, ip); err == nil || got != "" {
			t.Fatalf("got %q, %v; want empty and error", got, err)
		}
	}
	for _, name := range []string{"", "*", "*.example", ".portal", "portal.", "a..b", "-portal", "portal-", "a.-b", "a.b-", "portal_name", "pörtal", "portal\nserver=evil", "portal\r", "portal\x00", "portal,evil", "portal/evil", "portal#evil", "portal=evil", " portal", "portal ", strings.Repeat("a", 64), strings.Repeat("a.", 127) + "a"} {
		t.Run("name/"+name, func(t *testing.T) { check(t, goodListen, name, goodIP) })
	}
	check(t, netip.AddrPort{}, "portal", goodIP)
	for _, addr := range []string{"0.0.0.0", "::", "224.0.0.1", "ff02::1", "255.255.255.255", "fe80::1%eth0", "::ffff:0.0.0.0", "::ffff:224.0.0.1", "::ffff:255.255.255.255"} {
		t.Run("addr/"+addr, func(t *testing.T) {
			ip := netip.MustParseAddr(addr)
			check(t, netip.AddrPortFrom(ip, 5353), "portal", goodIP)
			check(t, goodListen, "portal", ip)
		})
	}
	check(t, goodListen, "portal", netip.Addr{})
	for _, port := range []uint16{0, 53, 1023} {
		check(t, netip.AddrPortFrom(goodListen.Addr(), port), "portal", goodIP)
	}
}
