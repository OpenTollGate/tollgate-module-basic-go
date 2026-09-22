package upstream_detector

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

// TestIsDefaultRoute pins the netlink encoding duality that broke gateway
// selection (#454): default routes arrive with Dst nil OR as an explicitly
// parsed 0.0.0.0/0 / ::/0 IPNet depending on dump path and library
// version — the old `Dst == nil` comparison only ever matched the first
// form, so every IPv4 default route was invisible and x.x.x.1 inference
// won.
func TestIsDefaultRoute(t *testing.T) {
	cases := []struct {
		name  string
		route netlink.Route
		want  bool
	}{
		{"nil dst", netlink.Route{Dst: nil}, true},
		{"parsed 0.0.0.0/0 (the bug)", netlink.Route{Dst: mustCIDR(t, "0.0.0.0/0")}, true},
		{"parsed ::/0", netlink.Route{Dst: mustCIDR(t, "::/0")}, true},
		{"subnet route", netlink.Route{Dst: mustCIDR(t, "172.29.0.0/24")}, false},
		{"host route", netlink.Route{Dst: mustCIDR(t, "10.0.0.1/32")}, false},
		{"v6 subnet", netlink.Route{Dst: mustCIDR(t, "fe80::/64")}, false},
		{"nil IP in net", netlink.Route{Dst: &net.IPNet{Mask: net.CIDRMask(0, 32)}}, false},
	}
	for _, c := range cases {
		if got := isDefaultRoute(c.route); got != c.want {
			t.Errorf("isDefaultRoute(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBestDefaultGatewayPrefersLowestMetric pins kernel route-selection
// order through the production helper: several defaults on a link, the
// lowest metric wins — the old first-match code took whatever the dump
// order gave.
func TestBestDefaultGatewayPrefersLowestMetric(t *testing.T) {
	routes := []netlink.Route{
		{Dst: mustCIDR(t, "0.0.0.0/0"), Gw: net.ParseIP("172.29.0.10"), Priority: 100},
		{Dst: mustCIDR(t, "0.0.0.0/0"), Gw: net.ParseIP("172.29.0.20"), Priority: 50},
		{Dst: mustCIDR(t, "172.29.0.0/24")},
	}
	best, metric := bestDefaultGateway(routes)
	if best != "172.29.0.20" || metric != 50 {
		t.Fatalf("lowest-metric default not selected: gw=%q metric=%d, want 172.29.0.20/50", best, metric)
	}

	none, m := bestDefaultGateway([]netlink.Route{{Dst: mustCIDR(t, "172.29.0.0/24")}})
	if none != "" || m != -1 {
		t.Fatalf("non-default routes must yield no gateway, got %q/%d", none, m)
	}
}
