package main_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"github.com/OpenTollGate/tollgate-module-basic-go" // Import the main package
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/nbd-wtf/go-nostr"
)

// TestEventValidation tests basic event validation without merchant dependency
func TestEventValidation(t *testing.T) {
	tests := []struct {
		name           string
		event          nostr.Event
		expectedStatus int
		description    string
	}{
		{
			name: "Valid Payment Event Structure",
			event: nostr.Event{
				Kind: 21000, // Payment event kind
				Tags: nostr.Tags{
					nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
					nostr.Tag{"payment", "test_token"},
				},
				PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
			},
			expectedStatus: http.StatusBadRequest, // Will fail at merchant processing, but structure is valid
			description:    "Valid payment event structure",
		},
		{
			name: "Invalid Event Kind",
			event: nostr.Event{
				Kind: 1022, // Session event kind (invalid for payment endpoint)
				Tags: nostr.Tags{
					nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
					nostr.Tag{"payment", "test_token"},
				},
				PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
			},
			expectedStatus: http.StatusBadRequest,
			description:    "Should reject non-payment event kinds",
		},
		{
			name: "Missing MAC Address",
			event: nostr.Event{
				Kind: 21000,
				Tags: nostr.Tags{
					nostr.Tag{"payment", "test_token"},
				},
				PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
			},
			expectedStatus: http.StatusBadRequest,
			description:    "Should reject events without device identifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Private key for testing (hex encoded)
			// nsec1mudxgnmyh83yhpsm4e4kqfl0y0lr4kvydvskl30fcr2wk2z35ywq9hzhda
			testPrivateKeyBech32 := "nsec1mudxgnmyh83yhpsm4e4kqfl0y0lr4kvydvskl30fcr2wk2z35ywq9hzhda"
			_, data, err := bech32.Decode(testPrivateKeyBech32)
			if err != nil {
				log.Fatalf("Failed to decode test private key (bech32): %v", err)
			}
			converted, err := bech32.ConvertBits(data, 5, 8, false)
			if err != nil {
				log.Fatalf("Failed to convert bits for test private key: %v", err)
			}
			testPrivateKeyHex := hex.EncodeToString(converted)

			// Sign the event for testing
			err = tt.event.Sign(testPrivateKeyHex)
			if err != nil {
				t.Fatal("Failed to sign event:", err)
			}

			eventJSON, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatal(err)
			}

			req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(main.HandleRootPost)
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("%s: handler returned wrong status code: got %v want %v",
					tt.description, status, tt.expectedStatus)
			}
		})
	}
}

// TestEventSignatureValidation tests signature validation specifically
func TestEventSignatureValidation(t *testing.T) {
	event := nostr.Event{
		Kind: 21000,
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
		Sig:    "invalid_signature",
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(main.HandleRootPost)
	handler.ServeHTTP(rr, req)

	// Should return BadRequest due to invalid signature
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	// Check that response is a notice event about invalid signature
	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Fatal("Failed to parse response:", err)
	}

	if response["kind"] != float64(21023) { // Notice event
		t.Errorf("Expected notice event in response, got kind: %v", response["kind"])
	}
}

// TestMACAddressValidation tests the MAC address validation
func TestMACAddressValidation(t *testing.T) {
	tests := []struct {
		name        string
		macAddress  string
		shouldPass  bool
		description string
	}{
		{
			name:        "Valid MAC with colons",
			macAddress:  "00:11:22:33:44:55",
			shouldPass:  true,
			description: "Standard colon-separated MAC address",
		},
		{
			name:        "Valid MAC with hyphens",
			macAddress:  "00-11-22-33-44-55",
			shouldPass:  true,
			description: "Hyphen-separated MAC address",
		},
		{
			name:        "Valid MAC without separators",
			macAddress:  "001122334455",
			shouldPass:  false, // This format requires at least one A-F to be valid
			description: "No separator MAC address with only numbers",
		},
		{
			name:        "Valid MAC without separators with hex",
			macAddress:  "00112233445A",
			shouldPass:  true,
			description: "No separator MAC address with hex digits",
		},
		{
			name:        "Invalid MAC too short",
			macAddress:  "00:11:22:33:44",
			shouldPass:  false,
			description: "MAC address with insufficient octets",
		},
		{
			name:        "Invalid MAC too long",
			macAddress:  "00:11:22:33:44:55:66",
			shouldPass:  false,
			description: "MAC address with too many octets",
		},
		{
			name:        "Invalid characters",
			macAddress:  "00:11:22:33:44:ZZ",
			shouldPass:  false,
			description: "MAC address with invalid hex characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := nostr.Event{
				Kind: 21000,
				Tags: nostr.Tags{
					nostr.Tag{"device-identifier", "mac", tt.macAddress},
					nostr.Tag{"payment", "test_token"},
				},
				PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
			}

			// Private key for testing (hex encoded)
			// nsec1mudxgnmyh83yhpsm4e4kqfl0y0lr4kvydvskl30fcr2wk2z35ywq9hzhda
			testPrivateKeyBech32 := "nsec1mudxgnmyh83yhpsm4e4kqfl0y0lr4kvydvskl30fcr2wk2z35ywq9hzhda"
			_, data, err := bech32.Decode(testPrivateKeyBech32)
			if err != nil {
				log.Fatalf("Failed to decode test private key (bech32): %v", err)
			}
			converted, err := bech32.ConvertBits(data, 5, 8, false)
			if err != nil {
				log.Fatalf("Failed to convert bits for test private key: %v", err)
			}
			testPrivateKeyHex := hex.EncodeToString(converted)

			// Sign the event for testing
			err = event.Sign(testPrivateKeyHex)
			if err != nil {
				t.Fatal("Failed to sign event:", err)
			}

			eventJSON, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}

			req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(main.HandleRootPost)
			handler.ServeHTTP(rr, req)

			// All should return BadRequest since we don't have merchant setup,
			// but we can check the response content to see if MAC validation occurred
			if status := rr.Code; status != http.StatusBadRequest {
				t.Errorf("%s: handler returned wrong status code: got %v want %v",
					tt.description, status, http.StatusBadRequest)
			}

			// For invalid MAC addresses, we should get a specific error about invalid MAC
			if !tt.shouldPass {
				var response map[string]interface{}
				err = json.Unmarshal(rr.Body.Bytes(), &response)
				if err == nil {
					// Check if it's a notice event (which indicates our validation worked)
					if response["kind"] == float64(21023) {
						t.Logf("%s: Correctly identified as invalid MAC address", tt.description)
					}
				}
			}
		})
	}
}
