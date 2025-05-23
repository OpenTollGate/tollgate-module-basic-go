package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/bragging"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/janitor"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// Global configuration variable
// Define configFile at a higher scope
var configManager *config_manager.ConfigManager
var tollgateDetailsString string
var merchantInstance *merchant.Merchant

func init() {
	var err error

	configManager, err = config_manager.NewConfigManager("/etc/tollgate/config.json")
	if err != nil {
		log.Fatalf("Failed to create config manager: %v", err)
	}

	installConfig, err := configManager.LoadInstallConfig()
	if err != nil {
		log.Printf("Error loading install config: %v", err)
		os.Exit(1)
	}
	mainConfig, err := configManager.LoadConfig()
	if err != nil {
		log.Printf("Error loading config: %v", err)
		os.Exit(1)
	}

	currentInstallationID := mainConfig.CurrentInstallationID
	log.Printf("CurrentInstallationID: %s", currentInstallationID)
	IPAddressRandomized := fmt.Sprintf("%s", installConfig.IPAddressRandomized)
	log.Printf("IPAddressRandomized: %s", IPAddressRandomized)
	if currentInstallationID != "" {
		_, err = configManager.GetNIP94Event(currentInstallationID)
		if err != nil {
			log.Printf("Error getting NIP94 event: %v", err)
			os.Exit(1)
		}
	}

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
}

func initJanitor() {
	janitorInstance, err := janitor.NewJanitor(configManager)
	if err != nil {
		log.Fatalf("Failed to create janitor instance: %v", err)
	}

	go janitorInstance.ListenForNIP94Events()
	log.Println("Janitor module initialized and listening for NIP-94 events")
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
	fmt.Fprint(w, merchantInstance.GetAdvertisement())
}

// handleRootPost handles POST requests to the root endpoint
func handleRootPost(w http.ResponseWriter, r *http.Request) {
	// Log the request details
	fmt.Printf("Received handleRootPost %s request from %s\n", r.Method, r.RemoteAddr)
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

	// Extract payment token from payment tag
	var paymentToken string
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "payment" && len(tag) >= 2 {
			paymentToken = tag[1]
			break
		}
	}

	fmt.Printf("Extracted MAC address: %s\n", macAddress)
	fmt.Printf("Extracted payment token: %s\n", paymentToken)

	purchaseSessionResult, err := merchantInstance.PurchaseSession(paymentToken, macAddress)

	// Set response headers and prepare JSON response
	w.Header().Set("Content-Type", "application/json")

	switch purchaseSessionResult.Status {
	case "success":
		w.WriteHeader(http.StatusOK)
	case "rejected":
		w.WriteHeader(http.StatusPaymentRequired)
	default:
		// Log unexpected errors for easier debugging
		log.Printf("Purchase session failed with status: %s, reason: %s",
			purchaseSessionResult.Status, purchaseSessionResult.Description)
		w.WriteHeader(http.StatusInternalServerError)
	}

	// Return meaningful response to the client with the operation status and reason
	response := map[string]string{"status": purchaseSessionResult.Status}
	if purchaseSessionResult.Description != "" {
		response["reason"] = purchaseSessionResult.Description
	}

	// Handle potential encoding errors
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
	}

	fmt.Fprint(w, response)

}

func announceSuccessfulPayment(macAddress string, amount int64, durationSeconds int64) error {
	mainConfig, err := configManager.LoadConfig()
	if err != nil {
		log.Printf("Error loading config: %v", err)
		return err
	}

	if !mainConfig.Bragging.Enabled {
		log.Println("Bragging is disabled in configuration")
		return nil
	}

	err = bragging.AnnounceSuccessfulPayment(configManager, amount, durationSeconds)
	if err != nil {
		log.Printf("Failed to create bragging service: %v", err)
		return err
	}

	if err != nil {
		return err
	}

	fmt.Printf("Successfully announced payment for MAC %s\n", macAddress)
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
	fmt.Println("Starting Tollgate Core")
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
