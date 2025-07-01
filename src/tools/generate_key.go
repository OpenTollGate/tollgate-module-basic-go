package main

import (
	"fmt"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func main() {
	pk := nostr.GeneratePrivateKey()
	nsec, err := nip19.EncodePrivateKey(pk)
	if err != nil {
		fmt.Println("Error encoding private key:", err)
		return
	}
	fmt.Println(nsec)
}