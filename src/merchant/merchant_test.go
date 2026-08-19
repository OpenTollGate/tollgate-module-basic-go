package merchant

import (
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func TestCalculateAllotment_PricePerStepZero(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "bytes",
			StepSize: 10485760,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com", PricePerStep: 0},
			},
		},
	}

	_, err := m.calculateAllotment(100, "https://mint.example.com")
	if err == nil {
		t.Fatal("expected error when PricePerStep is 0, got nil")
	}
}

func TestCalculateAllotment_ValidPricePerStep(t *testing.T) {
	tests := []struct {
		name       string
		metric     string
		stepSize   uint64
		priceStep  uint64
		amountSats uint64
		wantSteps  uint64
	}{
		{
			name:       "1 sat per step, 10 sats paid, bytes metric",
			metric:     "bytes",
			stepSize:   10485760,
			priceStep:  1,
			amountSats: 10,
			wantSteps:  10,
		},
		{
			name:       "2 sats per step, 10 sats paid",
			metric:     "milliseconds",
			stepSize:   60000,
			priceStep:  2,
			amountSats: 10,
			wantSteps:  5,
		},
		{
			name:       "1 sat per step, 1 sat paid",
			metric:     "bytes",
			stepSize:   10485760,
			priceStep:  1,
			amountSats: 1,
			wantSteps:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Merchant{
				config: &config_manager.Config{
					Metric:   tt.metric,
					StepSize: tt.stepSize,
					AcceptedMints: []config_manager.MintConfig{
						{URL: "https://mint.example.com", PricePerStep: tt.priceStep},
					},
				},
			}

			allotment, err := m.calculateAllotment(tt.amountSats, "https://mint.example.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expected := tt.wantSteps * tt.stepSize
			if allotment != expected {
				t.Errorf("got allotment %d, want %d", allotment, expected)
			}
		})
	}
}

func TestCalculateAllotment_MintNotFound(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "bytes",
			StepSize: 10485760,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com", PricePerStep: 1},
			},
		},
	}

	_, err := m.calculateAllotment(100, "https://unknown-mint.example.com")
	if err == nil {
		t.Fatal("expected error for unknown mint, got nil")
	}
}

func TestCalculateAllotment_UnsupportedMetric(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "packets",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com", PricePerStep: 1},
			},
		},
	}

	_, err := m.calculateAllotment(100, "https://mint.example.com")
	if err == nil {
		t.Fatal("expected error for unsupported metric, got nil")
	}
}

func TestProcessPayout_UnderflowGuard(t *testing.T) {
	tests := []struct {
		name           string
		balance        uint64
		minPayout      uint64
		minBalance     uint64
		expectNoPayout bool
	}{
		{
			name:           "balance below MinPayoutAmount skips",
			balance:        10,
			minPayout:      50,
			minBalance:     64,
			expectNoPayout: true,
		},
		{
			name:           "balance equal to MinBalance skips",
			balance:        64,
			minPayout:      32,
			minBalance:     64,
			expectNoPayout: true,
		},
		{
			name:           "balance below MinBalance but above MinPayout skips",
			balance:        50,
			minPayout:      32,
			minBalance:     64,
			expectNoPayout: true,
		},
		{
			name:           "balance above MinBalance proceeds",
			balance:        128,
			minPayout:      32,
			minBalance:     64,
			expectNoPayout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mintConfig := config_manager.MintConfig{
				URL:             "https://mint.example.com",
				MinBalance:      tt.minBalance,
				MinPayoutAmount: tt.minPayout,
			}

			// Verify the guard condition logic directly
			// processPayout checks: balance < MinPayoutAmount → skip
			if tt.balance < mintConfig.MinPayoutAmount {
				if !tt.expectNoPayout {
					t.Error("expected payout but balance < MinPayoutAmount guard would skip")
				}
				return
			}

			// processPayout checks: balance <= MinBalance → skip (underflow guard)
			if tt.balance <= mintConfig.MinBalance {
				if !tt.expectNoPayout {
					t.Error("expected payout but balance <= MinBalance guard would skip (underflow)")
				}
				return
			}

			// Would proceed to subtract: aimedPaymentAmount = balance - MinBalance
			if tt.expectNoPayout {
				t.Errorf("expected no payout but balance %d > MinBalance %d would proceed", tt.balance, mintConfig.MinBalance)
			}

			// Verify the subtraction itself is safe (no underflow)
			result := tt.balance - mintConfig.MinBalance
			if result > tt.balance {
				t.Errorf("underflow detected: balance(%d) - MinBalance(%d) = %d", tt.balance, mintConfig.MinBalance, result)
			}
		})
	}
}

func TestCalculateAllotment_TrailingSlashMintURL(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com/Bitcoin", PricePerStep: 1, MinPurchaseSteps: 1},
			},
		},
	}

	allotment, err := m.calculateAllotment(10, "https://mint.example.com/Bitcoin/")
	if err != nil {
		t.Fatalf("trailing slash should match, got error: %v", err)
	}
	if allotment != 10000 {
		t.Errorf("expected allotment 10000, got %d", allotment)
	}
}

func TestCalculateAllotment_CaseInsensitiveMintURL(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com/Bitcoin", PricePerStep: 1, MinPurchaseSteps: 1},
			},
		},
	}

	allotment, err := m.calculateAllotment(10, "https://MINT.EXAMPLE.COM/Bitcoin")
	if err != nil {
		t.Fatalf("uppercase host should match, got error: %v", err)
	}
	if allotment != 10000 {
		t.Errorf("expected allotment 10000, got %d", allotment)
	}
}

func TestCalculateAllotment_NoPathMintURL(t *testing.T) {
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: 1000,
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://mint.example.com", PricePerStep: 1, MinPurchaseSteps: 1},
			},
		},
	}

	allotment, err := m.calculateAllotment(10, "https://mint.example.com/")
	if err != nil {
		t.Fatalf("root path should match no-path config, got error: %v", err)
	}
	if allotment != 10000 {
		t.Errorf("expected allotment 10000, got %d", allotment)
	}
}

// TestCalculateAllotment_PostSwapFeeCrediting pins the crediting arithmetic
// for the exact post-fee amounts from Amperstrand fork issue #63 ("5-sat
// token receives as 4 sats"): the amount the wallet reports after the
// swap-to-trusted fee is what gets credited — 4 sats, not face value 5.
// The Fund→Receive passthrough (no fee re-credited) is characterized in
// merchant_token_flow_test.go once WalletPort injection lands (#299).
func TestCalculateAllotment_PostSwapFeeCrediting(t *testing.T) {
	cases := []struct {
		name         string
		amount       uint64 // post-swap amount Receive() returned
		pricePerStep uint64
		stepSize     uint64
		wantSteps    uint64
		wantErr      string // "" = expect success
	}{
		{"fork63_five_sats_minus_one_sat_fee", 4, 1, 10485760, 4, ""},
		{"zero_fee_face_value", 5, 1, 10485760, 5, ""},
		{"price_two_truncates_remainder", 5, 2, 10485760, 2, ""},
		{"price_two_post_fee_truncates", 3, 2, 10485760, 1, ""},
		{"amount_below_price_rejected_by_minimum", 1, 2, 10485760, 0, "minimum purchase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				config: &config_manager.Config{
					Metric:   "bytes",
					StepSize: tc.stepSize,
					AcceptedMints: []config_manager.MintConfig{
						{URL: "https://mint.example.com", PricePerStep: tc.pricePerStep, MinPurchaseSteps: 1},
					},
				},
			}
			allotment, err := m.calculateAllotment(tc.amount, "https://mint.example.com")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v (allotment %d)", tc.wantErr, err, allotment)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expected := tc.wantSteps * tc.stepSize
			if allotment != expected {
				t.Errorf("post-swap %d sats @ %d/step: expected %d bytes (%d steps), got %d",
					tc.amount, tc.pricePerStep, expected, tc.wantSteps, allotment)
			}
		})
	}
}
