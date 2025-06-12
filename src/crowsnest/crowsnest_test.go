package crowsnest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestParseMerchantAdvertisement(t *testing.T) {
	crowsnest := &Crowsnest{}

	// Create a sample advertisement
	event := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", "100", "sat"},
			{"mint", "https://mint.minibits.cash/Bitcoin"},
			{"mint", "https://mint2.nutmix.cash"},
		},
		Content: "",
	}

	// Convert to JSON
	advertisementJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Parse the advertisement
	networkInterface, err := crowsnest.parseMerchantAdvertisement(string(advertisementJSON), "wlan0")
	if err != nil {
		t.Fatalf("Failed to parse advertisement: %v", err)
	}

	// Verify the parsed values
	if networkInterface.Name != "wlan0" {
		t.Errorf("Expected Name='wlan0', got '%s'", networkInterface.Name)
	}
	if !networkInterface.IsTollgate {
		t.Error("Expected IsTollgate=true, got false")
	}
	if networkInterface.Metric != "milliseconds" {
		t.Errorf("Expected Metric='milliseconds', got '%s'", networkInterface.Metric)
	}
	if networkInterface.StepSize != 60000 {
		t.Errorf("Expected StepSize=60000, got %d", networkInterface.StepSize)
	}
	if networkInterface.PricePerStep != 100 {
		t.Errorf("Expected PricePerStep=100, got %d", networkInterface.PricePerStep)
	}
	if len(networkInterface.AcceptedMints) != 2 {
		t.Errorf("Expected 2 AcceptedMints, got %d", len(networkInterface.AcceptedMints))
	}
}

func TestFetchMerchantAdvertisement(t *testing.T) {
	crowsnest := &Crowsnest{}

	// Create a test server
	event := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", "100", "sat"},
			{"mint", "https://mint.minibits.cash/Bitcoin"},
		},
		Content: "",
	}
	eventJSON, _ := json.Marshal(event)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(eventJSON)
	}))
	defer server.Close()

	// Fetch the advertisement
	advertisement, err := crowsnest.fetchMerchantAdvertisement(server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch advertisement: %v", err)
	}

	// Verify the fetched advertisement
	var parsedEvent nostr.Event
	err = json.Unmarshal([]byte(advertisement), &parsedEvent)
	if err != nil {
		t.Fatalf("Failed to unmarshal advertisement: %v", err)
	}

	if parsedEvent.Kind != 10021 {
		t.Errorf("Expected Kind=10021, got %d", parsedEvent.Kind)
	}
}
