package bragging

import (
	"context"
	"fmt"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"strings"
)

func AnnounceSuccessfulPayment(configManager *config_manager.ConfigManager, amount int64, durationSeconds int64) error {
	config, err := configManager.LoadConfig()
	if err != nil {
		return err
	}
	privateKey := config.TollgatePrivateKey


	event := nostr.Event{
		Kind:      1,
		CreatedAt: nostr.Now(),
		Tags:      make(nostr.Tags, 0),
		Content:   "",
	}

	var content string
	for _, field := range config.Bragging.Fields {
		switch field {
		case "amount":
			event.Tags = append(event.Tags, nostr.Tag{"amount", fmt.Sprintf("%d", amount)})
			content += fmt.Sprintf("Amount: %d sats,\n", amount)
		case "mint":
			event.Tags = append(event.Tags, nostr.Tag{"mint", config.AcceptedMints[0]})
			content += fmt.Sprintf("Mint: %s,\n", config.AcceptedMints[0])
		case "duration":
			event.Tags = append(event.Tags, nostr.Tag{"duration", fmt.Sprintf("%d", durationSeconds)})
			content += fmt.Sprintf("Duration: %d seconds", durationSeconds)
		}
	}

	if content != "" {
		content = strings.TrimSuffix(content, ",")
		content += "\n\n#BraggingTollGateRawData"
	}

	event.Content = content

	err = event.Sign(privateKey)
	if err != nil {
		log.Printf("Failed to sign bragging event: %v", err)
		return err
	}

	for _, relayURL := range config.Relays {
		relay, err := configManager.RelayPool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}
		err = relay.Publish(context.Background(), event)
		if err != nil {
			log.Printf("Failed to publish event to relay %s: %v", relayURL, err)
		} else {
			log.Printf("Successfully published event to relay %s", relayURL)
		}
	}

	return nil
}
