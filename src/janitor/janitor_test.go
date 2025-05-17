package janitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

func TestParseNIP94Event(t *testing.T) {
	event := nostr.Event{
		Tags: nostr.Tags{
			{"url", "https://example.com/package.ipk"},
			{"url", "https://example.com/package.ipk"},
			{"version", "1.2.3"},
			{"architecture", "aarch64"},
			{"filename", "package.ipk"},
			{"timestamp", "1643723900"},
			{"release_channel", "stable"},
		},
		CreatedAt: 1643723900,
	}

	packageURL, version, _, _, _, timestamp, err := parseNIP94Event(event)
	if err != nil {
		t.Errorf("parseNIP94Event failed: %v", err)
	}
	url := packageURL
	if err != nil {
		t.Errorf("parseNIP94Event failed: %v", err)
	}
	if url != "https://example.com/package.ipk" {
		t.Errorf("expected URL %s, got %s", "https://example.com/package.ipk", url)
	}
	if version != "1.2.3" {
		t.Errorf("expected version %s, got %s", "1.2.3", version)
	}
	parsedTimestamp, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		t.Errorf("failed to parse timestamp: %v", err)
	}
	if parsedTimestamp != int64(1643723900) {
		t.Errorf("expected timestamp %d, got %d", 1643723900, parsedTimestamp)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name             string
		newVersion       string
		newTimestamp     int64
		currentVersion   string
		currentTimestamp int64
		expected         bool
	}{
		{
			name:             "newer version and newer timestamp",
			newVersion:       "1.0.1",
			newTimestamp:     2,
			currentVersion:   "1.0.0",
			currentTimestamp: 1,
			expected:         true,
		},
		{
			name:             "same version but newer timestamp",
			newVersion:       "1.0.0",
			newTimestamp:     2,
			currentVersion:   "1.0.0",
			currentTimestamp: 1,
			expected:         false,
		},
		{
			name:             "newer version but older timestamp",
			newVersion:       "1.0.1",
			newTimestamp:     1,
			currentVersion:   "1.0.0",
			currentTimestamp: 2,
			expected:         false,
		},
		{
			name:             "older version and older timestamp",
			newVersion:       "0.9.9",
			newTimestamp:     1,
			currentVersion:   "1.0.0",
			currentTimestamp: 2,
			expected:         false,
		},
		{
			name:             "newer version with build metadata",
			newVersion:       "1.0.1+build123",
			newTimestamp:     2,
			currentVersion:   "1.0.0",
			currentTimestamp: 1,
			expected:         true,
		},
		{
			name:             "newer version without build metadata",
			newVersion:       "1.0.1",
			newTimestamp:     2,
			currentVersion:   "1.0.0",
			currentTimestamp: 1,
			expected:         true,
		},
		{
			name:             "newer version with build metadata and current version with build metadata",
			newVersion:       "1.0.1+build123",
			newTimestamp:     2,
			currentVersion:   "1.0.0+build456",
			currentTimestamp: 1,
			expected:         true,
		},
		{
			name:             "newer version without build metadata and current version with build metadata",
			newVersion:       "1.0.1",
			newTimestamp:     2,
			currentVersion:   "1.0.0+build456",
			currentTimestamp: 1,
			expected:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentVersion, err := version.NewVersion(tt.currentVersion)
			if err != nil {
				t.Errorf("invalid current version: %v", err)
				return
			}
			currentVersionStr := currentVersion.String()
			newTimestampStr := fmt.Sprintf("%d", tt.newTimestamp)
			if got := isNewerVersion(tt.newVersion, currentVersionStr, newTimestampStr); got != tt.expected {
				t.Errorf("isNewerVersion() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEventMapCollision(t *testing.T) {
	// Create a channel to simulate Nostr events
	eventChan := make(chan *nostr.Event)

	// Start a goroutine to process events
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		eventMap := make(map[string]*packageEvent)
		for event := range eventChan {
			packageURL, versionStr, _, _, filename, timestamp, err := parseNIP94Event(*event)
			if err != nil {
				t.Errorf("parseNIP94Event failed: %v", err)
			}
			key := fmt.Sprintf("%v-%s", filename, versionStr)
			existingPackageEvent, ok := eventMap[key]
			if ok {
				parsedTimestamp, err := strconv.ParseInt(timestamp, 10, 64)
				if err != nil {
					t.Errorf("failed to parse timestamp: %v", err)
				}
				if parsedTimestamp > int64(existingPackageEvent.event.CreatedAt) {
					eventMap[key] = &packageEvent{
						event:      event,
						packageURL: packageURL,
					}
				}
			} else {
				eventMap[key] = &packageEvent{
					event:      event,
					packageURL: packageURL,
				}
			}
		}
		// Check the final state of eventMap
		if len(eventMap) != 1 {
			t.Errorf("expected eventMap to have 1 entry, got %d", len(eventMap))
		}
		event := eventMap["package.ipk-1.0.0"].event
		if event.CreatedAt != 1643723902 {
			t.Errorf("expected latest timestamp 1643723902, got %d", event.CreatedAt)
		}
	}()

	// Send events to the channel
	event1 := &nostr.Event{
		PubKey: "trusted_pubkey",
		Tags: nostr.Tags{
			{"url", "https://example.com/package.ipk"},
			{"version", "1.0.0"},
			{"arch", "aarch64"},
			{"branch", "main"},
			{"filename", "package.ipk"},
		},
		CreatedAt: 1643723900,
	}
	event1.Sign("private_key")

	event2 := &nostr.Event{
		PubKey: "trusted_pubkey",
		Tags: nostr.Tags{
			{"url", "https://example.com/package.ipk"},
			{"version", "1.0.0"},
			{"arch", "aarch64"},
			{"branch", "main"},
			{"filename", "package.ipk"},
		},
		CreatedAt: 1643723901,
	}
	event2.Sign("private_key")

	event3 := &nostr.Event{
		PubKey: "trusted_pubkey",
		Tags: nostr.Tags{
			{"url", "https://example.com/package.ipk"},
			{"version", "1.0.0"},
			{"arch", "aarch64"},
			{"branch", "main"},
			{"filename", "package.ipk"},
		},
		CreatedAt: 1643723902,
	}
	event3.Sign("private_key")

	eventChan <- event1
	eventChan <- event2
	eventChan <- event3

	close(eventChan)
	wg.Wait()
}

func TestUpdateInstallConfig(t *testing.T) {
	configFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configFile.Name())

	tmpFile, err := os.CreateTemp("", "install.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := config_manager.NewConfigManager(configFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewJanitor(cm)
	if err != nil {
		t.Fatal(err)
	}

	pkgPath := "/path/to/package"

	installConfig, err := cm.LoadInstallConfig()
	if err != nil {
		t.Errorf("LoadInstallConfig returned error: %v", err)
	}
	if installConfig.PackagePath != pkgPath {
		t.Errorf("Install config not updated correctly")
	}
}

func TestDownloadPackage(t *testing.T) {
	configFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configFile.Name())

	currentTime := time.Now().Unix()
	fourWeeksAgo := currentTime - int64(4*7*24*60*60) // 4 weeks ago in seconds

	relays := []string{"wss://relay.damus.io", "wss://nos.lol", "wss://nostr.mom"}
	trustedMaintainers := []string{"5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"}

	customConfig := map[string]interface{}{
		"relays":              relays,
		"trusted_maintainers": trustedMaintainers,
		"package_info": map[string]interface{}{
			"version":   "0.0.1",
			"timestamp": fourWeeksAgo,
		},
	}

	configData, err := json.Marshal(customConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(configFile.Name(), configData, 0644); err != nil {
		t.Fatal(err)
	}

	cm, err := config_manager.NewConfigManager(configFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewJanitor(cm)
	if err != nil {
		t.Fatal(err)
	}

	eventChan := make(chan *nostr.Event)
	var wg sync.WaitGroup
	wg.Add(1)

	var packageURL string
	var eventHandlerWG sync.WaitGroup
	eventHandlerWG.Add(1)
	go func() {
		defer eventHandlerWG.Done()
		for event := range eventChan {
			packageURL, _, _, _, _, _, err = parseNIP94Event(*event)
			if err != nil {
				t.Errorf("parseNIP94Event failed: %v", err)
			}
			if packageURL != "" {
				return
			}
		}
	}()

	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var subClosers sync.WaitGroup
	for _, relayURL := range relays {
		subClosers.Add(1)
		go func(relayURL string) {
			t.Logf("Connecting to relay %s", relayURL)
			defer subClosers.Done()
			relay, err := relayPool.EnsureRelay(relayURL)
			if err != nil {
				t.Logf("Failed to connect to relay %s: %v", relayURL, err)
				return
			}
			t.Logf("Connected to relay %s", relayURL)

			filter := nostr.Filter{
				Kinds: []int{1063}, // NIP-94 event kind
			}

			t.Logf("Subscribing to NIP-94 events on relay %s with filter: %+v", relayURL, filter)
			sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
			if err != nil {
				t.Logf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
				return
			}

			t.Logf("Subscribed to NIP-94 events on relay %s", relayURL)
			for event := range sub.Events {
				if event.Kind != 1063 {
					t.Logf("Unexpected event kind %d from relay %s", event.Kind, relayURL)
					continue
				}

				if !contains(trustedMaintainers, event.PubKey) {
					continue
				}

				eventChan <- event
			}
		}(relayURL)
	}

	go func() {
		subClosers.Wait()
		close(eventChan)
	}()

	eventHandlerWG.Wait()
	wg.Done()

	// Provide time to establish relay connections and find a valid blossom URL
	time.Sleep(3 * time.Second) // Wait for goroutines to finish
	t.Logf("Using package URL: %s", packageURL)

	if packageURL == "" {
		t.Skip("No NIP-94 event found with package URL. Skipping test.")
		return
	}

	t.Logf("Starting to DownloadPackage")
	pkgPath, pkg, err := DownloadPackage(cm, packageURL, "some_checksum")
	if err != nil {
		t.Errorf("DownloadPackage failed: %v", err)
	}

	t.Logf("Download package succeeded")
	if len(pkg) == 0 {
		t.Errorf("expected non-empty package content")
	}
	if pkgPath == "" {
		t.Errorf("expected non-empty package path")
	}
}
