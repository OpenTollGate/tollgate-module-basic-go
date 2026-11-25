package fips_exit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in integration probe. Never mutates networking; config is synthetic.
func TestLocalReadOnlyProbes(t *testing.T) {
	dir := os.Getenv("FIPS_PROBE_REPORT_DIR")
	if dir == "" {
		t.Skip("opt-in real local read-only probes")
	}
	commands := [][]string{{"-j", "link", "show"}, {"-j", "address", "show"}, {"-j", "-4", "rule", "show"}, {"-j", "-4", "route", "show", "table", "all"}}
	schemas := []string{linkSchema, linkSchema + " addr_info:a", "priority:n src:s table:t protocol:t", routeSchema}
	names := []string{"link", "address", "rule", "route"}
	for i, args := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		b, err := RunIPProbe(ctx, Probe{"ip", args, 1 << 20})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, names[i]+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("ip %v: %d bytes", args, len(b))
		var rows []observation
		if err = json.Unmarshal(b, &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if !validObservation(row, schemas[i]) {
				t.Errorf("%s incompatible row: %s", names[i], mustJSON(row))
			}
		}
	}
	c := validConfig()
	e, err := CollectLocalLinkRoutes(context.Background(), c, 3*time.Second)
	t.Logf("SYNTHETIC TEST CONFIG guest=%s mesh=%s tunnel=%s; evidence=%+v error=%v; refusal is NOT a working exit", c.GuestInterface, c.FIPSInterface, c.TunnelInterface, e, err)
	if err == nil {
		t.Fatal("unexpected acceptance of synthetic config")
	}
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
