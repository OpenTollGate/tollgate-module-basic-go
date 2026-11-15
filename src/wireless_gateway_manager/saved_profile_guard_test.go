package wireless_gateway_manager

import (
	"strings"
	"testing"
)

func TestFindOrCreateSavedProfileGuards(t *testing.T) {
	for _, tc := range []struct{ name, stored, requested, key, disabled string }{
		{"active", "psk2", "psk2", "synthetic-entered-key", "0"},
		{"open-downgrade", "psk2", "none", "", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUCI()
			seedSTA(f, "saved", "Saved", "radio0", tc.stored, "synthetic-stored-key", tc.disabled)
			f.set("wireless.saved.network", "wwan")
			c := &Connector{runUCI: f.exec}
			if _, e := c.FindOrCreateSTAForSSID("Saved", tc.key, tc.requested, "radio0"); e == nil {
				t.Error("unsafe existing-profile write was accepted")
			}
			for _, call := range f.callLog() {
				if !strings.HasPrefix(call, "get ") && !strings.HasPrefix(call, "show ") {
					t.Error("refused profile operation mutated UCI")
					break
				}
			}
			key, _ := f.get("wireless.saved.key")
			if key != "synthetic-stored-key" {
				t.Error("stored key changed")
			}
		})
	}
}
