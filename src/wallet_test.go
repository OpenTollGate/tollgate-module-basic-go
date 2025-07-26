package main

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func TestParseToken(t *testing.T) {
	token := "invalid_token"
	_, err := tollwallet.ParseToken(token)
	if err == nil {
		t.Errorf("ParseToken should fail for invalid token")
	}
}

/*
// TestCollectPayment is temporarily commented out as it requires a more complete implementation
// involving Nostr and a TollWallet instance.
func TestCollectPayment(t *testing.T) {
	token := "invalid_token"
	privateKey := "test_private_key"
	relayPool := nostr.NewSimplePool(context.Background())

	relays := []string{"wss://relay.damus.io"}
	acceptedMint := "https://mint.minibits.cash/Bitcoin"
	err := CollectPayment(token, privateKey, relayPool, relays, acceptedMint)
	if err == nil {
		t.Errorf("CollectPayment should fail for invalid token and private key")
	}
}
*/
