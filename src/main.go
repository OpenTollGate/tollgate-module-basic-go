package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/OpenTollgate/tollgate-module-basic-go/src/modules"
	"github.com/nbd-wtf/go-nostr"
)

// Private key for signing the nostr event (in hex format)
// In production, this should be kept secure and not hardcoded
var tollgatePrivateKey string = "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663"
var acceptedMint = "https://mint.minibits.cash/Bitcoin"
var pricePerMinute int = 5

var tollgateDetailsEvent nostr.Event
var tollgateDetailsString string

// Initialize the nostr pool for Cashu operations
var relayPool *nostr.SimplePool

func init() {
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
	fmt.Fprintf(w, "mac="+mac)
}

func handleDetails(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Details requested")
	fmt.Fprintf(w, tollgateDetailsString)
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

	// Process and swap the token for fresh proofs
	swapError := CollectPayment(paymentToken, tollgatePrivateKey, relayPool)
	if swapError != nil {
		log.Printf("Error swapping token: %v", swapError)
		w.WriteHeader(http.StatusPaymentRequired)
		return
		// We can still continue with the token we have
	} else {
		log.Printf("Successfully swapped token for fresh proofs")
	}

	var allottedMinutes = tokenValue / pricePerMinute

	// Calculate durationSeconds based on token value (1 minute per sat)
	durationSeconds := int64(allottedMinutes * 60) // convert to seconds
	if durationSeconds < 60 {
		durationSeconds = 60 // minimum 1 minute
	}

	// Open gate for the specified duration using the valve module
	err = modules.OpenGate(macAddress, durationSeconds)
	if err != nil {
		log.Printf("Error opening gate: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Return a success status with token info
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Access granted for %d minutes", durationSeconds/60)
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
