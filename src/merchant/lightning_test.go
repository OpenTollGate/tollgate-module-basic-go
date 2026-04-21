package merchant

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func TestGetSessionRemovesExpiredMillisecondsSession(t *testing.T) {
	macAddress := "aa:bb:cc:dd:ee:ff"
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			macAddress: {
				MacAddress: macAddress,
				StartTime:  time.Now().Add(-3 * time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  1000,
			},
		},
	}

	session, err := m.GetSession(macAddress)
	if err == nil {
		t.Fatal("expected expired session lookup to fail")
	}
	if session != nil {
		t.Fatal("expected no session to be returned for expired session")
	}
	if _, exists := m.customerSessions[macAddress]; exists {
		t.Fatal("expected expired session to be removed from memory")
	}
}

func TestLightningQuotePersistencePrunesExpiredRecords(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	now := time.Now().Unix()

	m := &Merchant{
		configManager: &config_manager.ConfigManager{ConfigFilePath: configPath},
		lightningQuotes: map[string]*lightningQuoteRecord{
			"active": {
				MacAddress:     "aa:bb:cc:dd:ee:ff",
				MintURL:        "https://mint.example",
				Amount:         21,
				Allotment:      21000,
				SessionGranted: true,
				UpdatedAt:      now,
			},
			"stale": {
				MacAddress: "11:22:33:44:55:66",
				MintURL:    "https://mint.example",
				Amount:     7,
				UpdatedAt:  time.Now().Add(-2 * lightningQuoteRetention).Unix(),
			},
		},
	}

	if err := m.saveLightningQuotes(); err != nil {
		t.Fatalf("saveLightningQuotes returned error: %v", err)
	}

	loaded := &Merchant{
		configManager:   &config_manager.ConfigManager{ConfigFilePath: configPath},
		lightningQuotes: make(map[string]*lightningQuoteRecord),
	}
	if err := loaded.loadLightningQuotes(); err != nil {
		t.Fatalf("loadLightningQuotes returned error: %v", err)
	}

	if len(loaded.lightningQuotes) != 1 {
		t.Fatalf("expected 1 persisted quote after pruning, got %d", len(loaded.lightningQuotes))
	}

	record, ok := loaded.lightningQuotes["active"]
	if !ok {
		t.Fatal("expected active quote to be loaded")
	}
	if record.Allotment != 21000 || !record.SessionGranted {
		t.Fatalf("unexpected active quote payload: %+v", record)
	}
	if _, ok := loaded.lightningQuotes["stale"]; ok {
		t.Fatal("expected stale quote to be pruned")
	}
}
