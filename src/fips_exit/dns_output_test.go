package fips_exit

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRenderDNSOutput(t *testing.T) {
	local, exit := [32]byte{1}, [32]byte{2}
	src, _ := Derive(local)
	dst, _ := Derive(exit)
	for _, resolver := range []string{"127.0.0.1:5353", "[::1]:5353", "[::ffff:127.0.0.1]:5353"} {
		r := netip.MustParseAddrPort(resolver)
		got, err := RenderDNSOutput(453, "fips0", "br-guest", netip.MustParseAddrPort("192.168.8.1:1053"), local, exit, r)
		if err != nil {
			t.Fatal(err)
		}
		family := "ip"
		if r.Addr().Unmap().Is6() {
			family = "ip6"
		}
		want := fmt.Sprintf(`# DNS output confinement: include inside table inet fw4.
chain tg_fexit_dns_output {
    type filter hook output priority -2; policy accept;
    meta skuid != 453 return
    oifname "fips0" ip6 saddr %s ip6 daddr %s udp dport 53 return
    oifname "lo" %s daddr %s meta l4proto { udp, tcp } th dport 5353 return
    oifname "br-guest" ip saddr 192.168.8.1 ct state established ct direction reply meta l4proto { udp, tcp } th sport 1053 return
    counter drop
}
`, src, dst, family, r.Addr().Unmap())
		if got != want {
			t.Fatalf("unexpected confinement:\n%s\nwant:\n%s", got, want)
		}
		if os.Getenv("FIPS_DNS_NFT_CHECK") == "1" {
			check := func(text string) ([]byte, error) {
				cmd := exec.Command("sudo", "-n", "unshare", "-n", "nft", "-c", "-f", "-")
				cmd.Stdin = strings.NewReader("table inet fw4 {\n" + text + "}\n")
				return cmd.CombinedOutput()
			}
			if out, err := check(got); err != nil {
				t.Fatalf("isolated nft check: %v: %s", err, out)
			}
			if out, err := check(strings.Replace(got, "counter drop", "definitely_invalid_nft", 1)); err == nil || !strings.Contains(string(out), "syntax error") {
				t.Fatalf("negative control: %v: %s", err, out)
			}
			t.Logf("nft -c stdin privileged netns PASS; syntax-negative rejected: %s", resolver)
		}
	}
}

func TestRenderDNSOutputRejects(t *testing.T) {
	tests := []struct {
		name                          string
		uid                           int64
		fips, guest, listen, resolver string
		local, exit                   [32]byte
	}{
		{name: "root", uid: 0}, {name: "negative", uid: -1}, {name: "overflow", uid: 4294967296}, {name: "invalid sentinel", uid: 4294967295},
		{name: "bad fips", fips: "bad\""}, {name: "bad guest", guest: "*"}, {name: "same interfaces", guest: "fips0"}, {name: "fips loopback", fips: "lo"}, {name: "guest loopback", guest: "lo"},
		{name: "zero local", local: [32]byte{}}, {name: "zero exit", exit: [32]byte{}}, {name: "same keys", exit: [32]byte{1}},
		{name: "wildcard listener", listen: "0.0.0.0:1053"}, {name: "v6 listener", listen: "[fd00::1]:1053"}, {name: "loopback listener", listen: "127.0.0.1:1053"}, {name: "multicast listener", listen: "224.0.0.1:1053"}, {name: "low port", listen: "192.168.8.1:53"}, {name: "zero port", listen: "192.168.8.1:0"},
		{name: "remote resolver", resolver: "192.168.8.2:5353"}, {name: "zero resolver port", resolver: "127.0.0.1:0"}, {name: "zoned resolver", resolver: "[::1%lo]:5353"}, {name: "endpoint collision", listen: "127.0.0.1:5353"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid := int64(453)
			if tt.name == "root" || tt.uid != 0 {
				uid = tt.uid
			}
			fips, guest, listen, resolver := "fips0", "br-guest", "192.168.8.1:1053", "127.0.0.1:5353"
			if tt.fips != "" {
				fips = tt.fips
			}
			if tt.guest != "" {
				guest = tt.guest
			}
			if tt.listen != "" {
				listen = tt.listen
			}
			if tt.resolver != "" {
				resolver = tt.resolver
			}
			local, exit := [32]byte{1}, [32]byte{2}
			if tt.name == "zero local" {
				local = tt.local
			}
			if tt.name == "zero exit" || tt.name == "same keys" {
				exit = tt.exit
			}
			got, err := RenderDNSOutput(uid, fips, guest, netip.MustParseAddrPort(listen), local, exit, netip.MustParseAddrPort(resolver))
			if err == nil || got != "" {
				t.Fatalf("invalid configuration rendered: %q, %v", got, err)
			}
		})
	}
}
