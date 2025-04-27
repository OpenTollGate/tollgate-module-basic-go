package main

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

func TestParseNIP94Event(t *testing.T) {
	event := nostr.Event{
		Tags: nostr.Tags{
			{"url", "https://example.com/package.ipk"},
			{"version", "1.2.3"},
			{"arch", "aarch64"},
			{"branch", "main"},
			{"filename", "package.ipk"},
		},
		CreatedAt: 1643723900,
	}

	url, version, filename, timestamp, err := parseNIP94Event(event)
	if err != nil {
		t.Errorf("parseNIP94Event failed: %v", err)
	}
	if url != "https://example.com/package.ipk" {
		t.Errorf("expected URL %s, got %s", "https://example.com/package.ipk", url)
	}
	if version != "1.2.3" {
		t.Errorf("expected version %s, got %s", "1.2.3", version)
	}
	if filename != "package.ipk" {
		t.Errorf("expected filename %s, got %s", "package.ipk", filename)
	}
	if timestamp != 1643723900 {
		t.Errorf("expected timestamp %d, got %d", 1643723900, timestamp)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name           string
		newVersion     string
		newTimestamp   int64
		currentVersion string
		currentTimestamp int64
		expected       bool
	}{
		{
			name:           "newer version and newer timestamp",
			newVersion:     "1.0.1",
			newTimestamp:   2,
			currentVersion: "1.0.0",
			currentTimestamp: 1,
			expected:       true,
		},
		{
			name:           "same version but newer timestamp",
			newVersion:     "1.0.0",
			newTimestamp:   2,
			currentVersion: "1.0.0",
			currentTimestamp: 1,
			expected:       false,
		},
		{
			name:           "newer version but older timestamp",
			newVersion:     "1.0.1",
			newTimestamp:   1,
			currentVersion: "1.0.0",
			currentTimestamp: 2,
			expected:       false,
		},
		{
			name:           "older version and older timestamp",
			newVersion:     "0.9.9",
			newTimestamp:   1,
			currentVersion: "1.0.0",
			currentTimestamp: 2,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentVersion, err := version.NewVersion(tt.currentVersion)
			if err != nil {
				t.Errorf("invalid current version: %v", err)
				return
			}
			if got := isNewerVersion(tt.newVersion, tt.newTimestamp, currentVersion, tt.currentTimestamp); got != tt.expected {
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
			packageURL, versionStr, filename, timestamp, err := parseNIP94Event(*event)
			if err != nil {
				t.Errorf("parseNIP94Event failed: %v", err)
			}
			key := fmt.Sprintf("%s-%s", filename, versionStr)
			existingPackageEvent, ok := eventMap[key]
			if ok {
				if timestamp > int64(existingPackageEvent.event.CreatedAt) {
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

func TestDownloadPackage(t *testing.T) {
	config, err := loadJanitorConfig("../../files/etc/tollgate/config.json")
	if err != nil {
		t.Fatal(err)
	}

	janitor, err := NewJanitor(config.Relays, config.TrustedMaintainers, config.PackageInfo.Version, config.PackageInfo.Timestamp, "../../files/etc/tollgate/config.json")
	if err != nil {
		t.Fatal(err)
	}

	var packageURL string
	eventChan := make(chan *nostr.Event)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(eventChan)
		for event := range eventChan {
			packageURL, _, _, _, err = parseNIP94Event(*event)
			if err != nil {
				t.Errorf("parseNIP94Event failed: %v",	err)
			}
			if packageURL != "" {
				return
			}
		}
	}()

	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)
	var subClosers sync.WaitGroup
	for _, relayURL := range janitor.relays {
		subClosers.Add(1)
		go func(relayURL string) {
			defer subClosers.Done()
			relay, err := relayPool.EnsureRelay(relayURL)
			if err != nil {
				t.Logf("Failed to connect to relay %s: %v", relayURL, err)
				return
			}
			sub, err := relay.Subscribe(ctx, []nostr.Filter{
				{
					Kinds: []int{1063}, // NIP-94 event kind
				},
			})
			if err != nil {
				t.Logf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
				return
			}
			for event := range sub.Events {
				eventChan <- event
			}
		}(relayURL)
	}
	go func() {
		subClosers.Wait()
		close(eventChan)
	}()

	if packageURL == "" {
		t.Fatal("No NIP-94 event found with package URL")
	}

	pkg, err := janitor.DownloadPackage(packageURL)
	if err != nil {
		t.Errorf("DownloadPackage failed: %v", err)
	}
	if len(pkg) == 0 {
		t.Errorf("expected non-empty package content")
	}
}

func TestInstallPackage(t *testing.T) {
	janitor := &Janitor{}
	pkg := []byte("package content")
	err := janitor.InstallPackage(pkg)
	if err != nil {
		t.Errorf("InstallPackage failed: %v", err)
		return
	}
}

func TestRunPostInstallScript(t *testing.T) {
	runPostInstallScript("../../files/etc/tollgate/config.json")
	// Add assertions to verify the config file was updated correctly
}
