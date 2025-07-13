package main

import (
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/nbd-wtf/go-nostr"
)

func main() {
	privateKeyHex := nostr.GeneratePrivateKey()
	fmt.Println("Hex Private Key:", privateKeyHex)

	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		panic(err)
	}

	fiveBitGroups, err := bech32.ConvertBits(privateKeyBytes, 8, 5, true)
	if err != nil {
		panic(err)
	}

	nsec, err := bech32.Encode("nsec", fiveBitGroups)
	if err != nil {
		panic(err)
	}
	fmt.Println("Bech32 Encoded Private Key (nsec):", nsec)
}
