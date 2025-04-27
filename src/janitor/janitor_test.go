package main

import (
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
