package bragging

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

var sem = make(chan bool, 5) // Allow up to 5 concurrent publishes

func PublishEvent(configManager *config_manager.ConfigManager, event *nostr.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	config, err := configManager.LoadConfig()
	if err != nil {
		return err
	}

	for _, relayURL := range config.Relays {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			relay, err := configManager.GetPublicPool().EnsureRelay(url)
			if err != nil {
				log.Printf("Failed to connect to relay %s: %v", url, err)
				return
			}
			sem <- true              // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			for attempt := 1; attempt <= 3; attempt++ {
				log.Printf("Attempting to connect to relay %s (attempt %d)", url, attempt)
				var err error
				relay, err = configManager.GetPublicPool().EnsureRelay(url)
				if err != nil {
					log.Printf("Relay connection failed (attempt %d): %v", attempt, err)
					continue
				}

				status := relay.Publish(ctx, *event)
				if status != nil {
					log.Printf("Publish failed (attempt %d): %s", attempt, status)
				} else {
					log.Printf("Successfully published to relay %s", url)
				}
			}
		}(relayURL)
	}
	wg.Wait()
	return nil
}
