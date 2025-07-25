package chandler

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
)

// Mock implementations for testing

type mockMerchant struct{}

func (m *mockMerchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "mock_payment_token", nil
}

func (m *mockMerchant) GetAcceptedMints() []config_manager.MintConfig {
	return []config_manager.MintConfig{
		{
			URL:       "https://mock.mint.com",
			PriceUnit: "sat",
		},
	}
}

func (m *mockMerchant) GetBalance() uint64 {
	return 10000 // Mock balance
}

func (m *mockMerchant) GetBalanceByMint(mintURL string) uint64 {
	return 10000 // Mock balance
}

func TestChandlerInterface(t *testing.T) {
	// Basic test to ensure the interface methods are defined correctly
	// This test verifies compilation of the interface methods

	// Test that we can create interface methods without errors
	var _ ChandlerInterface = (*Chandler)(nil)

	t.Log("ChandlerInterface methods compiled successfully")
}

func TestMockMerchant(t *testing.T) {
	merchant := &mockMerchant{}

	// Test CreatePaymentToken
	token, err := merchant.CreatePaymentToken("https://test.mint.com", 100)
	if err != nil {
		t.Errorf("CreatePaymentToken failed: %v", err)
	}
	if token != "mock_payment_token" {
		t.Errorf("Expected 'mock_payment_token', got '%s'", token)
	}

	// Test GetAcceptedMints
	mints := merchant.GetAcceptedMints()
	if len(mints) != 1 {
		t.Errorf("Expected 1 mint, got %d", len(mints))
	}

	// Test GetBalance
	balance := merchant.GetBalance()
	if balance != 10000 {
		t.Errorf("Expected balance 10000, got %d", balance)
	}

	// Test GetBalanceByMint
	balanceByMint := merchant.GetBalanceByMint("https://test.mint.com")
	if balanceByMint != 10000 {
		t.Errorf("Expected balance by mint 10000, got %d", balanceByMint)
	}
}

func TestUsageTrackerInterface(t *testing.T) {
	// Test that usage tracker interfaces are properly defined
	var _ UsageTrackerInterface = (*TimeUsageTracker)(nil)
	var _ UsageTrackerInterface = (*DataUsageTracker)(nil)

	t.Log("UsageTrackerInterface methods compiled successfully")
}

func TestPricingOptions(t *testing.T) {
	// Test ValidateBudgetConstraints function exists and works with valid inputs
	proposal := &PaymentProposal{
		UpstreamPubkey: "test-pubkey",
		Steps:          10,
		PricingOption: &tollgate_protocol.PricingOption{
			AssetType:    "cashu",
			PricePerStep: 1,
			PriceUnit:    "sat",
			MintURL:      "https://test.mint.com",
			MinSteps:     1,
		},
		Reason:             "test",
		EstimatedAllotment: 600000, // 10 minutes in milliseconds
	}

	err := ValidateBudgetConstraints(proposal, 0.01, 0.001, "milliseconds", 60000) // 60000 ms per step
	if err != nil {
		t.Errorf("ValidateBudgetConstraints failed with valid inputs: %v", err)
	}
}

func TestTrustPolicy(t *testing.T) {
	// Test ValidateTrustPolicy function
	err := ValidateTrustPolicy("test-pubkey", []string{}, []string{}, "trust_all")
	if err != nil {
		t.Errorf("ValidateTrustPolicy failed with trust_all policy: %v", err)
	}

	err = ValidateTrustPolicy("test-pubkey", []string{}, []string{}, "trust_none")
	if err == nil {
		t.Error("Expected ValidateTrustPolicy to fail with trust_none policy")
	}

	err = ValidateTrustPolicy("blocked-pubkey", []string{}, []string{"blocked-pubkey"}, "trust_all")
	if err == nil {
		t.Error("Expected ValidateTrustPolicy to fail with blocked pubkey")
	}
}
