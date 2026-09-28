package tollwallet

import "testing"

// A mint admitted at runtime must join the accepted set that gates
// Receive, and admission must be idempotent (#481).
func TestAcceptMintGrowsAcceptedSetIdempotently(t *testing.T) {
	w := &TollWallet{acceptedMints: []string{"http://mint-a.test"}}

	if err := w.AcceptMint("http://mint-b.test"); err != nil {
		t.Fatalf("AcceptMint: %v", err)
	}
	if !contains(w.acceptedMints, "http://mint-b.test") {
		t.Fatalf("late mint not in accepted set: %v", w.acceptedMints)
	}

	before := len(w.acceptedMints)
	if err := w.AcceptMint("http://mint-b.test"); err != nil {
		t.Fatalf("AcceptMint (repeat): %v", err)
	}
	if len(w.acceptedMints) != before {
		t.Fatalf("AcceptMint not idempotent: %v", w.acceptedMints)
	}
}

// Admitting a mint already in the set (alternate spelling of the same
// URL aside) must be a no-op, not a duplicate entry.
func TestAcceptMintKeepsExistingMintSingle(t *testing.T) {
	w := &TollWallet{acceptedMints: []string{"http://mint-a.test"}}

	if err := w.AcceptMint("http://mint-a.test"); err != nil {
		t.Fatalf("AcceptMint: %v", err)
	}
	if len(w.acceptedMints) != 1 {
		t.Fatalf("duplicate mint entry: %v", w.acceptedMints)
	}
}
