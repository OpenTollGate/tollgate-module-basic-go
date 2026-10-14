package bearerport

import "testing"

// TestDerivationIsPureAndSpentnessLivesAtTheAuthority is the executable form of
// the two load-bearing facts behind the NUT-07 derivation:
//
//  1. The nullifier is a PURE FUNCTION of (seed, index, authority). Two
//     independent implementations of one authority therefore derive the same
//     nullifier without coordinating, which is what lets a mint and a wallet
//     agree on a double-spend tag while the seed stays local.
//
//  2. It is scoped by authority. A mint and a federation must NOT share a
//     nullifier space: otherwise accepting a note at one would make the port
//     refuse an unrelated note at the other, and a mint could correlate the
//     same holder's activity across institutions.
//
//  3. Spentness is not derived at all. It is a fact the authority learns only
//     at redemption, and it is recorded in the authority's memory.
func TestDerivationIsPureAndSpentnessLivesAtTheAuthority(t *testing.T) {
	seed := []byte("seed-for-derivation")

	// Two independent instances of the SAME mint must agree, bit for bit.
	a, b := NewCashuAuthority("https://mint.example"), NewCashuAuthority("https://mint.example")
	for i := uint64(0); i < 3; i++ {
		if x, y := a.Nullifier(seed, i), b.Nullifier(seed, i); x != y {
			t.Errorf("index %d: same mint disagreed: %q vs %q", i, x, y)
		}
	}

	// Two DIFFERENT authorities must not: the tag is scoped, so no cross-mint
	// correlation is possible and no accidental collision can occur.
	fedi := NewFedimintAuthority("fed1invitefedimintexample")
	if a.Nullifier(seed, 0) == fedi.Nullifier(seed, 0) {
		t.Errorf("a mint and a federation derived the same nullifier")
	}

	// Distinct counters must never collide, or a range could be exposed twice
	// without the authority noticing.
	if a.Nullifier(seed, 0) == a.Nullifier(seed, 1) {
		t.Errorf("nullifier collision across counters")
	}

	// Spentness is authority memory, learned at redemption, and monotone.
	blob := a.Issue(seed, 0, 10)
	commit := CommitmentOf(seed, 0, a.Name())
	null := a.Nullifier(seed, 0)

	rec, ok, err := a.Issued(commit)
	if err != nil || !ok {
		t.Fatalf("freshly signed output: ok=%v err=%v", ok, err)
	}
	if rec.Spent {
		t.Fatalf("freshly signed output reported spent")
	}
	states, err := a.CheckStates([]string{null})
	if err != nil {
		t.Fatalf("checkstates: %v", err)
	}
	if states[null] != StateUnspent {
		t.Fatalf("freshly signed = %v, want StateUnspent", states[null])
	}

	// Consume it through the real path: swap it for a replacement output.
	port := NewPort()
	port.Register(a)
	in, err := port.Decode(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := a.Swap([]Instrument{in}, []Output{{Commitment: "replacement", Amount: 10}}); err != nil {
		t.Fatalf("swap: %v", err)
	}

	states, err = a.CheckStates([]string{null})
	if err != nil {
		t.Fatalf("checkstates after spend: %v", err)
	}
	if states[null] != StateSpent {
		t.Errorf("after redemption = %v, want StateSpent", states[null])
	}
	if _, err := a.Swap([]Instrument{in}, []Output{{Commitment: "replacement2", Amount: 10}}); err == nil {
		t.Errorf("double spend succeeded")
	}
}
