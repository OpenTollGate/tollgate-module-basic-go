package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

type packageEvent struct {
	event      *nostr.Event
	packageURL string
}

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
	configPath         string
}

func NewJanitor(relays []string, trustedMaintainers []string, currentVersion string, currentTimestamp int64, configPath string) (*Janitor, error) {
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
	var wg sync.WaitGroup
	eventChan := make(chan *nostr.Event, 1000) // Buffered channel to handle events from multiple relays

	for _, relayURL := range j.relays {
		wg.Add(1)
		go func(relayURL string) {
			defer wg.Done()
			log.Printf("Connecting to relay: %s", relayURL)
			relay, err := relayPool.EnsureRelay(relayURL)
			if err != nil {
				log.Printf("Failed to connect to relay %s: %v", relayURL, err)
				return
			}

			sub, err := relay.Subscribe(ctx, []nostr.Filter{
				{
					Kinds: []int{1063}, // NIP-94 event kind
				},
			})
			if err != nil {
				log.Printf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
				return
			}

			log.Printf("Subscribed to NIP-94 events on relay %s", relayURL)
			for event := range sub.Events {
				eventChan <- event
			}
			log.Printf("Stopped listening for NIP-94 events on relay %s", relayURL)
		}(relayURL)
	}

	go func() {
		wg.Wait()
		close(eventChan) // Close the channel when all relays are done
	}()

	eventMap := make(map[string]*packageEvent)
	totalEvents := 0
	untrustedEventCount := 0
	trustedEventCount := 0
	collisionCount := 0

	for event := range eventChan {
		totalEvents++

		if !contains(j.trustedMaintainers, event.PubKey) {
			untrustedEventCount++
			continue
		} else {
			trustedEventCount++
			ok, err := event.CheckSignature()
			if err != nil || !ok {
				log.Printf("Invalid signature for NIP-94 event %s: %v", event.ID, err)
				continue
			}

			packageURL, versionStr, filename, timestamp, err := parseNIP94Event(*event)
			if err != nil {
				continue
			}

			key := fmt.Sprintf("%s-%s", filename, versionStr)
			existingPackageEvent, ok := eventMap[key]
			if ok {
				collisionCount++
				if timestamp > int64(existingPackageEvent.event.CreatedAt) {
					eventMap[key] = &packageEvent{
						event:      event,
						packageURL: packageURL,
					}
					log.Printf("Collision detected for file %s, version %s, updating to newer event with timestamp %d", filename, versionStr, timestamp)
				}
			} else {
				log.Printf("Found occurrence of package %s, version %s, timestamp %d", filename, versionStr, timestamp)
				eventMap[key] = &packageEvent{
					event:      event,
					packageURL: packageURL,
				}
			}
		}
	}

	log.Printf("Finished processing NIP-94 events. Total events: %d, Untrusted events: %d, Collisions: %d", totalEvents, untrustedEventCount, collisionCount)

	for _, packageEvent := range eventMap {
		event := packageEvent.event
		_, versionStr, _, timestamp, err := parseNIP94Event(*event)
		if err != nil {
			log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
			continue
		}

		log.Printf("Newest NIP-94 event for version %s: event ID=%s, timestamp=%d", versionStr, event.ID, timestamp)
		if isNewerVersion(versionStr, timestamp, j.currentVersion, j.currentTimestamp) {
			log.Printf("Newer package version available: %s", versionStr)
			pkg, err := j.DownloadPackage(packageEvent.packageURL)
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
			runPostInstallScript(j.configPath)
		}
	}
}

func (j *Janitor) DownloadPackage(url string) ([]byte, error) {
	log.Printf("Downloading package from %s", url)
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error downloading package: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to download package. Status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("failed to download package. Status code: %d", resp.StatusCode)
	}

	pkg, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading package content: %v", err)
		return nil, err
	}

	log.Println("Package downloaded successfully")
	return pkg, nil
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
	tmpFile, err := os.CreateTemp("", "package.ipk")
	if err != nil {
		log.Printf("Error creating temp file: %v", err)
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(pkg); err != nil {
		log.Printf("Error writing package to temp file: %v", err)
		return err
	}
	tmpFile.Close()

	cmd := exec.Command("opkg", "install", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error installing package: %v, output: %s", err, output)
		return err
	}

	log.Println("Package installed successfully")
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
	// arch := tagMap["arch"]
	// branch := tagMap["branch"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	// log.Printf("Parsed NIP-94 event: url=%s, version=%s, arch=%s, branch=%s, filename=%s, timestamp=%d",
	//	url, version, arch, branch, filename, timestamp)

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

func runPostInstallScript(configPath string) {
	log.Println("Running post-install script")
	config, err := loadJanitorConfig(configPath)
	if err != nil {
		log.Printf("Error loading config: %v", err)
		return
	}

	// Update the config with new package version and timestamp
	newVersion := "1.2.3" // This should be dynamically obtained from the installed package
	newTimestamp := time.Now().Unix()
	config.PackageInfo.Version = newVersion
	config.PackageInfo.Timestamp = newTimestamp

	// Save the updated config
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Error marshaling config: %v", err)
		return
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		log.Printf("Error writing config file: %v", err)
		return
	}

	log.Println("Post-install script completed successfully")
}

func main() {
	log.Println("Janitor module starting")
	config, err := loadJanitorConfig("files/etc/tollgate/config.json")
	if err != nil {
		log.Fatal(err)
	}

	janitor, err := NewJanitor(config.Relays, config.TrustedMaintainers, config.PackageInfo.Version, config.PackageInfo.Timestamp, "files/etc/tollgate/config.json")
	if err != nil {
		log.Fatal(err)
	}

	janitor.ListenForNIP94Events()
}
