package fips_exit

import (
	"fmt"
	"net/netip"
	"testing"
)

func TestRenderUpstreamDNSExact(t *testing.T) {
	local, exit := [32]byte{1}, [32]byte{2}
	localIP, _ := Derive(local)
	exitIP, _ := Derive(exit)
	for _, tc := range []struct {
		resolver, address string
		port              uint16
	}{
		{"127.0.0.1:5353", "127.0.0.1", 5353},
		{"[::1]:53", "::1", 53},
		{"127.12.34.56:1", "127.12.34.56", 1},
		{"[::ffff:127.0.0.1]:65535", "127.0.0.1", 65535},
	} {
		t.Run(tc.resolver, func(t *testing.T) {
			got, err := RenderUpstreamDNS(local, exit, netip.MustParseAddrPort(tc.resolver))
			want := fmt.Sprintf("server=%s#53@%s\nserver=/fips/%s#%d\n", exitIP, localIP, tc.address, tc.port)
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestRenderUpstreamDNSRejectKeys(t *testing.T) {
	for _, keys := range [][2][32]byte{
		{{}, {2}}, {{1}, {}}, {{}, {}}, {{1}, {1}},
	} {
		got, err := RenderUpstreamDNS(keys[0], keys[1], netip.MustParseAddrPort("127.0.0.1:5353"))
		if err == nil || got != "" {
			t.Fatalf("keys %v: got %q, %v; want empty and error", keys, got, err)
		}
	}
}

func TestRenderUpstreamDNSRejectResolver(t *testing.T) {
	resolvers := []netip.AddrPort{{}, netip.AddrPortFrom(netip.Addr{}, 53)}
	for _, s := range []string{
		"127.0.0.1:0", "[::1]:0", "0.0.0.0:53", "[::]:53",
		"192.0.2.1:53", "[fd00::1]:53", "[fe80::1]:53",
		"224.0.0.1:53", "[ff02::1]:53", "[::ffff:192.0.2.1]:53",
		"[::1%lo]:53", "[::1%lo\nserver=evil]:53",
	} {
		resolvers = append(resolvers, netip.MustParseAddrPort(s))
	}
	for _, resolver := range resolvers {
		t.Run(resolver.String(), func(t *testing.T) {
			got, err := RenderUpstreamDNS([32]byte{1}, [32]byte{2}, resolver)
			if err == nil || got != "" {
				t.Fatalf("got %q, %v; want empty and error", got, err)
			}
		})
	}
}
