package cli

import (
	"strings"
	"testing"
)

// The private-network family must refuse a radio-less system with ONE shape.
//
// THE DEFECT THIS PINS (#1002 C2): rename/set-encryption produced the friendly
// "no private network" refusal while status/enable/set-password leaked raw UCI
// errors first ("failed to get UCI value: exit status 1") — three commands
// answering the same condition in three dialects. All five now speak
// errNoPrivateNetwork, verbatim identical.
func TestPrivateNetworkFamily_RefusalShapeIsUniformOnRadiolessSystem(t *testing.T) {
	state, _ := fakeUCI(t) // empty state: no wireless.* entries at all
	seedState(t, state)    // zero lines — radio-less

	s := &CLIServer{}
	want := "this router has no private network (no wireless.private_radio0 section); run the TollGate setup first"

	cases := map[string]struct {
		resp CLIResponse
		cmd  string
	}{
		"status":         {s.handlePrivateNetworkStatus(), "status"},
		"enable":         {s.handlePrivateNetworkEnable(), "enable"},
		"set-password":   {s.handlePrivateNetworkSetPassword("NewPass-123"), "set-password"},
		"rename":         {s.handlePrivateNetworkRename("TestSSID"), "rename"},
		"set-encryption": {s.handlePrivateNetworkSetEncryption("psk2+ccmp"), "set-encryption"},
	}
	for name, tc := range cases {
		if tc.resp.Success {
			t.Errorf("%s: expected refusal on radio-less system, got success", name)
			continue
		}
		if !strings.Contains(tc.resp.Error, want) {
			t.Errorf("%s: refusal missing the canonical message;\n got: %s\nwant substring: %s", name, tc.resp.Error, want)
		}
	}
}

// The refusal must fire BEFORE any uci write: a radio-less system must not
// gain a wireless.private_radio0 section as a side effect of being told it
// has none (the uci-set-creates-sections trap setPrivateRadioOption guards).
func TestPrivateNetworkFamily_RadiolessRefusalWritesNothing(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state)

	s := &CLIServer{}
	_ = s.handlePrivateNetworkEnable()
	_ = s.handlePrivateNetworkSetPassword("NewPass-123")

	for _, line := range strings.Split(uciLog(t, log), "\n") {
		if strings.Contains(line, "uci set wireless") {
			t.Fatalf("radio-less refusal path issued a uci set: %s", line)
		}
	}
}
