package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWANHealthFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wan-health.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOLLGATE_WAN_HEALTH_PATH", path)
	return path
}

func TestHandleWANHealthReturnsSnapshotAndToast(t *testing.T) {
	writeWANHealthFixture(t, `{
		"ts": "2026-10-30T12:45:23Z",
		"ssid": "StarGate",
		"channel": 100,
		"dfs": true,
		"csa_count_1h": 2,
		"carrier_loss_1h": 2,
		"signal": -72,
		"tx_bitrate": 6,
		"recommend_ethernet": true
	}`)

	rec := httptest.NewRecorder()
	handleWANHealth(rec, httptest.NewRequest(http.MethodGet, "/wan-health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["success"] != true {
		t.Fatalf("success = %v", body["success"])
	}
	toast, ok := body["toast"].(map[string]any)
	if !ok {
		t.Fatalf("toast missing: %s", rec.Body.String())
	}
	message, _ := toast["message"].(string)
	if !strings.Contains(message, "switched channels 2 time(s)") {
		t.Fatalf("unexpected toast message: %q", message)
	}
	if !strings.Contains(strings.ToLower(message), "ethernet") {
		t.Fatalf("toast must mention Ethernet: %q", message)
	}
}

func TestHandleWANHealthMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("TOLLGATE_WAN_HEALTH_PATH", filepath.Join(t.TempDir(), "missing.json"))
	rec := httptest.NewRecorder()
	handleWANHealth(rec, httptest.NewRequest(http.MethodGet, "/wan-health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["data"] != nil {
		t.Fatalf("data = %v, want nil", body["data"])
	}
	if body["toast"] != nil {
		t.Fatalf("toast = %v, want nil", body["toast"])
	}
}

func TestHandleWANHealthInvalidJSON(t *testing.T) {
	writeWANHealthFixture(t, "{")
	rec := httptest.NewRecorder()
	handleWANHealth(rec, httptest.NewRequest(http.MethodGet, "/wan-health", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandleWANHealthRejectsPost(t *testing.T) {
	writeWANHealthFixture(t, `{}`)
	rec := httptest.NewRecorder()
	handleWANHealth(rec, httptest.NewRequest(http.MethodPost, "/wan-health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
