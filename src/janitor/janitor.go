package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

type JanitorConfig struct {
	Relays             []string `json:"relays"`
	TrustedMaintainers []string `json:"trusted_maintainers"`
	PackageInfo        struct {
		Version   string `json:"version"`
		Timestamp int64  `json:"timestamp"`
	} `json:"package_info"`
}

func loadJanitorConfig(path string) (*JanitorConfig, error) {
	log.Printf("Loading configuration from %s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Error reading config file: %v", err)
		return nil, err
	}

	var config JanitorConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Printf("Error unmarshaling config: %v", err)
		return nil, err
	}

	log.Printf("Configuration loaded: %+v", config)
	return &config, nil
}

type Janitor struct {
	relays             []string
	trustedMaintainers []string
	currentVersion     *version.Version
	currentTimestamp   int64
}

func NewJanitor(relays []string, trustedMaintainers []string, currentVersion string, currentTimestamp int64) (*Janitor, error) {
	log.Printf("Creating new Janitor instance")
	v, err := version.NewVersion(currentVersion)
	if err != nil {
		log.Printf("Invalid current version: %v", err)
		return nil, err
	}
	return &Janitor{
		relays:             relays,
		trustedMaintainers: trustedMaintainers,
		currentVersion:     v,
		currentTimestamp:   currentTimestamp,
	}, nil
}

func (j *Janitor) ListenForNIP94Events() {
	log.Println("Starting to listen for NIP-94 events")
	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)

	for _, relayURL := range j.relays {
		log.Printf("Connecting to relay: %s", relayURL)
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
			log.Printf("Subscribed to NIP-94 events on relay %s", relayURL)
			totalEvents := 0
			untrustedEventCount := 0
			trustedEventCount := 0
			eventMap := make(map[string]*nostr.Event)
			collisionCount := 0

			for event := range sub.Events {
				totalEvents++

				if !contains(j.trustedMaintainers, event.PubKey) {
					untrustedEventCount++
					//log.Printf("Received untrusted event from pubkey %s", event.PubKey)
					continue
				} else {
					trustedEventCount++
					ok, err := event.CheckSignature()
					if err != nil || !ok {
						log.Printf("Invalid signature for NIP-94 event %s from relay %s: %v", event.ID, relayURL, err)
						continue
					}

					packageURL, versionStr, filename, timestamp, err := parseNIP94Event(*event)
					if err != nil {
						//log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
						continue
					}

					//log.Printf("Received trusted event from pubkey %s", event.PubKey)
					//log.Printf("Received event from relay %s: %+v", relayURL, event)
					key := fmt.Sprintf("%s-%s", packageURL, versionStr)
					existingEvent, ok := eventMap[key]
					if ok {
						log.Printf("Repeat occurence of package %s, version %s, timestamp %s ", filename, versionStr, timestamp)
						collisionCount++
						if timestamp > int64(existingEvent.CreatedAt) {
							eventMap[key] = event
							log.Printf("Collision detected for version %s, updating to newer event", versionStr)
						}
					} else {
						log.Printf("First occurrence of package %s, version %s, timestamp %s ", filename, versionStr, timestamp)
						eventMap[key] = event
					}
				}
			}

			log.Printf("Stopped listening for NIP-94 events on relay %s. Total events: %d, Untrusted events: %d, Collisions: %d", relayURL, totalEvents, untrustedEventCount, collisionCount)

			for _, event := range eventMap {
				packageURL, versionStr, _, timestamp, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					continue
				}

				log.Printf("Newest NIP-94 event for version %s: event ID=%s, timestamp=%d, URL=%s", versionStr, event.ID, timestamp, packageURL)
				if isNewerVersion(versionStr, timestamp, j.currentVersion, j.currentTimestamp) {
					log.Printf("Newer package version available: %s", versionStr)
					pkg, err := j.DownloadPackage(packageURL)
					if err != nil {
						log.Printf("Error downloading package: %v", err)
						continue
					}

					err = j.verifyPackageChecksum(pkg, *event)
					if err != nil {
						log.Printf("Error verifying package checksum: %v", err)
						continue
					}

					err = j.InstallPackage(pkg)
					if err != nil {
						log.Printf("Error installing package: %v", err)
						continue
					}

					log.Printf("Successfully installed new package version: %s", versionStr)
					runPostInstallScript()
				}
			}
		}(relayURL, sub)
	}
}

func (j *Janitor) DownloadPackage(url string) ([]byte, error) {
	log.Printf("Downloading package from %s", url)
	// implement logic to download the package
	return nil, nil
}

func (j *Janitor) verifyPackageChecksum(pkg []byte, event nostr.Event) error {
	log.Println("Verifying package checksum")
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "x" && len(tag) > 1 {
			expectedHash := tag[1]
			actualHash := sha256.Sum256(pkg)
			if expectedHash != hex.EncodeToString(actualHash[:]) {
				return fmt.Errorf("package checksum verification failed")
			}
			log.Println("Package checksum verified successfully")
		}
	}
	return nil
}

func (j *Janitor) InstallPackage(pkg []byte) error {
	log.Println("Installing package")
	// implement logic to install the package
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
func parseNIP94Event(event nostr.Event) (string, string, string, int64, error) {
	requiredTags := []string{"url", "version", "arch", "branch", "filename"}
	tagMap := make(map[string]string)

	for _, tag := range event.Tags {
		if len(tag) > 0 && len(tag) > 1 {
			tagMap[tag[0]] = tag[1]
		}
	}

	// Check if all required tags are present
	for _, tag := range requiredTags {
		if _, ok := tagMap[tag]; !ok {
			return "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tag '%s'", tag)
		}
	}

	url := tagMap["url"]
	version := tagMap["version"]
	arch := tagMap["arch"]
	branch := tagMap["branch"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	log.Printf("Parsed NIP-94 event: url=%s, version=%s, arch=%s, branch=%s, filename=%s, timestamp=%d",
		url, version, arch, branch, filename, timestamp)

	if url == "" || version == "" || timestamp == 0 {
		return "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	return url, version, filename, timestamp, nil
}

func isNewerVersion(newVersion string, newTimestamp int64, currentVersion *version.Version, currentTimestamp int64) bool {
	newVersionObj, err := version.NewVersion(newVersion)
	if err != nil {
		log.Printf("Invalid new version: %v", err)
		return false
	}
	return newVersionObj.GreaterThan(currentVersion) && newTimestamp > currentTimestamp
}

func runPostInstallScript() {
	log.Println("Running post-install script")
	// implement logic to run the post-install script
}

func main() {
	log.Println("Janitor module starting")
	config, err := loadJanitorConfig("files/etc/tollgate/config.json")
	if err != nil {
		log.Fatal(err)
	}

	janitor, err := NewJanitor(config.Relays, config.TrustedMaintainers, config.PackageInfo.Version, config.PackageInfo.Timestamp)
	if err != nil {
		log.Fatal(err)
	}

	janitor.ListenForNIP94Events()
}
