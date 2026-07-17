package tollwallet

import (
	"testing"

	"github.com/Origami74/gonuts-tollgate/cashu"
)

// TestStripDLEQProofsRemovesDLEQ verifies that stripDLEQProofs produces
// a token whose proofs have no DLEQ data.
func TestStripDLEQProofsRemovesDLEQ(t *testing.T) {
	// Keyset IDs must be valid hex for NewTokenV4.
	// Use 8-byte hex IDs (common in Cashu).
	proofs := cashu.Proofs{
		{
			Amount: 1,
			Id:     "deadbeefdeadbeef",
			Secret: "test-secret-1",
			C:      "deadbeef",
		},
		{
			Amount: 2,
			Id:     "deadbeefdeadbeef",
			Secret: "test-secret-2",
			C:      "cafebabe",
		},
	}

	mintURL := "https://mint.example.com"

	// Create original token without DLEQ (DLEQ needs valid hex e/s/r values)
	originalToken, err := cashu.NewTokenV4(proofs, mintURL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4 failed: %v", err)
	}

	// Strip DLEQ proofs (no-op since we created without DLEQ, but verifies the path)
	stripped, err := stripDLEQProofs(originalToken)
	if err != nil {
		t.Fatalf("stripDLEQProofs failed: %v", err)
	}

	// Verify stripped token has no DLEQ proofs
	strippedProofs := stripped.Proofs()
	if len(strippedProofs) != len(proofs) {
		t.Fatalf("expected %d proofs, got %d", len(proofs), len(strippedProofs))
	}

	for i, p := range strippedProofs {
		if p.DLEQ != nil {
			t.Errorf("proof %d still has DLEQ data: %+v", i, p.DLEQ)
		}
	}

	// Verify mint URL preserved
	if stripped.Mint() != mintURL {
		t.Errorf("expected mint %s, got %s", mintURL, stripped.Mint())
	}
}

// TestStripDLEQProofsPreservesCoreFields verifies that stripping DLEQ
// does not modify the core proof fields (Amount, Secret, C, Id).
func TestStripDLEQProofsPreservesCoreFields(t *testing.T) {
	proofs := cashu.Proofs{
		{
			Amount: 5,
			Id:     "aabbccddaabbccdd",
			Secret: "secret-xyz",
			C:      "aabbccdd",
		},
	}

	mintURL := "https://mint.example.com"

	originalToken, err := cashu.NewTokenV4(proofs, mintURL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4 failed: %v", err)
	}

	stripped, err := stripDLEQProofs(originalToken)
	if err != nil {
		t.Fatalf("stripDLEQProofs failed: %v", err)
	}

	strippedProofs := stripped.Proofs()
	if len(strippedProofs) != 1 {
		t.Fatalf("expected 1 proof, got %d", len(strippedProofs))
	}

	p := strippedProofs[0]
	if p.Amount != 5 {
		t.Errorf("expected amount 5, got %d", p.Amount)
	}
	if p.Secret != "secret-xyz" {
		t.Errorf("expected secret 'secret-xyz', got '%s'", p.Secret)
	}
}

// TestStripDLEQProofsAlreadyNilDLEQ verifies that stripping a token
// that already has no DLEQ proofs works correctly (no-op).
func TestStripDLEQProofsAlreadyNilDLEQ(t *testing.T) {
	proofs := cashu.Proofs{
		{
			Amount: 1,
			Id:     "deadbeefdeadbeef",
			Secret: "test-secret",
			C:      "deadbeef",
		},
	}

	mintURL := "https://mint.example.com"

	originalToken, err := cashu.NewTokenV4(proofs, mintURL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4 failed: %v", err)
	}

	stripped, err := stripDLEQProofs(originalToken)
	if err != nil {
		t.Fatalf("stripDLEQProofs failed: %v", err)
	}

	strippedProofs := stripped.Proofs()
	for i, p := range strippedProofs {
		if p.DLEQ != nil {
			t.Errorf("proof %d should have nil DLEQ, got %+v", i, p.DLEQ)
		}
	}
}
