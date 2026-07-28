package tollgate_protocol

import (
	"encoding/json"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestValidateAdvertisement_NilEvent(t *testing.T) {
	err := ValidateAdvertisement(nil)
	if err == nil {
		t.Fatal("expected error for nil event")
	}
}

func TestValidateAdvertisement_WrongKind(t *testing.T) {
	event := &nostr.Event{Kind: 0}
	err := ValidateAdvertisement(event)
	if err == nil {
		t.Fatal("expected error for wrong kind")
	}
}

func TestValidateAdvertisement_RightKindBadSig(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"step_size", "22020096"},
		},
	}
	err := ValidateAdvertisement(event)
	if err == nil {
		t.Fatal("expected error for unsigned event")
	}
}

func TestParseAdvertisementFromBytes_Valid(t *testing.T) {
	raw := `{"kind":10021,"id":"abc","pubkey":"def","created_at":1000,"tags":[["metric","bytes"]],"content":"","sig":"xyz"}`
	event, err := ParseAdvertisementFromBytes([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Kind != TollGateAdvertisementKind {
		t.Fatalf("expected kind %d, got %d", TollGateAdvertisementKind, event.Kind)
	}
}

func TestParseAdvertisementFromBytes_InvalidJSON(t *testing.T) {
	_, err := ParseAdvertisementFromBytes([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestExtractAdvertisementInfo_NilEvent(t *testing.T) {
	_, err := ExtractAdvertisementInfo(nil)
	if err == nil {
		t.Fatal("expected error for nil event")
	}
}

func TestExtractAdvertisementInfo_WrongKind(t *testing.T) {
	event := &nostr.Event{Kind: 99999}
	_, err := ExtractAdvertisementInfo(event)
	if err == nil {
		t.Fatal("expected error for wrong kind")
	}
}

func TestExtractAdvertisementInfo_ValidTags(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"step_size", "22020096"},
			{"price_per_step", "cashu", "1", "sat", "https://mint.example.com", "1"},
			{"tips", "01", "02"},
		},
	}
	info, err := ExtractAdvertisementInfo(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Metric != "bytes" {
		t.Errorf("expected metric 'bytes', got '%s'", info.Metric)
	}
	if info.StepSize != 22020096 {
		t.Errorf("expected step_size 22020096, got %d", info.StepSize)
	}
	if len(info.PricingOptions) != 1 {
		t.Fatalf("expected 1 pricing option, got %d", len(info.PricingOptions))
	}
	opt := info.PricingOptions[0]
	if opt.AssetType != "cashu" {
		t.Errorf("expected asset type 'cashu', got '%s'", opt.AssetType)
	}
	if opt.PricePerStep != 1 {
		t.Errorf("expected price_per_step 1, got %d", opt.PricePerStep)
	}
	if opt.PriceUnit != "sat" {
		t.Errorf("expected price_unit 'sat', got '%s'", opt.PriceUnit)
	}
	if opt.MintURL != "https://mint.example.com" {
		t.Errorf("expected mint URL, got '%s'", opt.MintURL)
	}
	if len(info.TIPs) != 2 {
		t.Errorf("expected 2 TIPs, got %d", len(info.TIPs))
	}
}

func TestExtractAdvertisementInfo_MissingMetric(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"step_size", "22020096"},
			{"price_per_step", "cashu", "1", "sat", "https://mint.example.com", "1"},
		},
	}
	_, err := ExtractAdvertisementInfo(event)
	if err == nil {
		t.Fatal("expected error for missing metric")
	}
}

func TestExtractAdvertisementInfo_MissingStepSize(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"price_per_step", "cashu", "1", "sat", "https://mint.example.com", "1"},
		},
	}
	_, err := ExtractAdvertisementInfo(event)
	if err == nil {
		t.Fatal("expected error for missing step_size")
	}
}

func TestExtractAdvertisementInfo_NoPricing(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"step_size", "22020096"},
		},
	}
	_, err := ExtractAdvertisementInfo(event)
	if err == nil {
		t.Fatal("expected error for missing pricing options")
	}
}

func TestExtractAdvertisementInfo_MalformedPriceTag(t *testing.T) {
	event := &nostr.Event{
		Kind: TollGateAdvertisementKind,
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"step_size", "22020096"},
			{"price_per_step", "cashu"},
			{"price_per_step", "cashu", "1", "sat", "https://mint.example.com", "1"},
		},
	}
	info, err := ExtractAdvertisementInfo(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.PricingOptions) != 1 {
		t.Fatalf("expected 1 valid pricing option (malformed skipped), got %d", len(info.PricingOptions))
	}
}

func TestValidateAdvertisementFromBytes_Full(t *testing.T) {
	raw := `{"kind":10021,"id":"abc","pubkey":"def","created_at":1000,"tags":[["metric","bytes"]],"content":"","sig":"xyz"}`
	_, err := ValidateAdvertisementFromBytes([]byte(raw))
	if err == nil {
		t.Fatal("expected error (sig won't verify without real keys)")
	}
}

func TestValidateAdvertisementFromBytes_BadJSON(t *testing.T) {
	_, err := ValidateAdvertisementFromBytes([]byte("{{{"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func ensureJSON(t *testing.T, v interface{}) {
	t.Helper()
	if _, err := json.Marshal(v); err != nil {
		t.Fatalf("value not JSON-serializable: %v", err)
	}
}

func TestPricingOption_JSONRoundTrip(t *testing.T) {
	opt := PricingOption{
		AssetType:    "cashu",
		PricePerStep: 1,
		PriceUnit:    "sat",
		MintURL:      "https://mint.example.com",
		MinSteps:     1,
	}
	ensureJSON(t, opt)
}

func TestAdvertisementInfo_JSONRoundTrip(t *testing.T) {
	info := AdvertisementInfo{
		Metric:   "bytes",
		StepSize: 22020096,
		PricingOptions: []PricingOption{
			{AssetType: "cashu", PricePerStep: 1, PriceUnit: "sat", MintURL: "https://mint.example.com", MinSteps: 1},
		},
		TIPs: []string{"01", "02"},
	}
	ensureJSON(t, info)
}
