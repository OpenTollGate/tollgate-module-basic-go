package merchant

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func TestMintURLMatchesTrailingSlash(t *testing.T) {
	if !tollwallet.MintURLMatches("https://mint.example.com/Bitcoin", "https://mint.example.com/Bitcoin/") {
		t.Error("trailing slash difference should match")
	}
	if !tollwallet.MintURLMatches("https://mint.example.com/Bitcoin/", "https://mint.example.com/Bitcoin") {
		t.Error("trailing slash difference should match (reversed)")
	}
}

func TestMintURLMatchesCaseInsensitiveHost(t *testing.T) {
	if !tollwallet.MintURLMatches("https://Mint.Example.COM/Bitcoin", "https://mint.example.com/Bitcoin") {
		t.Error("host case difference should match")
	}
}

func TestMintURLMatchesNoPath(t *testing.T) {
	if !tollwallet.MintURLMatches("https://mint.example.com", "https://mint.example.com/") {
		t.Error("missing path vs root path should match")
	}
}

func TestMintURLMatchesDifferentSchemesDiffer(t *testing.T) {
	if tollwallet.MintURLMatches("http://mint.example.com/Bitcoin", "https://mint.example.com/Bitcoin") {
		t.Error("different schemes should not match")
	}
}

func TestMintURLMatchesDifferentHostsDiffer(t *testing.T) {
	if tollwallet.MintURLMatches("https://mint.example.com/Bitcoin", "https://mint.other.com/Bitcoin") {
		t.Error("different hosts should not match")
	}
}

func TestCalculateAllotmentFuzzyMintURLMatch(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com/Bitcoin", PricePerStep: 1, MinPurchaseSteps: 1},
			},
		},
	}

	// Token mint URL has trailing slash — should still match
	allotment, err := m.calculateAllotment(5, "https://mint.example.com/Bitcoin/")
	if err != nil {
		t.Fatalf("expected fuzzy match to succeed, got error: %v", err)
	}
	expected := uint64(5 * 1000) // 5 steps * 1000ms step size
	if allotment != expected {
		t.Errorf("expected allotment %d, got %d", expected, allotment)
	}
}

func TestCalculateAllotmentFuzzyMintURLCaseInsensitive(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com/Bitcoin", PricePerStep: 2, MinPurchaseSteps: 1},
			},
		},
	}

	// Token mint URL has uppercase host — should still match
	allotment, err := m.calculateAllotment(10, "https://MINT.EXAMPLE.COM/Bitcoin")
	if err != nil {
		t.Fatalf("expected case-insensitive match to succeed, got error: %v", err)
	}
	expected := uint64(5 * 1000) // 10 sats / 2 pricePerStep = 5 steps * 1000ms
	if allotment != expected {
		t.Errorf("expected allotment %d, got %d", expected, allotment)
	}
}

func TestCalculateAllotmentRejectsUnknownMint(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com/Bitcoin", PricePerStep: 1, MinPurchaseSteps: 1},
			},
		},
	}

	_, err := m.calculateAllotment(5, "https://mint.unknown.com/Bitcoin")
	if err == nil {
		t.Fatal("expected error for unknown mint URL")
	}
}
