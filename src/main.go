package main

import (
	"context" // Added for context.Background()
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net" // Added for net.Interfaces()
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/cli"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
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
	merchantInstance.StartDataUsageMonitoring()

	// Initialize CLI server
	initCLIServer()

	// Initialize upstream detector module
	initUpstreamDetector()
}

func initUpstreamDetector() {
	upstreamDetectorInstance, err := upstream_detector.NewUpstreamDetector(configManager)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create upstream detector instance")
	}

	// Create and set upstream session manager instance
	usmInstance, err := upstream_session_manager.NewUpstreamSessionManager(configManager, merchantInstance)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create upstream session manager instance")
	}
	upstreamDetectorInstance.SetUpstreamSessionManager(usmInstance)

	go func() {
		err := upstreamDetectorInstance.Start()
		if err != nil {
			mainLogger.WithError(err).Error("Error starting upstream detector")
		}
	}()

	mainLogger.Info("UpstreamDetector module initialized with upstream session manager and monitoring network changes")
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

func getMacAddress(ipAddress string) (string, error) {
	if net.ParseIP(ipAddress) == nil {
		return "", fmt.Errorf("invalid IP address: %s", ipAddress)
	}
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

	// Get MAC address from request
	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		mainLogger.WithError(err).Error("Error getting MAC address")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "mac-address-lookup-failed",
			fmt.Sprintf("Failed to lookup MAC address for IP %s: %v", ip, err), "")
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		mainLogger.WithError(err).Error("Error reading request body")
		sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-request",
			fmt.Sprintf("Error reading request body: %v", err), macAddress)
		return
	}
	defer r.Body.Close()

	// Print the request body to console
	bodyStr := string(body)
	mainLogger.WithField("body", bodyStr).Debug("Received POST request")

	var cashuToken string

	// Try to parse as JSON (Nostr event format)
	var event nostr.Event
	err = json.Unmarshal(body, &event)

	if err == nil && event.Kind == 21000 {
		// It's a valid Nostr event (signature validation is now optional)
		mainLogger.WithFields(logrus.Fields{
			"event_id":   event.ID,
			"created_at": event.CreatedAt,
			"kind":       event.Kind,
			"pubkey":     event.PubKey,
		}).Info("Parsed nostr event (signature not validated)")

		// Extract payment token from event
		var paymentToken string
		for _, tag := range event.Tags {
			if len(tag) >= 2 && tag[0] == "payment" {
				paymentToken = tag[1]
				break
			}
		}

		if paymentToken == "" {
			mainLogger.Error("No payment tag found in event")
			sendNoticeResponse(w, merchantInstance, http.StatusBadRequest, "error", "invalid-event",
				"No payment tag found in event", macAddress)
			return
		}

		cashuToken = paymentToken
	} else {
		// Treat as plain Cashu token string
		mainLogger.Info("Treating request as plain Cashu token string")
		cashuToken = strings.TrimSpace(bodyStr)
	}

	// Process payment with cashu token and MAC address
	responseEvent, err := merchantInstance.PurchaseSession(cashuToken, macAddress)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		mainLogger.WithError(err).Error("Payment processing failed")
		sendNoticeResponse(w, merchantInstance, http.StatusInternalServerError, "error", "internal-error",
			fmt.Sprintf("Internal error during payment processing: %v", err), macAddress)
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

type lightningInvoiceRequest struct {
	Amount  uint64 `json:"amount"`
	MintURL string `json:"mint_url"`
	Mint    string `json:"mint"`
}

type lightningInvoiceResponse struct {
	Status        int    `json:"status"`
	Quote         string `json:"quote"`
	Invoice       string `json:"invoice,omitempty"`
	MintURL       string `json:"mint_url"`
	Amount        uint64 `json:"amount"`
	Expiry        uint64 `json:"expiry,omitempty"`
	State         string `json:"state"`
	AccessGranted bool   `json:"access_granted"`
	Allotment     uint64 `json:"allotment,omitempty"`
	Metric        string `json:"metric,omitempty"`
	Error         string `json:"error,omitempty"`
}

type balanceResponse struct {
	Status        int    `json:"status"`
	SessionActive bool   `json:"session_active"`
	Metric        string `json:"metric,omitempty"`
	Usage         uint64 `json:"usage"`
	Allotment     uint64 `json:"allotment"`
	Remaining     uint64 `json:"remaining"`
	StartTime     int64  `json:"start_time,omitempty"`
	Error         string `json:"error,omitempty"`
}

func parseUsageString(usage string) (uint64, uint64, error) {
	parts := strings.Split(strings.TrimSpace(usage), "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid usage format: %s", usage)
	}

	used, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid usage value: %w", err)
	}

	allotment, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid allotment value: %w", err)
	}

	return used, allotment, nil
}

func HandleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		mainLogger.WithError(err).Error("Error getting MAC address for /balance")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(balanceResponse{Status: 0, Error: "failed to resolve device MAC address"})
		return
	}

	usage, err := merchantInstance.GetUsage(macAddress)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{"mac": macAddress, "error": err}).Error("Error getting balance usage")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(balanceResponse{Status: 0, Error: err.Error()})
		return
	}
	if usage == "-1/-1" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balanceResponse{Status: 1, SessionActive: false})
		return
	}

	used, allotment, err := parseUsageString(usage)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{"mac": macAddress, "usage": usage, "error": err}).Error("Error parsing usage string")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(balanceResponse{Status: 0, Error: err.Error()})
		return
	}

	session, err := merchantInstance.GetSession(macAddress)
	if err != nil || session == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balanceResponse{Status: 1, SessionActive: false})
		return
	}

	remaining := uint64(0)
	if allotment > used {
		remaining = allotment - used
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(balanceResponse{
		Status:        1,
		SessionActive: true,
		Metric:        session.Metric,
		Usage:         used,
		Allotment:     allotment,
		Remaining:     remaining,
		StartTime:     session.StartTime,
	})
}

func HandleLightningInvoice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleLightningInvoicePost(w, r)
	case http.MethodGet:
		handleLightningInvoiceGet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleLightningInvoicePost(w http.ResponseWriter, r *http.Request) {
	var req lightningInvoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "invalid request body"})
		return
	}

	mintURL := strings.TrimSpace(req.MintURL)
	if mintURL == "" {
		mintURL = strings.TrimSpace(req.Mint)
	}
	if req.Amount == 0 || mintURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "amount and mint_url are required"})
		return
	}

	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		mainLogger.WithError(err).Error("Error getting MAC address for lightning invoice")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "failed to resolve device MAC address"})
		return
	}

	invoice, err := merchantInstance.RequestLightningInvoice(macAddress, mintURL, req.Amount)
	if err != nil {
		mainLogger.WithError(err).Warn("Failed to create lightning invoice")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(lightningInvoiceResponse{
		Status:        1,
		Quote:         invoice.QuoteID,
		Invoice:       invoice.Invoice,
		MintURL:       invoice.MintURL,
		Amount:        invoice.Amount,
		Expiry:        invoice.Expiry,
		State:         invoice.State,
		AccessGranted: false,
	})
}

func handleLightningInvoiceGet(w http.ResponseWriter, r *http.Request) {
	quoteID := strings.TrimSpace(r.URL.Query().Get("quote"))
	if quoteID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "quote is required"})
		return
	}

	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		mainLogger.WithError(err).Error("Error getting MAC address for lightning status")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "failed to resolve device MAC address"})
		return
	}

	// Quotes are bound to the device MAC at invoice creation time. Polling only
	// reveals status for that same device and access is granted to the recorded MAC.
	status, err := merchantInstance.GetLightningInvoiceStatus(quoteID, macAddress)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, merchant.ErrQuoteNotFound) {
			statusCode = http.StatusNotFound
		}
		mainLogger.WithError(err).Warn("Failed to fetch lightning invoice status")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(lightningInvoiceResponse{
		Status:        1,
		Quote:         status.QuoteID,
		MintURL:       status.MintURL,
		Amount:        status.Amount,
		State:         status.State,
		AccessGranted: status.AccessGranted,
		Allotment:     status.Allotment,
		Metric:        status.Metric,
	})
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

	http.HandleFunc("/ln-invoice", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /ln-invoice endpoint")
		CorsMiddleware(HandleLightningInvoice)(w, r)
	})

	http.HandleFunc("/balance", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /balance endpoint")
		CorsMiddleware(HandleBalance)(w, r)
	})

	http.HandleFunc("/usage", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /usage endpoint")

		// Get MAC address from request
		ip := getIP(r)
		macAddress, err := getMacAddress(ip)
		if err != nil {
			mainLogger.WithError(err).Error("Error getting MAC address for /usage")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "-1/-1")
			return
		}

		// Get usage from merchant
		usageStr, err := merchantInstance.GetUsage(macAddress)
		if err != nil {
			mainLogger.WithFields(logrus.Fields{
				"mac":   macAddress,
				"error": err,
			}).Error("Error getting usage")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "-1/-1")
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, usageStr)
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

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func getIP(r *http.Request) string {
	if isLocalRequest(r) {
		ip := r.Header.Get("X-Real-Ip")
		if ip != "" {
			return strings.TrimSpace(ip)
		}

		ips := r.Header.Get("X-Forwarded-For")
		if ips != "" {
			return strings.TrimSpace(strings.Split(ips, ",")[0])
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
