package config_manager

import (
	"encoding/json"
	"testing"
)

func TestMintConfigMinStepsDefault(t *testing.T) {
	tests := []struct {
		name string
		json string
		want uint64
	}{
		{"absent key defaults to 1", `{"url":"https://x","price_per_step":1}`, 1},
		{"explicit purchase_min_steps=3", `{"url":"https://x","purchase_min_steps":3}`, 3},
		{"legacy min_purchase_steps=2", `{"url":"https://x","min_purchase_steps":2}`, 2},
		{"legacy wins when primary absent", `{"url":"https://x","min_purchase_steps":5}`, 5},
		{"purchase_min_steps=0 defaults to 1", `{"url":"https://x","purchase_min_steps":0}`, 1},
		{"both keys, primary wins", `{"url":"https://x","purchase_min_steps":7,"min_purchase_steps":2}`, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mc MintConfig
			if err := json.Unmarshal([]byte(tt.json), &mc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if mc.MinPurchaseSteps != tt.want {
				t.Errorf("MinPurchaseSteps = %d, want %d", mc.MinPurchaseSteps, tt.want)
			}
		})
	}
}

func TestMintConfigMarshalRoundtrip(t *testing.T) {
	original := MintConfig{
		URL:              "https://mint.example",
		PricePerStep:     1,
		PriceUnit:        "sat",
		MinPurchaseSteps: 2,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded MintConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loaded.MinPurchaseSteps != original.MinPurchaseSteps {
		t.Errorf("roundtrip: MinPurchaseSteps = %d, want %d", loaded.MinPurchaseSteps, original.MinPurchaseSteps)
	}
}

func TestAdvertisementMinStepsPositive(t *testing.T) {
	// Every default config entry must produce min_steps >= 1 after
	// unmarshal (the client-side contract: cashud rejects min_steps=0)
	for i, mint := range defaultProductionMints() {
		data, err := json.Marshal(mint)
		if err != nil {
			t.Fatalf("marshal default[%d]: %v", i, err)
		}
		var loaded MintConfig
		if err := json.Unmarshal(data, &loaded); err != nil {
			t.Fatalf("unmarshal default[%d]: %v", i, err)
		}
		if loaded.MinPurchaseSteps < 1 {
			t.Errorf("default[%d] (%s): MinPurchaseSteps = %d, want >= 1", i, loaded.URL, loaded.MinPurchaseSteps)
		}
	}
}
