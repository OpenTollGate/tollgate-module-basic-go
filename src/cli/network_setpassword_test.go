package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// fakeUCI installs shim `uci` and `wifi` binaries on PATH. uci keeps a flat
// "key=value" state file and appends every invocation to a log file, so a test
// can assert exactly which options a handler wrote (and that it committed and
// reloaded).
func fakeUCI(t *testing.T) (state, log string) {
	t.Helper()
	dir := t.TempDir()
	state = filepath.Join(dir, "uci.state")
	log = filepath.Join(dir, "uci.log")
	if err := os.WriteFile(state, nil, 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	uci := `#!/bin/sh
state="` + state + `"
log="` + log + `"
echo "uci $*" >> "$log"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
  get)
    v=$(grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2- | tail -n1)
    if [ -z "$v" ]; then [ "$q" = 1 ] || echo "uci: Entry not found" >&2; exit 1; fi
    printf '%s\n' "$v" ;;
  set)
    key="${1%%=*}"
    grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null; mv "$state.tmp" "$state"
    printf '%s\n' "$1" >> "$state" ;;
  *) : ;;
esac
exit 0
`
	wifi := `#!/bin/sh
echo "wifi $*" >> "` + log + `"
exit 0
`
	for name, body := range map[string]string{"uci": uci, "wifi": wifi} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state, log
}

func seedState(t *testing.T, state string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(state, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

func uciLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(b)
}

// set-password must write the passphrase AND enable the radio: 99-tollgate-setup
// leaves a credential-less private AP disabled, so a key written into a radio
// that stays down is a silent "the password did nothing".
func TestPrivateNetworkSetPasswordEnablesTheRadio(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "wireless.private_radio0=wifi-iface")

	s := &CLIServer{}
	resp := s.handlePrivateNetworkSetPassword("NewPass-123")
	if !resp.Success {
		t.Fatalf("set-password failed: %s", resp.Error)
	}

	got := uciLog(t, log)
	for _, want := range []string{
		"set wireless.private_radio0.key=NewPass-123",
		"set wireless.private_radio0.disabled=0",
		"commit wireless",
		"wifi reload",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("uci log missing %q\nlog:\n%s", want, got)
		}
	}
}

// Both radios exist (dual-band): both the key and the enable must land on each.
func TestPrivateNetworkSetPasswordEnablesEveryRadio(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "wireless.private_radio0=wifi-iface", "wireless.private_radio1=wifi-iface")

	s := &CLIServer{}
	if resp := s.handlePrivateNetworkSetPassword("NewPass-123"); !resp.Success {
		t.Fatalf("set-password failed: %s", resp.Error)
	}

	got := uciLog(t, log)
	for _, want := range []string{
		"set wireless.private_radio0.key=NewPass-123",
		"set wireless.private_radio1.key=NewPass-123",
		"set wireless.private_radio0.disabled=0",
		"set wireless.private_radio1.disabled=0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("uci log missing %q\nlog:\n%s", want, got)
		}
	}
}

// The config applier (config.json -> UCI, the board settings path) must enable
// the radio it writes a passphrase onto, for the same reason.
func TestApplyPrivateNetworkCredentialsEnablesTheRadio(t *testing.T) {
	state, log := fakeUCI(t)
	seedState(t, state, "wireless.private_radio0=wifi-iface")

	_ = applyPrivateNetworkCredentials(&config_manager.Config{PrivateKey: "FromConfig-123"})

	got := uciLog(t, log)
	for _, want := range []string{
		"set wireless.private_radio0.key=FromConfig-123",
		"set wireless.private_radio0.disabled=0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("uci log missing %q\nlog:\n%s", want, got)
		}
	}
}
