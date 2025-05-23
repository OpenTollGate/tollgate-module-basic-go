package main

import (
	"context"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// func TestDecodeCashuToken(t *testing.T) {
// 	token := "invalid_token"
// 	_, err := decodeCashuToken(token)
// 	if err == nil {
// 		t.Errorf("decodeCashuToken should fail for invalid token")
// 	}
// }

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
