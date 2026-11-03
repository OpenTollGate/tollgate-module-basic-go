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
		// The guarantee is that the address is de-registered BEFORE the add, so
		// re-running cannot fail on a duplicate registration. The delete must go
		// through the owning id: `endpoint delete <addr>` is invalid grammar.
		// So look for an id-resolving delete of this address in the preceding
		// lines, and require that a failed add is tracked, not swallowed
		// (#974: `|| true` here produced a silently unbonded router — the
		// failure must not abort the OTHER uplinks under `set -e`, but the
		// script as a whole must exit non-zero when any add fails).
		fields := strings.Fields(line)
		if len(fields) < 5 {
			t.Errorf("malformed endpoint add: %q", line)
			continue
		}
		addr := fields[4]
		if !strings.HasPrefix(strings.TrimSpace(line), "if ! ip mptcp endpoint add") {
			t.Errorf("endpoint add for %s must run under a per-uplink `if !` guard: %q", addr, line)
		}
		if !strings.Contains(line, "FAILED=1") || !strings.Contains(line, "add failed") {
			t.Errorf("endpoint add for %s must record the failure loudly (echo + FAILED=1): %q", addr, line)
		}
		guarded := false
		for j := i - 1; j >= 0 && j >= i-6; j-- {
			if strings.Contains(lines[j], "ip mptcp endpoint delete id") &&
				strings.Contains(lines[j-2], addr) {
				guarded = true
				break
			}
		}
		if !guarded {
			t.Errorf("endpoint add for %s must be preceded by an id-resolving delete of the same address, got:\n%s",
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

// #974: disable must be a full rollback — FlushScript removes every kernel
// endpoint, and is a silent no-op on kernels without MPTCP so a stock node
// never logs a warning on boot.
func TestFlushScriptRendersGuardedFlush(t *testing.T) {
	s := FlushScript()
	if !strings.Contains(s, "ip mptcp endpoint flush") {
		t.Errorf("flush script must contain the flush command:\n%s", s)
	}
	if !strings.Contains(s, "exit 0") {
		t.Errorf("flush script must exit 0 when MPTCP is unavailable (nothing to flush):\n%s", s)
	}
	if strings.Contains(s, "exit 1\nfi") && !strings.Contains(s, "endpoint flush") {
		t.Errorf("flush script must not demand MPTCP the way apply does:\n%s", s)
	}
	if FlushScript() != s {
		t.Error("flush script must render byte-identical on every call (idempotent reconcile)")
	}
}

// The loud-failure invariant: a failed endpoint add must not abort the other
// uplinks (device-learned: EEXIST under `set -e` left the bond half-applied)
// but must fail the whole script so the reconcile surfaces it (#974).
func TestApplyScriptFailsScriptWhenAnyEndpointAddFails(t *testing.T) {
	c := validConfig()
	c.Uplinks = []Uplink{
		{Interface: "eth1", Address: "192.0.2.10"},
		{Interface: "phy1-sta0", Address: "198.51.100.7"},
	}
	s := c.ApplyScript()
	if !strings.Contains(s, "FAILED=0") {
		t.Error("apply script must initialise the failure tracker")
	}
	if !strings.Contains(s, `[ "$FAILED" = 1 ]`) || !strings.Contains(s, "exit 1") {
		t.Error("apply script must exit non-zero when any endpoint add failed")
	}
	if strings.Contains(s, "endpoint add failed") == false {
		t.Error("a failed add must say which endpoint failed")
	}
}
