package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/nbd-wtf/go-nostr"
	)

type JanitorConfig struct {
	Relays             []string `json:"relays"`
	TrustedMaintainers []string `json:"trusted_maintainers"`
}

func loadJanitorConfig(path string) (*JanitorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config JanitorConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

type Janitor struct {
	relays               []string
	trustedMaintainers    []string
}

func NewJanitor(relays []string, trustedMaintainers []string) *Janitor {
	return &Janitor{
		relays:               relays,
		trustedMaintainers: trustedMaintainers,
	}
}

func (j *Janitor) ListenForNIP94Events() {
	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)

	for _, relayURL := range j.relays {
		relay, err := relayPool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}

		sub, err := relay.Subscribe(ctx, []nostr.Filter{
			{
				Kinds: []int{1063}, // NIP-94 event kind
			},
		})
		if err != nil {
			log.Printf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
			continue
		}

		go func(relayURL string, sub *nostr.Subscription) {
				untrustedEventCount := 0
				totalEvents := 0
				for event := range sub.Events {
					totalEvents++
					if !contains(j.trustedMaintainers, event.PubKey) {
						untrustedEventCount++
						continue
					}
					ok, err := event.CheckSignature()
					if err != nil || !ok {
						log.Printf("Invalid signature for NIP-94 event %s from relay %s: %v", event.ID, relayURL, err)
						continue
				}
				packageURL, version, timestamp, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					continue
				}
				log.Printf("Parsed NIP-94 event %s: version=%s, timestamp=%d, URL=%s", event.ID, version, timestamp, packageURL)
				if isNewerVersion(version, timestamp) {
					log.Printf("Newer package version available: %s", version)
					pkg, err := j.DownloadPackage(packageURL)
					if err != nil {
						log.Printf("Error downloading package: %v", err)
						continue
					}
					err = j.InstallPackage(pkg)
					if err != nil {
						log.Printf("Error installing package: %v", err)
						continue
					}
					log.Printf("Successfully installed new package version: %s", version)
				}
			}
		}(relayURL, sub)
	}
}

func (j *Janitor) DownloadPackage(url string) ([]byte, error) {
	return nil, nil
}

func (j *Janitor) InstallPackage(pkg []byte) error {
	return nil
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

// parseNIP94Event extracts package information from a NIP-94 event
func parseNIP94Event(event nostr.Event) (string, string, int64, error) {
	var url string
	version := "1.0.0" // Default version if not found
	timestamp := int64(event.CreatedAt)

	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "url" && len(tag) > 1 {
			url = tag[1]
		}
		if len(tag) > 0 && tag[0] == "version" && len(tag) > 1 {
			version = tag[1]
		}
	}

	if url == "" || version == "" || timestamp == 0 {
		return "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	return url, version, timestamp, nil
}

func isNewerVersion(version string, timestamp int64) bool {
	currentTimestamp, err := getCurrentPackageTimestamp()
	if err != nil {
		log.Printf("Error getting current package timestamp: %v", err)
		return false
	}
	return timestamp > currentTimestamp
}

func getCurrentPackageTimestamp() (int64, error) {
	return 0, nil
}

func main() {
	config, err := loadJanitorConfig("files/etc/tollgate/config.json")
	if err != nil {
		log.Fatal(err)
	}

	janitor := NewJanitor(config.Relays, config.TrustedMaintainers)
	janitor.ListenForNIP94Events()
}