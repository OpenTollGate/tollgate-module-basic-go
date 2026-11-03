package wireless_gateway_manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUCIBinary installs a `uci` shim on PATH that mimics OpenWrt uci for the
// one case under test: `uci show <config>` with no /etc/config/<config> prints
// "uci: Entry not found" to stderr and exits 1.
func fakeUCIBinary(t *testing.T, argsBody string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + argsBody + "\n"
	p := filepath.Join(dir, "uci")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write uci shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// THE DEFECT THIS PINS (#1002 C1): on a radio-less system `uci show wireless`
// fails with a bare "exit status 1", and GetSTASections surfaced it raw —
// `tollgate upstream list` answered "Failed to list upstreams: exit status 1"
// while `upstream scan` degraded cleanly ("no radios found"). A missing
// /etc/config/wireless is "zero sections", not an error.
func TestGetSTASections_MissingWirelessConfigIsEmptyNotError(t *testing.T) {
	fakeUCIBinary(t, `
case "$1" in
  show)
    if [ "$2" = "wireless" ]; then echo "uci: Entry not found" >&2; exit 1; fi
    ;;
esac
exit 0
`)
	c := &Connector{}
	sections, err := c.GetSTASections()
	if err != nil {
		t.Fatalf("radio-less system must parse as zero sections, got error: %v", err)
	}
	if len(sections) != 0 {
		t.Fatalf("expected 0 sections, got %d", len(sections))
	}
}

// A REAL uci failure on show must still surface: only the exact "Entry not
// found" wording means "config absent" — anything else is corruption the
// operator must see, not silently zero sections.
func TestGetSTASections_OtherShowFailureStillErrors(t *testing.T) {
	fakeUCIBinary(t, `echo "uci: Parse error" >&2; exit 2`)
	c := &Connector{}
	if _, err := c.GetSTASections(); err == nil {
		t.Fatal("non-Entry-not-found uci failure must surface as an error")
	} else if !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected the underlying exec error, got: %v", err)
	}
}
