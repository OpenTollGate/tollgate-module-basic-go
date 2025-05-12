package bragging

import (
	"context"
	"log"
	"sync"

	"github.com/nbd-wtf/go-nostr"
)

var relayRequestSemaphore = make(chan struct{}, 5) // Allow up to 5 concurrent requests

func rateLimitedRelayRequest(relay *nostr.Relay, event nostr.Event) error {
    relayRequestSemaphore <- struct{}{}
    defer func() { <-relayRequestSemaphore }()
    
    return relay.Publish(context.Background(), event)
}

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

func (s *Service) Config() Config {
	return s.config
}

func (s *Service) publishEvent(event nostr.Event) error {
    var wg sync.WaitGroup
    for _, relayURL := range s.config.Relays {
        wg.Add(1)
        go func(relayURL string) {
            defer wg.Done()
            relay, err := s.relayPool.EnsureRelay(relayURL)
            if err != nil {
                log.Printf("Failed to connect to relay %s: %v", relayURL, err)
                return
            }
            err = rateLimitedRelayRequest(relay, event)
            if err != nil {
                log.Printf("Failed to publish event to relay %s: %v", relayURL, err)
            } else {
                log.Printf("Successfully published event to relay %s", relayURL)
            }
        }(relayURL)
    }
    wg.Wait()
    return nil
}

func NewBraggingService(config Config, privateKey string, relayPool *nostr.SimplePool) (*Service, error) {
	pubKey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		return nil, err
	}
	return &Service{
		config:     config,
		publicKey:  pubKey,
		privateKey: privateKey,
		relayPool:  relayPool,
	}, nil
}
