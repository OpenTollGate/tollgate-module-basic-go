package wan_health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDFSChannel(t *testing.T) {
	cases := map[int]bool{
		1: false, 6: false, 11: false, 36: false, 48: false, 149: false, 165: false,
		52: true, 64: true, 100: true, 144: true,
	}
	for channel, want := range cases {
		if got := IsDFSChannel(channel); got != want {
			t.Fatalf("IsDFSChannel(%d) = %v, want %v", channel, got, want)
		}
	}
}

func TestLoadNormalisesDFSAndRecommendation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wan-health.json")
	body := `{
		"ts": "2026-10-30T12:45:23Z",
		"ssid": "StarGate",
		"channel": 100,
		"dfs": false,
		"csa_count_1h": 2,
		"carrier_loss_1h": 2,
		"signal": -72,
		"tx_bitrate": 6
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !status.DFS {
		t.Fatal("channel 100 must be normalised to DFS=true")
	}
	if !status.RecommendEthernet {
		t.Fatal("DFS/CSA must recommend Ethernet")
	}
	toast := status.Toast()
	if !toast.Active || toast.Level != "warning" || toast.SnoozeSeconds != 3600 {
		t.Fatalf("unexpected toast: %+v", toast)
	}
	if !strings.Contains(toast.Message, "switched channels 2 time(s)") {
		t.Fatalf("unexpected message: %q", toast.Message)
	}
	if !strings.Contains(strings.ToLower(toast.Message), "ethernet") {
		t.Fatalf("message must mention Ethernet: %q", toast.Message)
	}
}

func TestCarrierLossOnlyToast(t *testing.T) {
	status := Status{Channel: 6, CarrierLoss1h: 3}
	toast := status.Toast()
	if !toast.Active {
		t.Fatal("carrier loss must activate the toast")
	}
	if !strings.Contains(toast.Message, "dropped 3 time(s)") {
		t.Fatalf("unexpected message: %q", toast.Message)
	}
}

func TestHealthyLinkHasNoToast(t *testing.T) {
	status := Status{Channel: 36, DFS: false}
	if toast := status.Toast(); toast.Active {
		t.Fatalf("healthy link must not toast: %+v", toast)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected IsNotExist, got %v", err)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a parse error")
	}
}
