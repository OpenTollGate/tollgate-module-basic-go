package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/janitor"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/relay"
	"github.com/nbd-wtf/go-nostr"
)

// Global configuration variable
// Define configFile at a higher scope
var (
	configManager *config_manager.ConfigManager
	mainConfig    *config_manager.Config
	installConfig *config_manager.InstallConfig
)
var tollgateDetailsString string
var merchantInstance *merchant.Merchant

// getTollgatePaths returns the configuration file paths based on the environment.
// If TOLLGATE_TEST_CONFIG_DIR is set, it uses paths within that directory for testing.
// Otherwise, it defaults to /etc/tollgate.
func getTollgatePaths() (configPath, installPath, identitiesPath string) {
	if testDir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); testDir != "" {
		configPath = filepath.Join(testDir, "config.json")
		installPath = filepath.Join(testDir, "install.json")
		identitiesPath = filepath.Join(testDir, "identities.json")
		return
	}
	// Default paths for production
	configPath = "/etc/tollgate/config.json"
	installPath = "/etc/tollgate/install.json"
	identitiesPath = "/etc/tollgate/identities.json"
	return
}

func init() {
	var err error

	configPath, installPath, identitiesPath := getTollgatePaths()

	configManager, err = config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		log.Fatalf("Failed to create config manager: %v", err)
	}

	installConfig = configManager.GetInstallConfig()

	mainConfig = configManager.GetConfig()

	IPAddressRandomized := fmt.Sprintf("%t", installConfig.IPAddressRandomized)
	log.Printf("IPAddressRandomized: %s", IPAddressRandomized)

	var err2 error
	merchantInstance, err2 = merchant.New(configManager)
	if err2 != nil {
		log.Fatalf("Failed to create merchant: %v", err2)
	}

	if err != nil {
		log.Fatalf("Failed to create merchant: %v", err)
	}

	merchantInstance.StartPayoutRoutine()

	// Initialize janitor module
	initJanitor()

	// Initialize private relay
	initPrivateRelay()
}

func initJanitor() {
	janitorInstance, err := janitor.NewJanitor(configManager)
	if err != nil {
		log.Fatalf("Failed to create janitor instance: %v", err)
	}

	go janitorInstance.ListenForNIP94Events()
	log.Println("Janitor module initialized and listening for NIP-94 events")
}

func initPrivateRelay() {
	go startPrivateRelayWithAutoRestart()
	log.Println("Private relay initialization started")
}

func startPrivateRelayWithAutoRestart() {
	for {
		log.Println("Starting TollGate private relay on ws://localhost:4242")

		// Create a new private relay instance
		privateRelay := relay.NewPrivateRelay()

		// Set up relay metadata
		privateRelay.GetRelay().Info.Name = "TollGate Private Relay"
		privateRelay.GetRelay().Info.Description = "In-memory relay for TollGate protocol events (kinds 21000-21023)"
		privateRelay.GetRelay().Info.PubKey = ""
		privateRelay.GetRelay().Info.Contact = ""
		privateRelay.GetRelay().Info.SupportedNIPs = []any{1, 11}
		privateRelay.GetRelay().Info.Software = "https://github.com/OpenTollGate/tollgate-module-basic-go"
		privateRelay.GetRelay().Info.Version = "v0.0.1"

		log.Printf("Accepting event kinds: 21000 (Payment), 10021 (Discovery), 1022 (Session), 21023 (Notice)")

		// Start the relay (this blocks until error)
		err := privateRelay.Start(":4242")

		if err != nil {
			log.Printf("Private relay crashed: %v", err)
			log.Println("Restarting private relay in 5 seconds...")
			time.Sleep(5 * time.Second)
		} else {
			log.Println("Private relay stopped normally")
			break
		}
	}
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
func CorsMiddleware(next http.HandlerFunc) http.HandlerFunc {
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
	fmt.Fprint(w, merchantInstance.GetAdvertisement())
}

// handleRootPost handles POST requests to the root endpoint
func HandleRootPost(w http.ResponseWriter, r *http.Request) {
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
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Error reading request body: %v", err), "")
		return
	}
	defer r.Body.Close()

	// Print the request body to console
	bodyStr := string(body)
	log.Printf("Received POST request with body: %s", bodyStr)

	// Parse the request body as a nostr event
	var event nostr.Event
	err = json.Unmarshal(body, &event)
	if err != nil {
		log.Println("Error parsing nostr event:", err)
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Error parsing nostr event: %v", err), "")
		return
	}

	// Verify the event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		log.Println("Invalid signature for nostr event:", err)
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Invalid signature for nostr event"), event.PubKey)
		return
	}

	log.Println("Parsed nostr event:", event.ID)
	log.Println("  - Created at:", event.CreatedAt)
	log.Println("  - Kind:", event.Kind)
	log.Println("  - Pubkey:", event.PubKey)

	// Validate that this is a payment event (kind 21000)
	if event.Kind != 21000 {
		log.Printf("Invalid event kind: %d, expected 21000", event.Kind)
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Invalid event kind: %d, expected 21000", event.Kind), event.PubKey)
		return
	}

	// Process payment and get session event
	responseEvent, err := merchantInstance.PurchaseSession(event)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		log.Printf("Payment processing failed: %v", err)
		sendNoticeResponse(w, merchantInstance, http.StatusInternalServerError, "error", "internal-error",
			fmt.Sprintf("Internal error during payment processing: %v", err), event.PubKey)
		return
	}

	// Check if the response is a notice event (kind 21023) or session event (kind 1022)
	if responseEvent.Kind == 21023 {
		// It's a notice event (error case), return with appropriate status
		w.WriteHeader(http.StatusBadRequest)
		err = json.NewEncoder(w).Encode(responseEvent)
	} else {
		// It's a session event (success case), return with OK status
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(responseEvent)
	}

	if err != nil {
		log.Printf("Error encoding session response: %v", err)
	}

}

// sendNoticeResponse creates and sends a notice event response
func sendNoticeResponse(w http.ResponseWriter, merchantInstance *merchant.Merchant, statusCode int, level, code, message, customerPubkey string) {
	noticeEvent, err := merchantInstance.CreateNoticeEvent(level, code, message, customerPubkey)
	if err != nil {
		log.Printf("Error creating notice event: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(noticeEvent)
}

// handleRoot routes requests based on method
func HandleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		HandleRootPost(w, r)
	} else {
		handleDetails(w, r)
	}
}

func main() {
	var port = ":2121" // Change from "0.0.0.0:2121" to just ":2121"
	fmt.Println("Starting Tollgate Core")
	fmt.Println("Listening on all interfaces on port", port)

	// Add verbose logging for debugging
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Registering handlers...")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("DEBUG: Hit / endpoint from %s", r.RemoteAddr)
		CorsMiddleware(HandleRoot)(w, r)
	})

	http.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("DEBUG: Hit /whoami endpoint from %s", r.RemoteAddr)
		CorsMiddleware(handler)(w, r)
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
