package bragging

import (
	"context"
	"github.com/nbd-wtf/go-nostr"
)

type Service struct {
	config     Config
	publicKey  string
	privateKey string
	relayPool  *nostr.SimplePool
}

type Config struct {
	Enabled   bool
	Relays    []string
	Fields    []string
	Template  string
	UserOptIn bool
}

func NewService(config Config, privateKey string) (*Service, error) {
	pubKey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		return nil, err
	}
	return &Service{
		config:     config,
		publicKey:  pubKey,
		privateKey: privateKey,
		relayPool:  nostr.NewSimplePool(context.Background()),
	}, nil
}
