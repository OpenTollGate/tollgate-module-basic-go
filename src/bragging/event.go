package bragging

import (
	"fmt"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"strings"
)

func CreateEvent(configManager *config_manager.ConfigManager, saleData map[string]interface{}) (*nostr.Event, error) {
	config, err := configManager.LoadConfig()
	if err != nil {
		return nil, err
	}
	/**
	TODO: get the state of enabled from bragging.enabled using the config manager:
	root@OpenWrt:~# cat /etc/tollgate/config.json | jq
	{
	  "tollgate_private_key": "79b7106c596aec6083c195df307e5d1329425f19a813b94e865f0e72536cfd49",
	  "accepted_mints": [
	    "https://mint.minibits.cash/Bitcoin",
	    "https://mint2.nutmix.cash"
	  ],
	  "price_per_minute": 1,
	  "bragging": {
	    "enabled": true,
	    "fields": [
	      "amount",
	      "mint",
	      "duration"
	    ]
	  },
	  "relays": [
	    "wss://relay.damus.io",
	    "wss://nos.lol",
	    "wss://nostr.mom"
	  ],
	  "trusted_maintainers": [
	    "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"
	  ],
	  "fields_to_be_reviewed": [
	    "price_per_minute",
	    "relays",
	    "tollgate_private_key",
	    "trusted_maintainers"
	  ],
	  "current_installation_id": "32354ac5fbe5b777a7da16549c6233f709d96b1a1d9d30b71c888fb23ddc1652"
	
		return nil, nil
	
	}
		**/
	enabled := false
	fields := []string{}
	for _, field := range config.Bragging.Fields {
		if field == "amount" || field == "mint" || field == "duration" {
			fields = append(fields, field)
		}
	}
	if len(fields) > 0 {
		enabled = true
	}
	if !enabled {
		return nil, nil
	}

	event := &nostr.Event{
		Kind:      1,
		CreatedAt: nostr.Now(),
		Tags:      make(nostr.Tags, 0),
		Content:   "",
	}

	var content string
	for _, field := range config.Bragging.Fields {
		if value, exists := saleData[field]; exists {
			event.Tags = append(event.Tags, nostr.Tag{field, fmt.Sprint(value)})
			content += fmt.Sprintf("%s: %v, ", field, value)
		}
	}

	// Trim the trailing comma and space if content is not empty
	if content != "" {
		content = strings.TrimSuffix(content, ", ")
	}

	event.Content = content

	privateKey := config.TollgatePrivateKey
	err = event.Sign(privateKey)
	if err != nil {
		log.Printf("Failed to sign event: %v", err)

		return event, nil

	}
	return nil, err
}
