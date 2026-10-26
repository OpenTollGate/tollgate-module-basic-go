package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parsePrivateAddressSpec: the accepted shapes and the refusals. The /24-only
// rule exists because the DHCP pool and the setup script's collision logic
// assume a /24; a half-supported /16 would break both silently.
func TestParsePrivateAddressSpec(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isErr bool
	}{
		{"10.9.8.1/24", "10.9.8.1", false},
		{"10.9.8.1", "10.9.8.1", false},
		{"  192.168.77.1/24 ", "192.168.77.1", false},
		{"0.0.0.1", "0.0.0.1", false},
		{"255.255.255.254", "255.255.255.254", false},

		{"10.9.8.1/16", "", true},  // only /24 is supported
		{"10.9.8.1/240", "", true}, // ditto, not an octal trick
		{"10.9.8.1/24/24", "", true},
		{"10.9.8.300", "", true}, // octet out of range
		{"300.1.1.1", "", true},
		{"10.9.8", "", true},     // too few octets
		{"10.9.8.1.5", "", true}, // too many octets
		{"10.9..1", "", true},    // empty octet
		{"10.9.08.1", "", true},  // leading zero
		{"10.9.8.01", "", true},  // leading zero (host)
		{"10.9.8.0", "", true},   // network address
		{"10.9.8.255", "", true}, // broadcast address
		{"", "", true},
		{"not-an-ip", "", true},
		{"10.9.8.a", "", true},
	}
	for _, c := range cases {
		got, err := parsePrivateAddressSpec(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("parse(%q) = %q, expected an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parse(%q) failed: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// set-address writes BOTH halves of the pin (#906): the live interface config
// and the durable store 99-tollgate-setup preserves, then commits both and
// reloads the network.
func TestPrivateNetworkSetAddressWritesInterfaceAndStore(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "network.private=interface")

	reloadShim := filepath.Join(t.TempDir(), "network-reload")
	if err := os.WriteFile(reloadShim, []byte("#!/bin/sh\necho \"network-reload $*\" >> \""+log+"\"\n"), 0o755); err != nil {
		t.Fatalf("write reload shim: %v", err)
	}
	saved := networkReloadCommand
	networkReloadCommand = []string{reloadShim}
	defer func() { networkReloadCommand = saved }()

	s := &CLIServer{}
	resp := s.handlePrivateNetworkSetAddress("10.9.8.1/24")
	if !resp.Success {
		t.Fatalf("set-address failed: %s", resp.Error)
	}

	got := uciLog(t, log)
	for _, want := range []string{
		"set network.private.ipaddr=10.9.8.1",
		"set network.private.netmask=255.255.255.0",
		"set tollgate.private.ipaddr=10.9.8.1",
		"commit network",
		"commit tollgate",
		"network-reload",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q\nlog:\n%s", want, got)
		}
	}
	if !strings.Contains(resp.Message, "10.9.8.1/24") {
		t.Errorf("success message should name the new subnet: %q", resp.Message)
	}
}

// The store's named section is created on first pin, exactly the way
// 99-tollgate-setup creates tollgate.device.
func TestPrivateNetworkSetAddressCreatesStoreSection(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "network.private=interface")

	saved := networkReloadCommand
	networkReloadCommand = []string{"/nonexistent/init-script"} // dev host: warning path
	defer func() { networkReloadCommand = saved }()

	s := &CLIServer{}
	if resp := s.handlePrivateNetworkSetAddress("192.168.77.1"); !resp.Success {
		t.Fatalf("set-address failed: %s", resp.Error)
	}

	got := uciLog(t, log)
	if !strings.Contains(got, "set tollgate.private=private") {
		t.Errorf("expected the store section to be created\nlog:\n%s", got)
	}
}

// A router without a private network is refused, not half-configured: `uci
// set` on a missing section CREATES it, and a bare interface with no
// proto/device is not a private network.
func TestPrivateNetworkSetAddressRefusesWithoutPrivateNetwork(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state) // nothing

	s := &CLIServer{}
	resp := s.handlePrivateNetworkSetAddress("10.9.8.1/24")
	if resp.Success {
		t.Fatal("set-address must refuse when network.private is absent")
	}
	if !strings.Contains(resp.Error, "no private network") {
		t.Errorf("refusal should explain the precondition: %q", resp.Error)
	}
	if got := uciLog(t, log); strings.Contains(got, "tollgate.private.ipaddr") {
		t.Errorf("nothing must be written on refusal\nlog:\n%s", got)
	}
}

// An invalid spec writes nothing at all — a missing uci log is the strongest
// form of that (no uci invocation happened).
func TestPrivateNetworkSetAddressValidatesBeforeWriting(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "network.private=interface")

	s := &CLIServer{}
	for _, spec := range []string{"10.9.8.1/16", "10.9.8.0", "banana"} {
		resp := s.handlePrivateNetworkSetAddress(spec)
		if resp.Success {
			t.Errorf("spec %q must be refused", spec)
		}
	}
	if b, err := os.ReadFile(log); err == nil && strings.Contains(string(b), "set network.private.ipaddr") {
		t.Errorf("no uci writes expected for refused specs\nlog:\n%s", string(b))
	}
}

// The dispatch reaches the handler: `network private set-address <spec>`.
func TestPrivateNetworkDispatchSetAddress(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "network.private=interface")

	saved := networkReloadCommand
	networkReloadCommand = []string{"/nonexistent/init-script"}
	defer func() { networkReloadCommand = saved }()

	s := &CLIServer{}
	resp := s.handlePrivateNetworkCommand([]string{"set-address", "10.20.30.1/24"}, nil)
	if !resp.Success {
		t.Fatalf("dispatched set-address failed: %s", resp.Error)
	}
	if got := uciLog(t, log); !strings.Contains(got, "set tollgate.private.ipaddr=10.20.30.1") {
		t.Errorf("dispatched command did not write the store\nlog:\n%s", got)
	}
}
