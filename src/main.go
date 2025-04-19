package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/OpenTollgate/tollgate-module-basic-go/src/modules"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// Config structure to hold all configuration parameters
type Config struct {
TollgatePrivateKey string `json:"tollgate_private_key"`
AcceptedMint string `json:"accepted_mint"`
PricePerMinute int `json:"price_per_minute"`
MinPayment int `json:"min_payment"`
MintFee int `json:"mint_fee"`
Relays []string `json:"relays"`
Bragging BraggingConfig `json:"bragging"`
}

type BraggingConfig struct {
Enabled bool `json:"enabled"`
Relays []string `json:"relays"`
Fields []string `json:"fields"`
}

// Global configuration variable
var config Config

// Derived configuration values
var tollgatePrivateKey string
var acceptedMint string
var pricePerMinute int
var minPayment int
var mintFee int
var cutoffFee int

var tollgateDetailsEvent nostr.Event
var tollgateDetailsString string

// Initialize the nostr pool for Cashu operations
var relayPool *nostr.SimplePool

func init() {
	// Load configuration
	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create the nostr event
	tollgateDetailsEvent = nostr.Event{
		Kind: 21021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", fmt.Sprintf("%d", pricePerMinute), "sat"},
			{"mint", acceptedMint},
			{"tips", "1", "2", "3"},
		},
		Content: "",
	}

	// Override the existing signature with a newly generated one
	err := tollgateDetailsEvent.Sign(tollgatePrivateKey)
	if err != nil {
		log.Fatalf("Failed to sign tollgate event: %v", err)
	}

	// Convert to JSON string for storage
	detailsBytes, err := json.Marshal(tollgateDetailsEvent)
	if err != nil {
		log.Fatalf("Failed to marshal tollgate event: %v", err)
	}
	tollgateDetailsString = string(detailsBytes)

	// Initialize relay pool for NIP-60 operations
	relayPool = nostr.NewSimplePool(context.Background())
}

// loadConfig reads configuration from /etc/tollgate/config.json
func loadConfig() error {
	configFile := "/etc/tollgate/config.json"

	// Read the existing config file
	data, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Create a default config if it doesn't exist
			config := Config{
				TollgatePrivateKey: "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
				AcceptedMint:       "",
				PricePerMinute:     1,
				MinPayment:         1,
				MintFee:            1,
			}
			defaultConfig, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal default config: %v", err)
			}
			err = os.MkdirAll("/etc/tollgate", 0755)
			if err != nil {
				log.Printf("WARNING: Failed to create config directory: %v", err)
			}
			err = os.WriteFile(configFile, defaultConfig, 0644)
			if err != nil {
				log.Printf("WARNING: Failed to write default config file: %v", err)
			}
			data = defaultConfig
		} else {
			return fmt.Errorf("failed to read config file: %v", err)
		}
	}

	// Parse the config file
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config file: %v", err)
	}

	log.Printf("Relays loaded from config: %v", config.Relays)

	// Update global variables
	tollgatePrivateKey = config.TollgatePrivateKey
	acceptedMint = config.AcceptedMint
	pricePerMinute = config.PricePerMinute
	minPayment = config.MinPayment
	mintFee = config.MintFee
	cutoffFee = 2*mintFee + minPayment

	log.Printf("Configuration loaded: mint=%s, price=%d, fee=%d",
		acceptedMint, pricePerMinute, mintFee)

	return nil
}

func getMacAddress(ipAddress string) (string, error) {
	cmdIn := `cat /tmp/dhcp.leases | cut -f 2,3,4 -s -d" " | grep -i ` + ipAddress + ` | cut -f 1 -s -d" "`
	commandOutput, err := exec.Command("sh", "-c", cmdIn).Output()

	var commandOutputString = string(commandOutput)
	if err != nil {
		fmt.Println(err, "Error when getting client's mac address. Command output: "+commandOutputString)
		return "nil", err
	}

	return strings.Trim(commandOutputString, "\n"), nil
}

// CORS middleware to handle Cross-Origin Resource Sharing
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("cors middleware %s request from %s", r.Method, r.RemoteAddr)

		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*") // Allow any origin, or specify domains like "https://yourdomain.com"
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle preflight OPTIONS requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Call the next handler
		next(w, r)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	var ip = getIP(r)
	var mac, err = getMacAddress(ip)

	if err != nil {
		log.Println("Error getting MAC address:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	fmt.Println("mac", mac)
	fmt.Fprint(w, "mac=", mac)
}

func handleDetails(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Details requested")
	fmt.Fprint(w, tollgateDetailsString)
}

// handleRootPost handles POST requests to the root endpoint
func handleRootPost(w http.ResponseWriter, r *http.Request) {
	// Log the request details
	log.Printf("Received handleRootPost %s request from %s", r.Method, r.RemoteAddr)
	// Only process POST requests
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Println("Error reading request body:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Print the request body to console
	bodyStr := string(body)
	log.Println("Received POST request with body:", bodyStr)

	// Parse the request body as a nostr event
	var event nostr.Event
	err = json.Unmarshal(body, &event)
	if err != nil {
		log.Println("Error parsing nostr event:", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		log.Println("Invalid signature for nostr event:", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Println("Parsed nostr event:", event.ID)
	log.Println("  - Created at:", event.CreatedAt)
	log.Println("  - Kind:", event.Kind)
	log.Println("  - Pubkey:", event.PubKey)

	// Extract MAC address from device-identifier tag
	var macAddress string
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "device-identifier" && len(tag) >= 3 {
			macAddress = tag[2]
			break
		}
	}
	if macAddress == "" {
		log.Println("Missing or invalid device-identifier tag")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extract payment token from payment tag
	var paymentToken string
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "payment" && len(tag) >= 2 {
			paymentToken = tag[1]
			break
		}
	}
	if paymentToken == "" {
		log.Println("Missing or invalid payment tag")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("Extracted MAC address: %s", macAddress)
	log.Printf("Extracted payment token: %s", paymentToken)

	// Decode the Cashu token
	tokenValue, err := decodeCashuToken(paymentToken)
	if err != nil {
		log.Printf("Error decoding Cashu token: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the token has sufficient value before redeeming it
	if tokenValue < cutoffFee {
		log.Printf("Token value too low (%d sats). Minimum %d sats required.", tokenValue, cutoffFee)
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprintf(w, "Payment required. Token value too low (%d sats). Minimum %d sats required.", tokenValue, cutoffFee)
		return
	}

	// Process and swap the token for fresh proofs - only if value is sufficient
	relays := config.Relays
	log.Printf("Relays being passed to CollectPayment: %v", relays)
	swapError := CollectPayment(paymentToken, tollgatePrivateKey, relayPool, relays)
	if swapError != nil {
		log.Printf("Error swapping token: %v", swapError)
		w.WriteHeader(http.StatusPaymentRequired)
		return
		// We can still continue with the token we have
	} else {
		log.Printf("Successfully swapped token for fresh proofs")
	}

	// Calculate the actual value after deducting fees
	// First mint fee for the payment and second fee for consolidation transaction
	var valueAfterFees = tokenValue - 2*mintFee
	if valueAfterFees < minPayment {
		log.Printf("ValueAfterFees: Token value too low (%d sats). Minimum %d sats required.", valueAfterFees, minPayment)
		w.WriteHeader(http.StatusPaymentRequired)
		return // Not enough value to open the gate
		// This should have been caught by the token value check above
	}

	// Calculate minutes based on the net value
	// TODO: Update frontend to show the correct duration after fees
	//       Already tested to verify that allottedMinutes is correct
	var allottedMinutes = int64(valueAfterFees / pricePerMinute)
	if allottedMinutes < 1 {
		allottedMinutes = 1 // Minimum 1 minute
	}

	// Convert to seconds for gate opening
	durationSeconds := int64(allottedMinutes * 60)

	// Log the calculation for transparency
	log.Printf("Calculated minutes: %d (from value %d, minus fees %d)", 
		allottedMinutes, tokenValue, 2*mintFee)

	// Open gate for the specified duration using the valve module
	err = modules.OpenGate(macAddress, durationSeconds)
	if err != nil {
		log.Printf("Error opening gate: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Announce successful payment via Nostr if enabled
	err = announceSuccessfulPayment(macAddress, int64(valueAfterFees), durationSeconds)
	if err != nil {
	    log.Printf("Error announcing successful payment: %v", err)
	}

	// Return a success status with token info
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Access granted for %d minutes (payment: %d sats, fees: %d sats)",
	    allottedMinutes, valueAfterFees, 2*mintFee)
}

func announceSuccessfulPayment(macAddress string, amount int64, durationSeconds int64) error {
    if !config.Bragging.Enabled {
        log.Println("Bragging is disabled in configuration")
        return nil
    }

    privateKey := tollgatePrivateKey
    event := nostr.Event{
        Kind:      1,
        CreatedAt: nostr.Now(),
        Tags:      make(nostr.Tags, 0),
        Content:   "",
    }

    var content string
    for _, field := range config.Bragging.Fields {
        switch field {
        case "amount":
            event.Tags = append(event.Tags, nostr.Tag{"amount", fmt.Sprintf("%d", amount)})
            content += fmt.Sprintf("Amount: %d sats,\n", amount)
        case "mint":
            event.Tags = append(event.Tags, nostr.Tag{"mint", acceptedMint})
            content += fmt.Sprintf("Mint: %s,\n", acceptedMint)
        case "duration":
            event.Tags = append(event.Tags, nostr.Tag{"duration", fmt.Sprintf("%d", durationSeconds)})
            content += fmt.Sprintf("Duration: %d seconds", durationSeconds)
        }
    }

    // Trim the trailing comma and space if content is not empty
    if content != "" {
        content = strings.TrimSuffix(content, ",")
        content += "\n\n#BraggingTollGateRawData"
    }

    event.Content = content

    pubkey, err := nostr.GetPublicKey(privateKey)
    if err != nil {
    	log.Printf("Failed to get public key: %v", err)
    	return err
    }
    npub, err := nip19.EncodePublicKey(pubkey)
    if err != nil {
    	log.Printf("Failed to encode public key to npub: %v", err)
    	return err
    }
    log.Printf("Encoded public key to npub: %s", npub)
    log.Printf("Attempting to sign bragging event")
    err = event.Sign(privateKey)
    if err != nil {
    	log.Printf("Failed to sign bragging event: %v", err)
    	return err
    }
    log.Printf("Successfully signed bragging event")
    log.Printf("Bragging event ID: %s", event.ID)
    log.Printf("Bragging npub: %s", npub)


    log.Printf("Initializing relay pool for bragging event publication")
    log.Printf("Relays configured for bragging: %v", config.Relays)
    relayPool := nostr.NewSimplePool(context.Background())
    for _, relayURL := range config.Relays {
        relay, err := relayPool.EnsureRelay(relayURL)
        if err != nil {
            log.Printf("Failed to connect to relay %s: %v", relayURL, err)
            continue
        }
        err = relay.Publish(context.Background(), event)
        if err != nil {
            log.Printf("Failed to publish event to relay %s: %v", relayURL, err)
        } else {
            log.Printf("Successfully published event to relay %s", relayURL)
        }
    }

    if err != nil {
        return err
    }

    log.Printf("Successfully announced payment for MAC %s", macAddress)
    return nil
}

// handleRoot routes requests based on method
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleRootPost(w, r)
	} else {
		handleDetails(w, r)
	}
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("Received %s request from %s to %s\n", r.Method, getIP(r), r.URL.Path)
}

func main() {
	var port = ":2121" // Change from "0.0.0.0:2121" to just ":2121"
	fmt.Println("Starting Tollgate - TIP-01")
	fmt.Println("Listening on all interfaces on port", port)

	// Add verbose logging for debugging
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Registering handlers...")

	http.HandleFunc("/x", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("DEBUG: Hit /x endpoint from %s", r.RemoteAddr)
		testHandler(w, r)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("DEBUG: Hit / endpoint from %s", r.RemoteAddr)
		corsMiddleware(handleRoot)(w, r)
	})

	http.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("DEBUG: Hit /whoami endpoint from %s", r.RemoteAddr)
		corsMiddleware(handler)(w, r)
	})

	log.Println("Starting HTTP server on all interfaces...")
	server := &http.Server{
		Addr: port,
		// Add explicit timeouts to avoid potential deadlocks in Go 1.24
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Fatal(server.ListenAndServe())

	fmt.Println("Shutting down Tollgate - Whoami")
}

func getIP(r *http.Request) string {
	// Check if the IP is set in the X-Real-Ip header
	ip := r.Header.Get("X-Real-Ip")
	if ip != "" {
		return ip
	}

	// Check if the IP is set in the X-Forwarded-For header
	ips := r.Header.Get("X-Forwarded-For")
	if ips != "" {
		return strings.Split(ips, ",")[0]
	}

	// Fallback to the remote address, removing the port
	ip = r.RemoteAddr
	if colon := strings.LastIndex(ip, ":"); colon != -1 {
		ip = ip[:colon]
	}

	return ip
}
