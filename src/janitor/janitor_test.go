package main

import (
 "testing"

 "github.com/nbd-wtf/go-nostr"
)

func TestParseNIP94Event(t *testing.T) {
 event := nostr.Event{
 Tags: []nostr.Tag{
 {"url", "https://example.com/package.ipk"},
 {"version", "1.2.3"},
 },
 CreatedAt: 1643723900,
 }

 url, version, timestamp, err := parseNIP94Event(event)
 if err != nil {
 t.Errorf("parseNIP94Event failed: %v", err)
 }
 if url != "https://example.com/package.ipk" {
 t.Errorf("expected URL %s, got %s", "https://example.com/package.ipk", url)
 }
 if version != "1.2.3" {
 t.Errorf("expected version %s, got %s", "1.2.3", version)
 }
 if timestamp != 1643723900 {
 t.Errorf("expected timestamp %d, got %d", 1643723900, timestamp)
 }
}

func TestIsNewerVersion(t *testing.T) {
 // implement test cases for isNewerVersion function
}