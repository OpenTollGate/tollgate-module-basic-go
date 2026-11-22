package mptcp_bonding

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func steeringFixture() *Config {
	return &Config{
		Enabled:      true,
		Policy:       PolicyGuestOnly,
		Server:       "203.0.113.10:65101",
		Method:       "chacha20-ietf-poly1305",
		Password:     "fixture",
		SteeringCIDR: "10.115.249.0/24",
		FwMark:       "0x1",
		FwMarkTable:  100,
		TproxyPort:   1082,
		Uplinks: []Uplink{
			{Interface: "eth1", Address: "192.0.2.10"},
			{Interface: "phy1-sta0", Address: "198.51.100.7"},
		},
	}
}

func TestSteeringRequiresNDSAuthorisation(t *testing.T) {
	rules := steeringFixture().SteeringNFTRules()
	if rules == "" {
		t.Fatal("guest_only with a steering CIDR must render a chain")
	}
	for _, want := range []string{
		"chain mptcp_steer",
		"priority -140",
		"meta mark & 0x00030000 == 0x00020000",
		"meta mark & 0x00030000 == 0x00030000",
		"tproxy ip to :1082",
		"meta mark set 0x1",
		"__LOCAL_ADDRS__",
		"203.0.113.10",
		"10.115.249.0/24",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("steering rules missing %q:\n%s", want, rules)
		}
	}
	// Every source rule must carry an NDS mark; a CIDR-only rule would steer
	// pre-auth traffic into the paid bond.
	lines := 0
	for _, line := range strings.Split(rules, "\n") {
		if strings.Contains(line, "ip saddr 10.115.249.0/24") {
			lines++
			if !strings.Contains(line, "meta mark & 0x00030000") {
				t.Errorf("steering rule has no NDS mark (pre-auth bypass): %s", line)
			}
		}
	}
	if lines != 2 {
		t.Errorf("expected trusted + authenticated rules, got %d:\n%s", lines, rules)
	}
}

func TestSteeringPolicyAllUsesAnySource(t *testing.T) {
	c := steeringFixture()
	c.Policy = PolicyAll
	c.SteeringCIDR = ""
	rules := c.SteeringNFTRules()
	if !strings.Contains(rules, "ip saddr 0.0.0.0/0") {
		t.Fatalf("policy=all must steer any marked source:\n%s", rules)
	}
	// The server must still be excluded from diversion.
	if !strings.Contains(rules, "203.0.113.10") {
		t.Error("policy=all must still exclude the bond server")
	}
}

func TestSteeringGuestOnlyWithoutCIDRRendersNothing(t *testing.T) {
	c := steeringFixture()
	c.SteeringCIDR = ""
	if got := c.SteeringNFTRules(); got != "" {
		t.Fatalf("guest_only without a CIDR is invalid; renderer must not guess:\n%s", got)
	}
}

func TestSteeringApplyScriptInstallsRuleAndReloadsIdempotently(t *testing.T) {
	script := steeringFixture().SteeringApplyScript()
	for _, want := range []string{
		"/etc/nftables.d/50-mptcp-steering.nft",
		"cmp -s",
		`if [ "$CHANGED" = 1 ]`,
		"fw4 reload",
		"ip rule add fwmark 0x1/0x1 lookup 100 pref 90",
		"ip route replace local default dev lo table 100",
		"nft list chain inet fw4 mptcp_steer",
		`tr '\n' ','`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("steering apply script missing %q:\n%s", want, script)
		}
	}
}

func TestSteeringDoesNotRequireBusyBoxMissingPaste(t *testing.T) {
	// OpenWrt's BusyBox has no `paste`; it made the whole apply exit 127.
	script := steeringFixture().SteeringApplyScript()
	if strings.Contains(script, "paste ") {
		t.Fatalf("steering script must not require paste:\n%s", script)
	}
	if !strings.Contains(script, `tr '\n' ','`) {
		t.Fatalf("steering script must join local addresses with tr/sed:\n%s", script)
	}
}

func TestSteeringFlushRemovesEvenWhenDisabled(t *testing.T) {
	c := steeringFixture()
	c.Enabled = false
	flush := c.SteeringFlushScript()
	if flush == "" {
		t.Fatal("disable path must remove steering even when the bond is already disabled")
	}
	for _, want := range []string{
		"rm -f \"$FILE\"",
		"fw4 reload",
		"ip rule del fwmark 0x1/0x1 lookup 100 pref 90",
		"ip route flush table 100",
	} {
		if !strings.Contains(flush, want) {
			t.Errorf("steering flush missing %q:\n%s", want, flush)
		}
	}
}

func TestSteeringScriptsAreValidShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}
	for name, script := range map[string]string{
		"apply": steeringFixture().SteeringApplyScript(),
		"flush": steeringFixture().SteeringFlushScript(),
	} {
		path := filepath.Join(t.TempDir(), name+".sh")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("%s script is not valid shell: %v\n%s", name, err, out)
		}
	}
}
