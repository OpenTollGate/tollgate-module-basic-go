package wan_health

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestToastChannelSwitchClassification(t *testing.T) {
	for _, tc := range []struct {
		name            string
		channel         int
		dfs             bool
		csa, loss       int
		reason, message string
	}{
		{"non_dfs_switch", 36, false, 1, 0, "", "Upstream Wi-Fi switched channels 1 time(s) in the last hour. Ethernet would be more reliable."},
		{"non_dfs_switch_and_loss", 36, false, 2, 3, "", "Upstream Wi-Fi switched channels 2 time(s) in the last hour. Ethernet would be more reliable."},
		{"dfs_switch", 100, true, 2, 0, "", "Upstream Wi-Fi is on a DFS channel and switched channels 2 time(s) in the last hour. Ethernet would be more reliable."},
		{"dfs_normalised", 100, false, 0, 0, "", "Upstream Wi-Fi is on a DFS channel and switched channels 0 time(s) in the last hour. Ethernet would be more reliable."},
		{"reason_preserved", 36, false, 1, 0, "upstream changed channel", "Upstream Wi-Fi switched channels 1 time(s) in the last hour. Ethernet would be more reliable. (upstream changed channel)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wan-health.json")
			body := fmt.Sprintf(`{"channel":%d,"dfs":%t,"csa_count_1h":%d,"carrier_loss_1h":%d,"reason":%q}`, tc.channel, tc.dfs, tc.csa, tc.loss, tc.reason)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			got := s.Toast()
			if got.Message != tc.message {
				t.Fatalf("toast classification: got %q, want %q", got.Message, tc.message)
			}
			if !s.RecommendEthernet || !got.Active || got.Level != "warning" || got.Title != "Upstream Wi-Fi instability" || got.SnoozeSeconds != 3600 {
				t.Fatalf("warning metadata changed: status=%+v toast=%+v", s, got)
			}
		})
	}
}
