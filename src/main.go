package main

import (
	"context" // Added for context.Background()
	"encoding/json"
	"fmt"
	"io"
	"net" // Added for net.Interfaces()
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/chandler"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/cli"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/janitor"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/relay"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var mainLogger = logrus.WithField("module", "main")

// Global configuration variable
// Define configFile at a higher scope
var (
	configManager *config_manager.ConfigManager
	mainConfig    *config_manager.Config
	installConfig *config_manager.InstallConfig
)

var gatewayManager *wireless_gateway_manager.GatewayManager

var tollgateDetailsString string
var merchantInstance merchant.MerchantInterface
var cliServer *cli.CLIServer

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

func InitializeGlobalLogger(logLevel string) {
	level, err := logrus.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		// Default to info level if parsing fails
		level = logrus.InfoLevel
		logrus.WithError(err).Warn("Failed to parse log level, defaulting to info")
	}

	logrus.SetLevel(level)

	// Set a consistent formatter for the entire application
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	logrus.WithField("log_level", level.String()).Info("Global logger initialized")
}

func init() {
	var err error

	configPath, installPath, identitiesPath := getTollgatePaths()

	configManager, err = config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create config manager")
	}

	installConfig = configManager.GetInstallConfig()

	gatewayManager, err = wireless_gateway_manager.Init(context.Background(), configManager)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize gateway manager")
	}

	mainConfig = configManager.GetConfig()

	// Initialize global logger with the configured log level
	InitializeGlobalLogger(mainConfig.LogLevel)

	mainLogger.WithField("ip_randomized", installConfig.IPAddressRandomized).Info("Configuration loaded")

	var err2 error
	merchantInstance, err2 = merchant.New(configManager)
	if err2 != nil {
		mainLogger.WithError(err2).Fatal("Failed to create merchant")
	}

	merchantInstance.StartPayoutRoutine()

	// Initialize CLI server
	initCLIServer()

	// Initialize janitor module
	// initJanitor()

	// Initialize private relay
	initPrivateRelay()

	// Initialize crowsnest module
	initCrowsnest()
}

func initJanitor() {
	janitorInstance, err := janitor.NewJanitor(configManager)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create janitor instance")
	}

	go janitorInstance.ListenForNIP94Events()
	mainLogger.Info("Janitor module initialized and listening for NIP-94 events")
}

func initPrivateRelay() {
	go startPrivateRelayWithAutoRestart()
	mainLogger.Info("Private relay initialization started")
}

func initCrowsnest() {
	crowsnestInstance, err := crowsnest.NewCrowsnest(configManager)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create crowsnest instance")
	}

	// Create and set chandler instance
	chandlerInstance, err := chandler.NewChandler(configManager, merchantInstance)
	crowsnestInstance.SetChandler(chandlerInstance)

	go func() {
		err := crowsnestInstance.Start()
		if err != nil {
			mainLogger.WithError(err).Error("Error starting crowsnest")
		}
	}()

	mainLogger.Info("Crowsnest module initialized with chandler and monitoring network changes")
}

func initCLIServer() {
	cliServer = cli.NewCLIServer(configManager, merchantInstance)

	err := cliServer.Start()
	if err != nil {
		mainLogger.WithError(err).Error("Failed to start CLI server")
		return
	}

	mainLogger.Info("CLI server initialized and listening on Unix socket")
}

func startPrivateRelayWithAutoRestart() {
	for {
		mainLogger.Info("Starting TollGate private relay on ws://localhost:4242")

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

		mainLogger.Info("Accepting event kinds: 21000 (Payment), 10021 (Discovery), 1022 (Session), 21023 (Notice)")

		// Start the relay (this blocks until error)
		err := privateRelay.Start(":4242")

		if err != nil {
			mainLogger.WithError(err).Error("Private relay crashed")
			mainLogger.Info("Restarting private relay in 5 seconds...")
			time.Sleep(5 * time.Second)
		} else {
			mainLogger.Info("Private relay stopped normally")
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
		mainLogger.WithFields(logrus.Fields{
			"method":      r.Method,
			"remote_addr": r.RemoteAddr,
		}).Debug("CORS middleware processing request")

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
		mainLogger.WithError(err).Error("Error getting MAC address")
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
	mainLogger.WithFields(logrus.Fields{
		"method":      r.Method,
		"remote_addr": r.RemoteAddr,
	}).Info("Received handleRootPost request")
	// Only process POST requests
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		mainLogger.WithError(err).Error("Error reading request body")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Error reading request body: %v", err), "")
		return
	}
	defer r.Body.Close()

	// Print the request body to console
	bodyStr := string(body)
	mainLogger.WithField("body", bodyStr).Debug("Received POST request")

	// Parse the request body as a nostr event
	var event nostr.Event
	err = json.Unmarshal(body, &event)
	if err != nil {
		mainLogger.WithError(err).Error("Error parsing nostr event")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Error parsing nostr event: %v", err), "")
		return
	}

	// Verify the event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		mainLogger.WithError(err).Error("Invalid signature for nostr event")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Invalid signature for nostr event"), event.PubKey)
		return
	}

	mainLogger.WithFields(logrus.Fields{
		"event_id":   event.ID,
		"created_at": event.CreatedAt,
		"kind":       event.Kind,
		"pubkey":     event.PubKey,
	}).Info("Parsed nostr event")

	// Validate that this is a payment event (kind 21000)
	if event.Kind != 21000 {
		mainLogger.WithField("kind", event.Kind).Error("Invalid event kind, expected 21000")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
			fmt.Sprintf("Invalid event kind: %d, expected 21000", event.Kind), event.PubKey)
		return
	}

	// Process payment and get session event
	responseEvent, err := merchantInstance.PurchaseSession(event)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		mainLogger.WithError(err).Error("Payment processing failed")
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
		mainLogger.WithError(err).Error("Error encoding session response")
	}

}

// sendNoticeResponse creates and sends a notice event response
func sendNoticeResponse(w http.ResponseWriter, merchantInstance merchant.MerchantInterface, statusCode int, level, code, message, customerPubkey string) {
	noticeEvent, err := merchantInstance.CreateNoticeEvent(level, code, message, customerPubkey)
	if err != nil {
		mainLogger.WithError(err).Error("Error creating notice event")
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

	mainLogger.Info("Registering handlers...")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit / endpoint")

		CorsMiddleware(HandleRoot)(w, r)
	})

	http.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /whoami endpoint")
		CorsMiddleware(handler)(w, r)
	})

	mainLogger.Info("Starting HTTP server on all interfaces...")
	server := &http.Server{
		Addr: port,
		// Add explicit timeouts to avoid potential deadlocks in Go 1.24
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	mainLogger.Fatal(server.ListenAndServe())

	go func() {
		for {
			if !isOnline() {
				mainLogger.Info("Device is offline. Initiating gateway scan...")
				// No need to assign the result of RunPeriodicScan, as it runs in a goroutine internally.
				// We need to fetch the available gateways using GetAvailableGateways() instead.
				availableGateways, err := gatewayManager.GetAvailableGateways()
				if err != nil {
					mainLogger.WithError(err).Error("Error getting available gateways")
					continue
				}
				if len(availableGateways) > 0 {
					mainLogger.Info("Available gateways found. Attempting to connect...")
					err = gatewayManager.ConnectToGateway(availableGateways[0].BSSID, "") // Correct usage of ConnectToGateway
					if err != nil {
						mainLogger.WithError(err).Error("Error connecting to gateway")
					} else {
						mainLogger.Info("Successfully connected to a TollGate gateway.")
					}
				} else {
					mainLogger.Info("No suitable TollGate gateways found to connect to.")
				}
			} else {
				mainLogger.Debug("Device is online. No action needed.")
			}
			time.Sleep(5 * time.Minute)
		}
	}()

	fmt.Println("Shutting down Tollgate - Whoami")
}

// isOnline checks if the device has at least one active, non-loopback network interface with an IP address.
func isOnline() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		mainLogger.WithError(err).Error("Error getting network interfaces")
		return false
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			// Interface is up and not a loopback interface
			addrs, err := iface.Addrs()
			if err != nil {
				mainLogger.WithFields(logrus.Fields{
					"interface": iface.Name,
					"error":     err,
				}).Error("Error getting addresses for interface")
				continue
			}
			if len(addrs) > 0 {
				return true // Found at least one active, non-loopback interface with an IP address
			}
		}
	}
	return false
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
