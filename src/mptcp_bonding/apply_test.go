package mptcp_bonding

import (
	"strings"
	"testing"
)

// The applier turns a Config into an ordered, idempotent shell script. Two
// properties matter and are both easy to get wrong:
//
//  1. ORDER. `ip mptcp limits set` must precede endpoint registration, and
//     both must precede any attempt to use the bond. A script that runs the
//     steps in the wrong order leaves endpoints registered against a ceiling
//     that then rejects them.
//  2. IDEMPOTENCE. The script is run on every reconcile. Re-adding an already
//     registered endpoint must not fail or accumulate duplicates, or the
//     device drifts a little further from desired state on every run.
//
// The kitchen-sink failure this guards against: a script that works perfectly
// the first time and breaks the second, which is how a field device ends up
// unconfigured after the first reboot.

func TestApplyScriptOrdersLimitsBeforeEndpoints(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
	}

	script := c.ApplyScript()

	iLimits := strings.Index(script, "ip mptcp limits set")
	iEndpoint := strings.Index(script, "ip mptcp endpoint add")
	if iLimits < 0 {
		t.Fatalf("script must set limits, got:\n%s", script)
	}
	if iEndpoint < 0 {
		t.Fatalf("script must register endpoints, got:\n%s", script)
	}
	if iLimits > iEndpoint {
		t.Errorf("limits must be set BEFORE endpoints (limits@%d, endpoint@%d):\n%s",
			iLimits, iEndpoint, script)
	}
}

func TestApplyScriptIsIdempotentOnEndpoints(t *testing.T) {
	// Re-adding an existing endpoint is an error on the device, so the script
	// must guard each add. A bare `ip mptcp endpoint add` that fails on the
	// second run would abort a set -e script and leave the bond half-applied.
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
	}

	script := c.ApplyScript()
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "ip mptcp endpoint add") {
			continue
		}
		// The guarantee is that a delete of the same address appears BEFORE
		// the add, so re-running cannot fail on a duplicate registration.
		// Extract the address and look for its delete in the preceding lines.
		fields := strings.Fields(line)
		if len(fields) < 5 {
			t.Errorf("malformed endpoint add: %q", line)
			continue
		}
		addr := fields[4]
		guarded := false
		for j := i - 1; j >= 0 && j >= i-3; j-- {
			if strings.Contains(lines[j], "endpoint delete") && strings.Contains(lines[j], addr) {
				guarded = true
				break
			}
		}
		if !guarded {
			t.Errorf("endpoint add for %s must be preceded by a delete of the same address, got:\n%s",
				addr, script)
		}
	}
}

func TestApplyScriptIsValidShellAndDisablesOnEmpty(t *testing.T) {
	// A disabled bond must render nothing at all -- not even a header. A script
	// that emits a stray line could still be executed and surprise an operator.
	if s := (&Config{}).ApplyScript(); s != "" {
		t.Fatalf("a disabled bond must render no script, got: %q", s)
	}

	c := validConfig()
	script := c.ApplyScript()
	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Errorf("script must start with a shebang, got: %q", firstLine(script))
	}
	// `set -e` matters: a silently-swallowed failure is how a bond half-applies.
	if !strings.Contains(script, "set -e") {
		t.Error("script must fail fast (set -e)")
	}
	// The server credential must never be rendered into a world-readable file.
	if strings.Contains(script, c.Password) {
		t.Error("the password must never be written into the script")
	}
}

func TestApplyScriptOmitsEndpointsWithoutAddress(t *testing.T) {
	// Addressless uplinks cannot be registered yet; the script must skip them
	// rather than emit an add with an empty address (a device-side error).
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0"},
	}
	script := c.ApplyScript()
	if strings.Contains(script, "dev phy1-sta0 signal") {
		t.Errorf("addressless uplink must not be registered, got:\n%s", script)
	}
	if !strings.Contains(script, "192.0.2.10") {
		t.Errorf("the addressable uplink must still be registered, got:\n%s", script)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
