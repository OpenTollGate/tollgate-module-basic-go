// Example: How to send TIP-07 remote control commands to a TollGate router
//
// Usage:
//   go run send_command.go -controller-key <nsec/hex> -tollgate-pubkey <npub/hex> -command uptime
//   go run send_command.go -controller-key <nsec/hex> -tollgate-pubkey <npub/hex> -command reboot -args '{"delay_sec":60}'
//
// This demonstrates:
// 1. Creating a TIP-07 command event (kind 21024)
// 2. Signing it with controller private key
// 3. Publishing to relays
// 4. Listening for response events (kind 21025)

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const (
	CommandEventKind  = 21024
	ResponseEventKind = 21025
)

type CommandArgs struct {
	Args map[string]interface{} `json:"args,omitempty"`
}

func main() {
	// Parse command line flags
	controllerKey := flag.String("controller-key", "", "Controller private key (nsec or hex)")
	tollgatePubkey := flag.String("tollgate-pubkey", "", "TollGate public key to send command to (npub or hex)")
	command := flag.String("command", "uptime", "Command to execute (uptime, reboot, status)")
	argsJSON := flag.String("args", "{}", "Command arguments as JSON")
	deviceID := flag.String("device-id", "", "(Optional) Device ID for fleet management scenarios")
	relays := flag.String("relays", "wss://relay.damus.io,wss://nos.lol,wss://nostr.mom", "Comma-separated relay URLs")
	timeout := flag.Int("timeout", 30, "Response timeout in seconds")
	
	flag.Parse()

	if *controllerKey == "" || *tollgatePubkey == "" {
		flag.Usage()
		log.Fatal("Error: Both -controller-key and -tollgate-pubkey are required")
	}

	// Decode controller private key
	var controllerPrivKey string
	if _, data, err := nip19.Decode(*controllerKey); err == nil {
		controllerPrivKey = data.(string)
	} else {
		// Assume it's already hex
		controllerPrivKey = *controllerKey
	}

	// Decode TollGate public key
	var tollgatePubkeyHex string
	if prefix, data, err := nip19.Decode(*tollgatePubkey); err == nil {
		if prefix == "npub" {
			tollgatePubkeyHex = data.(string)
		} else {
			log.Fatal("Error: Invalid npub format")
		}
	} else {
		// Assume it's already hex
		tollgatePubkeyHex = *tollgatePubkey
	}

	// Get controller public key
	controllerPubkey, err := nostr.GetPublicKey(controllerPrivKey)
	if err != nil {
		log.Fatalf("Error: Invalid controller private key: %v", err)
	}

	// Parse command arguments
	var cmdArgs CommandArgs
	if err := json.Unmarshal([]byte(*argsJSON), &cmdArgs); err != nil {
		log.Fatalf("Error: Invalid JSON in -args: %v", err)
	}

	// Generate unique nonce
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())

	// Create command event
	event := nostr.Event{
		PubKey:    controllerPubkey,
		CreatedAt: nostr.Now(),
		Kind:      CommandEventKind,
		Tags: nostr.Tags{
			{"p", tollgatePubkeyHex},
			{"cmd", *command},
			{"nonce", nonce},
			{"device_id", *deviceID},
		},
		Content: *argsJSON,
	}

	// Sign the event
	if err := event.Sign(controllerPrivKey); err != nil {
		log.Fatalf("Error: Failed to sign event: %v", err)
	}

	log.Printf("📤 Sending TIP-07 command:")
	log.Printf("   Command: %s", *command)
	log.Printf("   To TollGate: %s", tollgatePubkeyHex)
	log.Printf("   From Controller: %s", controllerPubkey)
	log.Printf("   Event ID: %s", event.ID)
	log.Printf("   Device ID: %s", *deviceID)
	if cmdArgs.Args != nil && len(cmdArgs.Args) > 0 {
		log.Printf("   Args: %s", *argsJSON)
	}

	// Connect to relays and publish
	ctx := context.Background()
	relayURLs := parseRelays(*relays)
	
	publishCount := 0
	for _, relayURL := range relayURLs {
		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			log.Printf("⚠️  Failed to connect to %s: %v", relayURL, err)
			continue
		}
		defer relay.Close()

		if err := relay.Publish(ctx, event); err != nil {
			log.Printf("⚠️  Failed to publish to %s: %v", relayURL, err)
		} else {
			log.Printf("✅ Published to %s", relayURL)
			publishCount++
		}
	}

	if publishCount == 0 {
		log.Fatal("❌ Failed to publish to any relay")
	}

	log.Printf("\n⏳ Waiting for response (timeout: %ds)...", *timeout)

	// Listen for response
	responseReceived := make(chan bool, 1)
	
	go func() {
		for _, relayURL := range relayURLs {
			go listenForResponse(ctx, relayURL, event.ID, controllerPubkey, responseReceived)
		}
	}()

	// Wait for response or timeout
	select {
	case <-responseReceived:
		log.Printf("\n✅ Command completed successfully")
	case <-time.After(time.Duration(*timeout) * time.Second):
		log.Printf("\n⏱️  Timeout waiting for response")
		log.Printf("   Note: TollGate may still process the command")
		log.Printf("   Check if remote control is enabled in TollGate config")
		log.Printf("   Check if your controller pubkey is in allowed_pubkeys list")
	}
}

func listenForResponse(ctx context.Context, relayURL, commandEventID, controllerPubkey string, done chan bool) {
	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		return
	}
	defer relay.Close()

	// Subscribe to response events
	filter := nostr.Filter{
		Kinds: []int{ResponseEventKind},
		Tags: nostr.TagMap{
			"p":           []string{controllerPubkey},
			"in_reply_to": []string{commandEventID},
		},
		Since: nostr.Timestamp(time.Now().Add(-1 * time.Minute).Unix()),
	}

	sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		return
	}

	// Wait for response event
	timeout := time.After(35 * time.Second)
	for {
		select {
		case event := <-sub.Events:
			if event == nil {
				continue
			}

			// Parse response
			var response map[string]interface{}
			if err := json.Unmarshal([]byte(event.Content), &response); err != nil {
				log.Printf("⚠️  Failed to parse response: %v", err)
				continue
			}

			// Display response
			log.Printf("\n📥 Received response from TollGate:")
			log.Printf("   Event ID: %s", event.ID)
			log.Printf("   From: %s", event.PubKey)
			
			// Get status from tags
			statusTag := event.Tags.GetFirst([]string{"status"})
			if statusTag != nil && len(statusTag.Value()) >= 2 {
				log.Printf("   Status: %s", statusTag.Value()[1])
			}

			// Get device_id from tags
			deviceTag := event.Tags.GetFirst([]string{"device_id"})
			if deviceTag != nil && len(deviceTag.Value()) >= 2 {
				log.Printf("   Device ID: %s", deviceTag.Value()[1])
			}

			// Pretty print response content
			prettyJSON, _ := json.MarshalIndent(response, "   ", "  ")
			log.Printf("   Response: %s", string(prettyJSON))

			select {
			case done <- true:
			default:
			}
			return

		case <-timeout:
			return
		}
	}
}

func parseRelays(relayStr string) []string {
	relays := []string{}
	for _, r := range nostr.ParseRelayList(relayStr) {
		relays = append(relays, r)
	}
	return relays
}
