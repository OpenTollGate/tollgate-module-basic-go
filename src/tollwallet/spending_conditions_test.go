package tollwallet

import (
	"errors"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

func TestErrLockedToken_IsSentinel(t *testing.T) {
	if !errors.Is(ErrLockedToken, ErrLockedToken) {
		t.Fatal("ErrLockedToken should be detectable via errors.Is")
	}
}

func TestErrLockedToken_Message(t *testing.T) {
	if ErrLockedToken.Error() == "" {
		t.Fatal("ErrLockedToken should have non-empty message")
	}
}

func TestHasSpendingCondition_PlainSecret(t *testing.T) {
	proofs := cashu.Proofs{
		{Amount: 1, Secret: "just-a-plain-secret", C: "02abc"},
	}
	if hasLockedProofs(proofs) {
		t.Fatal("plain secret should not be detected as locked")
	}
}

func TestHasSpendingCondition_P2PKSecret(t *testing.T) {
	proofs := cashu.Proofs{
		{Amount: 1, Secret: `["P2PK",{"nonce":"abc123","data":"","tags":[["pubkeys","02abcdef"]]}]`, C: "02abc"},
	}
	if !hasLockedProofs(proofs) {
		t.Fatal("P2PK secret should be detected as locked")
	}
}

func TestHasSpendingCondition_HTLCSecret(t *testing.T) {
	proofs := cashu.Proofs{
		{Amount: 1, Secret: `["HTLC",{"nonce":"abc123","data":"","tags":[["hash_lock","abcdef"]]}]`, C: "02abc"},
	}
	if !hasLockedProofs(proofs) {
		t.Fatal("HTLC secret should be detected as locked")
	}
}

func TestHasSpendingCondition_MixedProofs(t *testing.T) {
	proofs := cashu.Proofs{
		{Amount: 1, Secret: "plain", C: "02abc"},
		{Amount: 2, Secret: `["P2PK",{"nonce":"abc","data":"","tags":[]}]`, C: "02def"},
	}
	if !hasLockedProofs(proofs) {
		t.Fatal("mixed proofs with one P2PK should be detected as locked")
	}
}
