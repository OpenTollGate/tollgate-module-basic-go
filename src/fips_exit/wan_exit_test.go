package fips_exit

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func validWANConfig() WANExitConfig {
	return WANExitConfig{
		Enabled:      true,
		WANInterface: "wan",
		LANInterface: "br-lan",
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("addr %q: %v", s, err)
	}
	return a
}

func TestWANExitDisabledRendersNothing(t *testing.T) {
	got, err := RenderWANExit(WANExitConfig{Enabled: false, WANInterface: "wan"}, nil)
	if err != nil {
		t.Fatalf("disabled must not error: %v", err)
	}
	if got != "" {
		t.Fatalf("disabled rendered %q", got)
	}
}

func TestWANExitRejectsBadInterfaceNames(t *testing.T) {
	// Each of these would be interpolated into a rule, so each is a way to inject.
	for _, name := range []string{"", "bad\"name", "bad\nname", "waytoolonginterfacename", "bad name"} {
		c := validWANConfig()
		c.WANInterface = name
		if got, err := RenderWANExit(c, nil); err == nil || got != "" {
			t.Fatalf("interface %q accepted: %q, %v", name, got, err)
		}
	}
}

func TestWANExitRejectsSameInterfaceBothSides(t *testing.T) {
	c := validWANConfig()
	c.LANInterface = c.WANInterface
	if _, err := RenderWANExit(c, nil); err == nil {
		t.Fatal("forwarding an interface to itself accepted")
	}
}

func TestWANExitRejectsUnusablePeers(t *testing.T) {
	c := validWANConfig()
	for _, addr := range []string{"0.0.0.0", "127.0.0.1", "224.0.0.1"} {
		peers := []WANPeer{{Address: mustAddr(t, addr), BytesGranted: 1}}
		if _, err := RenderWANExit(c, peers); err == nil {
			t.Fatalf("peer %s accepted as a traffic source", addr)
		}
	}
	// A v6 peer would render a rule that matches nothing, so it is refused rather
	// than silently doing nothing.
	peers := []WANPeer{{Address: mustAddr(t, "fd00::1"), BytesGranted: 1}}
	if _, err := RenderWANExit(c, peers); err == nil {
		t.Fatal("a v6 peer accepted by a v4 exit")
	}
}

func TestWANExitRejectsDuplicatePeers(t *testing.T) {
	c := validWANConfig()
	a := mustAddr(t, "192.168.1.50")
	peers := []WANPeer{{Address: a, BytesGranted: 1}, {Address: a, BytesGranted: 2}}
	if _, err := RenderWANExit(c, peers); err == nil {
		t.Fatal("duplicate peer accepted; two rules would split one peer's counter")
	}
}

func TestWANExitIsDenyByDefault(t *testing.T) {
	// With no peers, there must still be a drop rule scoped to the LAN-to-WAN
	// direction, or the exit forwards for free.
	got, err := RenderWANExit(validWANConfig(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "iifname $tg_wan_exit_lan oifname $tg_wan_exit_wan counter drop") {
		t.Fatalf("no default drop rule:\n%s", got)
	}
	if strings.Contains(got, "masquerade") {
		t.Fatalf("NAT rendered with no peers to NAT:\n%s", got)
	}
}

func TestWANExitGrantsExactlyThePaidPeers(t *testing.T) {
	peers := []WANPeer{
		{Address: mustAddr(t, "192.168.1.50"), BytesGranted: 60000},
		{Address: mustAddr(t, "192.168.1.7"), BytesGranted: 120000},
	}
	got, err := RenderWANExit(validWANConfig(), peers)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"ip saddr 192.168.1.50 counter accept",
		"ip saddr 192.168.1.7 counter accept",
		"ip daddr 192.168.1.50 ct state established,related",
		"ip daddr 192.168.1.7 ct state established,related",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWANExitEveryRuleIsScopedToBothInterfaces(t *testing.T) {
	// A rule scoped to one interface would forward between whatever networks the
	// exit happens to route. Every accept and the drop must name both.
	peers := []WANPeer{{Address: mustAddr(t, "192.168.1.50"), BytesGranted: 1}}
	got, err := RenderWANExit(validWANConfig(), peers)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "counter accept") && !strings.Contains(line, "counter drop") {
			continue
		}
		if !strings.Contains(line, "$tg_wan_exit_lan") || !strings.Contains(line, "$tg_wan_exit_wan") {
			t.Fatalf("rule not scoped to both interfaces: %q", line)
		}
	}
}

func TestWANExitIsStableAcrossRunsAndOrderings(t *testing.T) {
	// Two renders of the same peers in different orders must be byte-identical, so
	// a diff means a real change rather than map iteration.
	a := []WANPeer{
		{Address: mustAddr(t, "192.168.1.7"), BytesGranted: 1},
		{Address: mustAddr(t, "192.168.1.50"), BytesGranted: 2},
	}
	b := []WANPeer{a[1], a[0]}
	first, err := RenderWANExit(validWANConfig(), a)
	if err != nil {
		t.Fatalf("render a: %v", err)
	}
	second, err := RenderWANExit(validWANConfig(), b)
	if err != nil {
		t.Fatalf("render b: %v", err)
	}
	if first != second {
		t.Fatalf("render depends on peer order:\n--- a ---\n%s\n--- b ---\n%s", first, second)
	}
}

// TestWANExitParsesAsNFTables is the only test here that proves the output is
// actually valid nftables rather than merely well-formed by our own reading.
//
// It needs root for a real ruleset, so it runs `nft -c` (check only, which parses
// without loading) and skips where nft is absent. A parser-only check does not
// prove the rules enforce - that needs a live dataplane - but it does catch the
// class of mistake where a rule reads correctly and the kernel rejects it.
func TestWANExitParsesAsNFTables(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft not installed, so the render cannot be parsed here")
	}

	peers := []WANPeer{
		{Address: mustAddr(t, "192.168.1.50"), BytesGranted: 60000},
		{Address: mustAddr(t, "192.168.1.7"), BytesGranted: 120000},
	}
	body, err := RenderWANExit(validWANConfig(), peers)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// The include is table-body content, so it needs a table and the defines the
	// real /etc/nftables.d layout provides. `ip`/`ifname` need no other setup.
	script := "table inet t {\n" + body + "}\n"
	path := t.TempDir() + "/wan-exit.nft"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := exec.Command(nft, "-c", "-f", path).CombinedOutput()
	text := string(out)
	// `nft -c` still initialises the netlink cache, which wants privileges. Rather
	// than skip - a skipped check proves nothing - retry through sudo, and only skip
	// if that is unavailable too. The distinction matters: this test is the only one
	// that shows the kernel accepts the render.
	if strings.Contains(text, "Operation not permitted") ||
		strings.Contains(text, "cache initialization failed") {
		sudo, lookErr := exec.LookPath("sudo")
		if lookErr != nil {
			t.Skipf("nft needs privileges to parse and sudo is absent: %s",
				strings.TrimSpace(text))
		}
		out, err = exec.Command(sudo, "-n", nft, "-c", "-f", path).CombinedOutput()
		text = string(out)
		if strings.Contains(text, "password is required") ||
			strings.Contains(text, "not permitted") && err != nil {
			t.Skipf("nft needs privileges to parse and sudo -n is unavailable: %s",
				strings.TrimSpace(text))
		}
	}
	if err != nil {
		t.Fatalf("nft rejected the render: %v\n%s\n--- rendered ---\n%s", err, text, script)
	}
}

// TestWANExitPlantedDriftIsCaught plants the two mistakes that would silently
// stop enforcing, and requires the render to differ - a fence that cannot fail is
// decoration.
func TestWANExitPlantedDriftIsCaught(t *testing.T) {
	base, err := RenderWANExit(validWANConfig(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Drift 1: a different LAN interface would scope the drop to the wrong place.
	c := validWANConfig()
	c.LANInterface = "br-guest"
	drifted, err := RenderWANExit(c, nil)
	if err != nil {
		t.Fatalf("render drifted: %v", err)
	}
	if drifted == base {
		t.Fatal("changing the LAN interface did not change the render")
	}

	// Drift 2: adding a peer must add a carve-out before the drop.
	peers := []WANPeer{{Address: mustAddr(t, "192.168.1.50"), BytesGranted: 1}}
	withPeer, err := RenderWANExit(validWANConfig(), peers)
	if err != nil {
		t.Fatalf("render with peer: %v", err)
	}
	if withPeer == base {
		t.Fatal("adding a paid peer did not change the render")
	}
	accept := strings.Index(withPeer, "ip saddr 192.168.1.50")
	drop := strings.Index(withPeer, "counter drop")
	if accept < 0 || drop < 0 || accept > drop {
		t.Fatalf("the carve-out must precede the drop:\n%s", withPeer)
	}
}
