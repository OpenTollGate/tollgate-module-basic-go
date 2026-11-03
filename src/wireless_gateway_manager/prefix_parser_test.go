package wireless_gateway_manager

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

// Drive both production readers from the same bytes and expected verdict.
// This is a host-shell parser contract, not a router integration test.
func TestSSIDPrefixParserParity(t *testing.T) {
	data, err := os.ReadFile("../../tests/contract/ssid-prefix-parser-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Line, Prefix string
		Accept             bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatal("prefix parser fixture is incomplete")
	}
	script, err := os.ReadFile("../../packaging/files/etc/uci-defaults/99-tollgate-setup")
	if err != nil {
		t.Fatal(err)
	}
	// Extract the shipped function without executing setup's router mutations.
	function := regexp.MustCompile(`(?m)^ssid_prefix_known\(\) \{\n(?:[^\n]*\n)*?^\}`).Find(script)
	if len(function) == 0 {
		t.Fatal("shipped ssid_prefix_known function not found")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			withSSIDPrefixEnv(t, "", tc.Line)
			if got := hasTollGateSSID(tc.Prefix + "-ABCD"); got != tc.Accept {
				t.Errorf("Go acceptance = %v, want %v for %q", got, tc.Accept, tc.Line)
			}
			cmd := exec.Command("sh", "-c", string(function)+"\nssid_prefix_known \"$1\"", "prefix-test", tc.Prefix)
			out, err := cmd.CombinedOutput()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("shell harness failed: %v: %s", err, out)
				}
			}
			if got := err == nil; got != tc.Accept {
				t.Errorf("shell acceptance = %v, want %v for %q (output %q)", got, tc.Accept, tc.Line, out)
			}
		})
	}
}
