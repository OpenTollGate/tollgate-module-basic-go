package main

import (
	"strings"
	"testing"
)

// The regression behind issue #1006: config apply's per-setting report is a
// slice of records, and the text renderer used to dump it with %v — leaking
// raw Go map syntax (`map[detail:...]`) into operator-facing output.
func TestDisplayMapRendersRecordSlicesWithoutGoMapSyntax(t *testing.T) {
	data := map[string]interface{}{
		"applied": []interface{}{
			map[string]interface{}{
				"setting": "admin_access",
				"status":  "unchanged",
				"detail":  "admin_access=both adds no rule; no fragment present",
			},
			map[string]interface{}{
				"setting": "private_encryption",
				"status":  "skipped",
				"detail":  "no wireless.private_radio0 section on this host",
			},
		},
	}

	out, err := captureStdout(t, func() error {
		displayData(data)
		return nil
	})
	if err != nil {
		t.Fatalf("displayData: %v", err)
	}

	if strings.Contains(out, "map[") || strings.Contains(out, "[]interface") {
		t.Errorf("text output leaks Go internals: %q", out)
	}
	for _, want := range []string{
		"applied:",
		"setting: admin_access",
		"status: unchanged",
		"setting: private_encryption",
		"status: skipped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	// Record lines must be indented under the key, not inline with it.
	if strings.Contains(out, "applied: [") {
		t.Errorf("record slice rendered inline: %q", out)
	}
}

func TestDisplayMapRendersScalarSlicesAsListItems(t *testing.T) {
	data := map[string]interface{}{
		"mints": []interface{}{"https://mint-a.example", "https://mint-b.example"},
	}

	out, err := captureStdout(t, func() error {
		displayMap(data, "")
		return nil
	})
	if err != nil {
		t.Fatalf("displayMap: %v", err)
	}

	for _, want := range []string{
		"mints:",
		"- https://mint-a.example",
		"- https://mint-b.example",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "map[") {
		t.Errorf("scalar slice output leaks Go internals: %q", out)
	}
}
