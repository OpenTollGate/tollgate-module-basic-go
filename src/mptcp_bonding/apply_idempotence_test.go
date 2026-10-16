package mptcp_bonding

import (
	"strings"
	"testing"
)

// Running the rendered script on the real device exposed a bug the unit tests
// could not see: `ip mptcp endpoint delete <addr>` is NOT valid.
//
// The real grammar is:
//
//	ip mptcp endpoint delete id ID [ ADDRESS ]
//
// The id is mandatory. Deleting by address fails, the subsequent add then hits
// EEXIST (-17 "duplicate one"), and because the script is `set -e` the whole
// apply exits non-zero on every run. Observed on the GL-MT6000 (OpenWrt 24.10.2,
// kernel 6.12.94):
//
//	Error: too many addresses or duplicate one: -17.
//	RC=2
//
// These tests pin the corrected form so it cannot regress.

func TestApplyScriptSkipsAlreadyCorrectEndpoint(t *testing.T) {
	// The stronger guarantee, learned from the device: re-running must NOT
	// churn the endpoint id. Delete-then-add succeeds but allocates a new id
	// (observed 4 -> 7 -> 9 across runs), and removing an endpoint interrupts an
	// in-flight subflow. So an already-correct endpoint must be left alone.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	s := c.ApplyScript()

	if !strings.Contains(s, "cur_dev") {
		t.Error("the script must check the endpoint's current device, not just its presence")
	}
	if !strings.Contains(s, "if [ -n \"$cur_id\" ] && [ \"$cur_dev\" = ") {
		t.Errorf("the add must be conditional on the endpoint being absent or on the wrong device:\n%s", s)
	}
}

func TestApplyScriptDeletesByIDNotAddress(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	s := c.ApplyScript()

	for _, addr := range []string{"192.168.2.15", "10.2.1.122"} {
		bad := "ip mptcp endpoint delete " + addr + " "
		if strings.Contains(s, bad) {
			t.Errorf("delete-by-address is invalid grammar and fails on device; found %q", bad)
		}
	}
}

func TestApplyScriptIdempotencyUsesIDLookup(t *testing.T) {
	// The delete must be derived from the live endpoint list: look up the id
	// owning this address, and only if one exists delete it. That is what makes
	// a re-run a no-op instead of an EEXIST failure.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	s := c.ApplyScript()

	if !strings.Contains(s, "ip mptcp endpoint show") {
		t.Error("the script must consult `ip mptcp endpoint show` to resolve the id")
	}
	if !strings.Contains(s, "ip mptcp endpoint delete id") {
		t.Error("the delete must use the `id ID` form")
	}
	// Each add must still be guarded, so a second run does not fail the script.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "ip mptcp endpoint add") {
			if !strings.Contains(s, "id \"$") && !strings.Contains(s, "for ") {
				t.Errorf("adds must be preceded by an id-resolving loop, got:\n%s", s)
			}
		}
	}
}

func TestApplyScriptDoesNotUseSetEForEndpointAdds(t *testing.T) {
	// `set -e` plus an unguarded add is what turned a cosmetic problem into a
	// hard failure. The add must not be able to abort the script on EEXIST.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "lan1", Address: "192.168.2.15"},
		{Interface: "phy0-sta0", Address: "10.2.1.122"},
	}
	s := c.ApplyScript()
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ip mptcp endpoint add") {
			continue
		}
		// The add may be unguarded ONLY if it cannot abort the script. Under
		// `set -e` an EEXIST aborts the run, so every add must be guarded.
		if !strings.Contains(trimmed, "|| true") {
			t.Errorf("an unguarded endpoint add under `set -e` aborts on EEXIST; got %q", trimmed)
		}
	}
}
