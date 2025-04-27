package bragging

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventCreation(t *testing.T) {
	config := Config{
		Enabled:   true,
		UserOptIn: true,
		Fields:    []string{"amount", "mint", "duration"},
		Template:  "New sale! {{.amount}} sats via {{.mint}} for {{.duration}} sec",
		Relays:    []string{"wss://relay.damus.io"},
	}
	privateKey := nostr.GeneratePrivateKey()
	service, err := NewService(config, privateKey)
	require.NoError(t, err)

	saleData := map[string]interface{}{
		"amount":   150,
		"mint":     "https://mint.example",
		"duration": 900,
	}

	event, err := service.CreateEvent(saleData)
	require.NoError(t, err)
	assert.Equal(t, 1, event.Kind)
	assert.Contains(t, event.Content, "amount: 150")
	assert.Contains(t, event.Content, "mint: https://mint.example")
	assert.Contains(t, event.Content, "duration: 900")
	assert.Len(t, event.Tags, 3) // amount, mint, duration
}

func TestTemplateRendering(t *testing.T) {
	config := Config{Template: "Sale: {{.amount}} @ {{.mint}}"}
	privateKey := nostr.GeneratePrivateKey()
	service, err := NewService(config, privateKey)
	require.NoError(t, err)

	output := service.renderTemplate(map[string]interface{}{
		"amount": 150,
		"mint":   "https://mint.example",
	})

	assert.Contains(t, output, "Sale: 150 @ https://mint.example")
}

func TestRelayPublish(t *testing.T) {
	configJSON := `{
        "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
        "accepted_mint": "https://mint.minibits.ccash/Bitcoin",
        "price_per_minute": 1,
        "min_payment": 1,
        "mint_fee": 0,
        "bragging": {
            "enabled": true,
            "relays": ["wss://relay.damus.io", "wss://nostr.mom"],
            "fields": ["amount", "mint", "duration"]
        }
    }`

	var mainConfig map[string]interface{}
	err := json.Unmarshal([]byte(configJSON), &mainConfig)
	require.NoError(t, err)

	braggingConfig, ok := mainConfig["bragging"].(map[string]interface{})
	require.True(t, ok)
	_ = braggingConfig // use or remove this line

	relayURL := "wss://relay.damus.io"
	service := &Service{
		config: Config{
			Enabled: true,
			Relays:  []string{relayURL},
			Fields:  []string{"amount", "mint", "duration"},
		},
		relayPool: nostr.NewSimplePool(context.Background()),
	}

	saleData := map[string]interface{}{
		"amount":   150,
		"mint":     "https://mint.example",
		"duration": 900,
	}

	event, err := service.CreateEvent(saleData)
	require.NoError(t, err)

	privateKey := nostr.GeneratePrivateKey()
	publicKey, err := nostr.GetPublicKey(privateKey)
	require.NoError(t, err)
	t.Logf("Public Key: %s", publicKey)
	t.Logf("Signer nsec: %s", privateKey)

	err = event.Sign(privateKey)
	require.NoError(t, err)
	t.Logf("Event ID: %s", event.ID)

	err = service.PublishEvent(event)
	require.NoError(t, err)
	t.Logf("Published event to relay: %s", relayURL)

	// Fetch the event from the relay to verify it's stored
	filter := nostr.Filter{
		IDs: []string{event.ID},
	}
	relay, err := service.relayPool.EnsureRelay(relayURL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()
	events, err := relay.QuerySync(ctx, filter)
	require.NoError(t, err)
	require.NotEmpty(t, events, "event not found on relay")
	assert.Equal(t, event.ID, events[0].ID)
	t.Logf("Fetched event from relay: %+v", events[0])
	require.NoError(t, err)
	t.Logf("Public Key: %s", publicKey)
	t.Logf("Signer nsec: %s", privateKey)

	event.Sign(privateKey)
	t.Logf("Event ID: %s", event.ID)

	err = service.PublishEvent(event)
	t.Logf("Published event to relay: %s", relayURL)
	require.NoError(t, err)

	// Fetch the event from the relay to verify it's stored
	filter = nostr.Filter{
		IDs: []string{event.ID},
	}
	relay, err = service.relayPool.EnsureRelay(relayURL)
	require.NoError(t, err)
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()
	events, err = relay.QuerySync(ctx, filter)
	require.NoError(t, err)
	require.NotEmpty(t, events, "event not found on relay")
	assert.Equal(t, event.ID, events[0].ID)
	t.Logf("Fetched event from relay: %+v", events[0])
}
