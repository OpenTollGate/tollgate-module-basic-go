package mptcp_bonding

import (
	"strings"
	"testing"
)

// The MPTCP endpoint plan is the contract between TollGate's provisioning
// and the kernel. These tests pin the three facts that the lab proved the
// hard way (see docs/MPTCP-BONDING.md, "Endpoint signalling"):
//
//  1. An endpoint that is NOT marked "signal" is useless: the kernel never
//     sends ADD_ADDR, so the peer never learns the address and no second
//     subflow can ever form. A subflow-only endpoint looks configured and
//     silently does nothing.
//  2. add_addr_accepted must be raised on the receiving side, or the
//     ADD_ADDR is refused.
//  3. Every uplink's local address must be registered, and the far end
//     never should be, or the bond loops into its own tunnel.

func TestEndpointPlanMarksEveryUplinkSignalSubflow(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
	}

	plan := c.EndpointPlan()
	if len(plan) != 2 {
		t.Fatalf("want 2 endpoint commands, got %d: %v", len(plan), plan)
	}
	for _, cmd := range plan {
		// Both flags are mandatory. "signal" without "subflow" never joins;
		// "subflow" without "signal" never advertises.
		if !strings.Contains(cmd, "signal") || !strings.Contains(cmd, "subflow") {
			t.Errorf("endpoint must be marked both signal and subflow, got: %q", cmd)
		}
		if !strings.HasPrefix(cmd, "ip mptcp endpoint add ") {
			t.Errorf("endpoint command must use ip-full's mptcp subcommand, got: %q", cmd)
		}
	}
	if !strings.Contains(plan[0], "192.0.2.10") {
		t.Errorf("first endpoint must carry uplink[0]'s address, got: %q", plan[0])
	}
	if !strings.Contains(plan[1], "198.51.100.7") {
		t.Errorf("second endpoint must carry uplink[1]'s address, got: %q", plan[1])
	}
}

func TestEndpointPlanSkipsUplinksWithoutAddress(t *testing.T) {
	// An uplink with no known address cannot be registered. It is not an
	// error — the address may be discovered at runtime — but rendering an
	// endpoint command with an empty address would fail on the device.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0"}, // no address
	}

	plan := c.EndpointPlan()
	if len(plan) != 1 {
		t.Fatalf("want 1 endpoint (addressless uplink skipped), got %d: %v", len(plan), plan)
	}
	if strings.Contains(plan[0], "phy1-sta0") {
		t.Errorf("addressless uplink must be skipped, got: %q", plan[0])
	}
}

func TestEndpointPlanEmptyWhenDisabled(t *testing.T) {
	c := validConfig()
	c.Enabled = false
	if plan := c.EndpointPlan(); len(plan) != 0 {
		t.Fatalf("a disabled bond must render nothing, got: %v", plan)
	}
}

func TestLimitsSetCoversEveryUplink(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
		{Interface: "usb0", Address: "203.0.113.9"},
	}

	cmd := c.LimitsSetCommand()
	// The receiving side must accept at least as many ADD_ADDRs as we have
	// peers/addresses, and must permit at least as many subflows as uplinks.
	if !strings.Contains(cmd, "subflow") || !strings.Contains(cmd, "add_addr_accepted") {
		t.Fatalf("limits command must set both ceilings, got: %q", cmd)
	}
	// 3 uplinks => add_addr_accepted must be exactly 3, and the subflow
	// ceiling must be AT LEAST 3 (the default 8 satisfies it, but never the
	// kernel default of 2).
	if !strings.Contains(cmd, "add_addr_accepted 3") {
		t.Errorf("want add_addr_accepted 3 in limits command, got: %q", cmd)
	}
	if !limitsSubflowAtLeast(t, cmd, 3) {
		t.Errorf("subflow ceiling must be >= the uplink count (3), got: %q", cmd)
	}
}

// limitsSubflowAtLeast parses "subflow N" out of a limits command and reports
// whether N >= want.
func limitsSubflowAtLeast(t *testing.T, cmd string, want int) bool {
	t.Helper()
	const marker = "subflow "
	i := strings.Index(cmd, marker)
	if i < 0 {
		return false
	}
	rest := cmd[i+len(marker):]
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n >= want
}

func TestLimitsSetHonoursSubflowCeiling(t *testing.T) {
	// An explicit subflow ceiling must win over the uplink count, but never
	// drop below it: a ceiling below the uplink count silently leaves
	// uplinks unused.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
	}
	c.Subflows = 6
	cmd := c.LimitsSetCommand()
	if !strings.Contains(cmd, "subflow 6") {
		t.Errorf("explicit subflow ceiling must be honoured, got: %q", cmd)
	}
	if !strings.Contains(cmd, "add_addr_accepted 2") {
		t.Errorf("add_addr_accepted must track the uplink count, got: %q", cmd)
	}
}

func TestLimitsSetRaisesCeilingAboveUplinkCount(t *testing.T) {
	// A subflow ceiling BELOW the uplink count must be raised to it, or the
	// extra uplinks are configured but never used — a silent no-op.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
		{Interface: "usb0", Address: "203.0.113.9"},
	}
	c.Subflows = 2
	cmd := c.LimitsSetCommand()
	if !strings.Contains(cmd, "subflow 3") {
		t.Errorf("ceiling must be raised to the uplink count (3), got: %q", cmd)
	}
}

func TestLimitsSetEmptyWhenDisabled(t *testing.T) {
	c := validConfig()
	c.Enabled = false
	if cmd := c.LimitsSetCommand(); cmd != "" {
		t.Fatalf("a disabled bond must render no limits command, got: %q", cmd)
	}
}

func TestPrerequisitePackagesNamesIPFull(t *testing.T) {
	// BusyBox `ip` has no `mptcp` subcommand. Without ip-full every endpoint
	// command above fails on the device.
	pkgs := PrerequisitePackages()
	found := false
	for _, p := range pkgs {
		if p == "ip-full" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ip-full is required for the mptcp subcommand, got: %v", pkgs)
	}
}

// #974 defense-in-depth: LoadConfig rejects CIDR-form addresses, but
// programmatic callers bypass it — render-time must never emit an
// `endpoint add <addr>/<len>` the kernel would reject.
func TestEndpointPlanNormalizesCIDRToBare(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "veth0a", Address: "10.0.1.1/30"},
	}
	plan := c.EndpointPlan()
	if len(plan) != 1 {
		t.Fatalf("expected 1 endpoint command, got %d: %v", len(plan), plan)
	}
	if !strings.Contains(plan[0], "endpoint add 10.0.1.1 dev veth0a") ||
		strings.Contains(plan[0], "/30") {
		t.Errorf("CIDR must render as a bare address, got: %s", plan[0])
	}
}

func TestEndpointPlanSkipsUnparseableAddress(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "veth0a", Address: "bogus"},
		{Interface: "veth1a", Address: "10.0.2.1"},
	}
	plan := c.EndpointPlan()
	if len(plan) != 1 || !strings.Contains(plan[0], "10.0.2.1") {
		t.Errorf("unparseable address must be skipped, not rendered: %v", plan)
	}
}
