package lightning

import (
	"encoding/json"
	"testing"
)

func TestCapabilityProbeStructure(t *testing.T) {
	// Test that all capability constants are defined
	expectedCaps := []Capability{
		CapabilityLNURLPay,
		CapabilityAmountRange,
		CapabilityMetadata,
		CapabilitySuccessAction,
		CapabilityRouteHints,
		CapabilityFastResponse,
		CapabilitySecureHTTPS,
	}

	for _, cap := range expectedCaps {
		if string(cap) == "" {
			t.Errorf("Capability constant %v is empty", cap)
		}
	}

	// Test capability levels
	expectedLevels := []CapabilityLevel{
		LevelUnsupported,
		LevelBasic,
		LevelFull,
	}

	for _, level := range expectedLevels {
		if level < 0 || level > 2 {
			t.Errorf("Invalid capability level: %d", level)
		}
	}
}

func TestDefaultProbeOptions(t *testing.T) {
	options := DefaultProbeOptions()

	if options.Timeout != 10000 {
		t.Errorf("Expected timeout 10000, got %d", options.Timeout)
	}

	if options.TestAmount != 1000 {
		t.Errorf("Expected test amount 1000, got %d", options.TestAmount)
	}

	if !options.EnableDetailed {
		t.Error("Expected EnableDetailed to be true")
	}

	if options.AllowHTTP {
		t.Error("Expected AllowHTTP to be false")
	}
}

func TestCapabilityResultJSON(t *testing.T) {
	result := CapabilityResult{
		Capability:  CapabilityLNURLPay,
		Level:       LevelFull,
		Supported:   true,
		Details:     "Test details",
		ResponseTime: 150,
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal CapabilityResult: %v", err)
	}

	var decodedResult CapabilityResult
	err = json.Unmarshal(jsonData, &decodedResult)
	if err != nil {
		t.Fatalf("Failed to unmarshal CapabilityResult: %v", err)
	}

	if decodedResult.Capability != CapabilityLNURLPay {
		t.Errorf("Expected capability %s, got %s", CapabilityLNURLPay, decodedResult.Capability)
	}

	if !decodedResult.Supported {
		t.Error("Expected supported to be true")
	}
}

func TestCalculateOverallScore(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []CapabilityResult
		expectedMin  float64
		expectedMax  float64
	}{
		{
			name: "all unsupported",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: false},
				{Capability: CapabilityMetadata, Supported: false},
			},
			expectedMin: 0.0,
			expectedMax: 0.0,
		},
		{
			name: "all full support",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: true, Level: LevelFull},
				{Capability: CapabilityMetadata, Supported: true, Level: LevelFull},
			},
			expectedMin: 1.0,
			expectedMax: 1.0,
		},
		{
			name: "mixed support",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: true, Level: LevelFull},
				{Capability: CapabilityMetadata, Supported: true, Level: LevelBasic},
				{Capability: CapabilityAmountRange, Supported: false},
			},
			expectedMin: 0.5,  // (1.0 + 0.7 + 0) / 3 = 0.566...
			expectedMax: 0.6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateOverallScore(tt.capabilities)
			if score < tt.expectedMin || score > tt.expectedMax {
				t.Errorf("Score %f is outside expected range [%f, %f]", score, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}

func TestShouldUseGracefulFallback(t *testing.T) {
	tests := []struct {
		name          string
		capabilities []CapabilityResult
		expected      bool
	}{
		{
			name: "all critical supported",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: true},
				{Capability: CapabilityAmountRange, Supported: true},
				{Capability: CapabilitySecureHTTPS, Supported: true},
			},
			expected: false,
		},
		{
			name: "missing critical capabilities",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: true},
				{Capability: CapabilitySecureHTTPS, Supported: false},
			},
			expected: true,
		},
		{
			name: "only one critical supported",
			capabilities: []CapabilityResult{
				{Capability: CapabilityLNURLPay, Supported: true},
				{Capability: CapabilityAmountRange, Supported: false},
				{Capability: CapabilitySecureHTTPS, Supported: false},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldUseGracefulFallback(tt.capabilities)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}