package bragging

import (
	"context"
	"fmt"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"sync"
	"time"
)

func (s *Service) PublishEvent(event *nostr.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errors := make(chan error, len(s.config.Relays))

	for _, relayURL := range s.config.Relays {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			for attempt := 1; attempt <= 3; attempt++ {
				log.Printf("Attempting to connect to relay %s (attempt %d)", url, attempt)
				relay, err := s.relayPool.EnsureRelay(url)
				if err != nil {
					log.Printf("Relay connection failed (attempt %d): %v", attempt, err)
					continue
				}

				status := relay.Publish(ctx, *event)
				if status != nil {
					log.Printf("Publish failed (attempt %d): %s", attempt, status)
				} else {
					log.Printf("Successfully published to relay %s", url)
				} else {
					return
				}
			}
			errors <- fmt.Errorf("failed to publish to %s after 3 attempts", url)
		}(relayURL)
	}

	wg.Wait()
	close(errors)
	return <-errors // Return first error if any
}
