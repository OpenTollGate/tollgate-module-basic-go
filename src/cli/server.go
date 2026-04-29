package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/sirupsen/logrus"
)

const (
	SocketPath        = "/var/run/tollgate.sock"
	SocketPermissions = 0666
)

var cliLogger = logrus.WithField("module", "cli")

// CLIServer handles Unix socket communication for CLI commands
type CLIServer struct {
	configManager   *config_manager.ConfigManager
	merchant        merchant.MerchantInterface
	gatewayManager  *wireless_gateway_manager.GatewayManager
	startTime       time.Time
	listener        net.Listener
	running         bool
}

func NewCLIServer(configManager *config_manager.ConfigManager, merchant merchant.MerchantInterface, gatewayManager *wireless_gateway_manager.GatewayManager) *CLIServer {
	return &CLIServer{
		configManager:  configManager,
		merchant:       merchant,
		gatewayManager: gatewayManager,
		startTime:      time.Now(),
	}
}

// Start begins listening on the Unix socket
func (s *CLIServer) Start() error {
	// Remove existing socket file if it exists
	if err := os.Remove(SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing socket: %v", err)
	}

	// Create Unix socket listener
	listener, err := net.Listen("unix", SocketPath)
	if err != nil {
		return fmt.Errorf("failed to create Unix socket: %v", err)
	}

	// Set socket permissions so CLI can access it
	if err := os.Chmod(SocketPath, SocketPermissions); err != nil {
		listener.Close()
		return fmt.Errorf("failed to set socket permissions: %v", err)
	}

	s.listener = listener
	s.running = true

	cliLogger.WithField("socket_path", SocketPath).Info("CLI server started")

	// Accept connections in a goroutine
	go s.acceptConnections()

	return nil
}

// Stop shuts down the CLI server
func (s *CLIServer) Stop() error {
	if !s.running {
		return nil
	}

	s.running = false

	if s.listener != nil {
		s.listener.Close()
	}

	// Clean up socket file
	os.Remove(SocketPath)

	cliLogger.Info("CLI server stopped")
	return nil
}

// acceptConnections handles incoming connections
func (s *CLIServer) acceptConnections() {
	for s.running {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.running {
				cliLogger.WithError(err).Error("Failed to accept connection")
			}
			continue
		}

		go s.handleConnection(conn)
	}
}

// handleConnection processes a single CLI connection
func (s *CLIServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Use a buffered reader with larger buffer to handle long cashu tokens
	reader := bufio.NewReaderSize(conn, 8192) // 8KB buffer

	// Read until newline (our protocol sends data + \n)
	data, err := reader.ReadBytes('\n')
	if err != nil {
		cliLogger.WithError(err).Error("Failed to read from connection")
		return
	}

	// Remove the trailing newline
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	cliLogger.WithField("data_length", len(data)).Debug("Received CLI message")

	var msg CLIMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		cliLogger.WithError(err).Error("Failed to unmarshal CLI message")
		s.sendError(conn, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	if msg.Command == "upstream" && len(msg.Args) >= 2 && msg.Args[0] == "connect" {
		s.handleUpstreamConnectStreaming(conn, msg)
		return
	}

	response := s.processCommand(msg)
	s.sendResponse(conn, response)
}

// processCommand executes the CLI command and returns a response
func (s *CLIServer) processCommand(msg CLIMessage) CLIResponse {
	cliLogger.WithFields(logrus.Fields{
		"command": msg.Command,
		"args":    msg.Args,
	}).Debug("Processing CLI command")

	switch msg.Command {
	case "wallet":
		return s.handleWalletCommand(msg.Args, msg.Flags)
	case "network":
		return s.handleNetworkCommand(msg.Args, msg.Flags)
	case "upstream":
		return s.handleUpstreamCommand(msg.Args, msg.Flags)
	case "status":
		return s.handleStatusCommand(msg.Args, msg.Flags)
	case "version":
		return s.handleVersionCommand()
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown command: %s", msg.Command),
			Timestamp: time.Now(),
		}
	}
}

// handleWalletCommand processes wallet-related commands
func (s *CLIServer) handleWalletCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Wallet command requires an action (drain, balance, info)",
			Timestamp: time.Now(),
		}
	}

	action := args[0]
	switch action {
	case "drain":
		return s.handleWalletDrain(args[1:], flags)
	case "balance":
		return s.handleWalletBalance()
	case "info":
		return s.handleWalletInfo()
	case "fund":
		return s.handleWalletFund(args[1:], flags)
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown wallet action: %s (supported: drain, balance, info, fund)", action),
			Timestamp: time.Now(),
		}
	}
}

// handleWalletDrain processes the wallet drain command
func (s *CLIServer) handleWalletDrain(drainArgs []string, flags map[string]string) CLIResponse {
	if len(drainArgs) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Drain command requires a type: 'cashu' (lightning not yet supported)",
			Timestamp: time.Now(),
		}
	}

	drainType := drainArgs[0]
	switch drainType {
	case "cashu":
		return s.handleCashuDrain(flags)
	case "lightning":
		return CLIResponse{
			Success:   false,
			Error:     "Lightning drain not yet implemented",
			Timestamp: time.Now(),
		}
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown drain type: %s (supported: cashu)", drainType),
			Timestamp: time.Now(),
		}
	}
}

// handleCashuDrain drains all wallet balances to Cashu tokens for each mint
func (s *CLIServer) handleCashuDrain(flags map[string]string) CLIResponse {
	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	// Get ALL mints from the wallet (not just configured accepted mints)
	// This ensures we can drain funds even from mints that are no longer configured
	allMintBalances := s.merchant.GetAllMintBalances()
	if len(allMintBalances) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "No mints found in wallet",
			Timestamp: time.Now(),
		}
	}

	var tokens []CashuToken
	var totalDrained uint64

	// For each mint in the wallet, drain if balance > 0
	for mintURL, balance := range allMintBalances {

		if balance == 0 {
			cliLogger.WithField("mint", mintURL).Debug("Skipping mint with zero balance")
			continue
		}

		// Use DrainMint instead of CreatePaymentToken to avoid fee-related issues
		// DrainMint extracts all available balance without trying to add fees
		tokenString, actualAmount, err := s.merchant.DrainMint(mintURL)
		if err != nil {
			cliLogger.WithFields(logrus.Fields{
				"mint":    mintURL,
				"balance": balance,
				"error":   err,
			}).Error("Failed to drain mint")

			return CLIResponse{
				Success:   false,
				Error:     fmt.Sprintf("Failed to drain mint %s: %v", mintURL, err),
				Timestamp: time.Now(),
			}
		}

		tokens = append(tokens, CashuToken{
			MintURL: mintURL,
			Balance: actualAmount,
			Token:   tokenString,
		})

		totalDrained += actualAmount

		cliLogger.WithFields(logrus.Fields{
			"mint":    mintURL,
			"balance": actualAmount,
		}).Info("Created drain token")
	}

	if len(tokens) == 0 {
		return CLIResponse{
			Success: true,
			Message: "No tokens to drain - all mint balances are zero",
			Data: WalletDrainResult{
				Success: true,
				Tokens:  []CashuToken{},
				Total:   0,
			},
			Timestamp: time.Now(),
		}
	}

	result := WalletDrainResult{
		Success: true,
		Tokens:  tokens,
		Total:   totalDrained,
	}

	// Include filename in result if requested - client will handle saving
	if filename, ok := flags["save_to_file"]; ok && filename != "" {
		return CLIResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully drained %d sats from %d mints", totalDrained, len(tokens)),
			Data: map[string]interface{}{
				"tokens":       tokens,
				"total_sats":   totalDrained,
				"save_to_file": filename,
			},
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Successfully drained %d sats from %d mints", totalDrained, len(tokens)),
		Data:      result,
		Timestamp: time.Now(),
	}
}

// handleWalletBalance returns the current wallet balance
func (s *CLIServer) handleWalletBalance() CLIResponse {
	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	// Get total wallet balance from merchant
	totalBalance := s.merchant.GetBalance()

	return CLIResponse{
		Success: true,
		Message: fmt.Sprintf("Total wallet balance: %d sats", totalBalance),
		Data: WalletInfo{
			Balance: totalBalance,
		},
		Timestamp: time.Now(),
	}
}

// handleWalletInfo returns detailed wallet information
func (s *CLIServer) handleWalletInfo() CLIResponse {
	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	// Get total wallet balance from merchant
	totalBalance := s.merchant.GetBalance()

	// Get ALL mints from the wallet (not just configured accepted mints)
	// This shows all mints that have funds, even if they're no longer configured
	allMintBalances := s.merchant.GetAllMintBalances()

	// Filter to only show mints with non-zero balances
	// Convert to map[string]interface{} for proper JSON serialization
	mintBalances := make(map[string]interface{})
	for mintURL, balance := range allMintBalances {
		if balance > 0 {
			mintBalances[mintURL] = balance
		}
	}

	return CLIResponse{
		Success: true,
		Message: fmt.Sprintf("Wallet info - Total: %d sats across %d mints", totalBalance, len(mintBalances)),
		Data: map[string]interface{}{
			"total_balance": totalBalance,
			"mint_count":    len(mintBalances),
			"mint_balances": mintBalances,
		},
		Timestamp: time.Now(),
	}
}

// handleWalletFund processes the wallet fund command
func (s *CLIServer) handleWalletFund(fundArgs []string, flags map[string]string) CLIResponse {
	if len(fundArgs) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Fund command requires a cashu token argument",
			Timestamp: time.Now(),
		}
	}

	cashuToken := fundArgs[0]
	if cashuToken == "" {
		return CLIResponse{
			Success:   false,
			Error:     "Cashu token cannot be empty",
			Timestamp: time.Now(),
		}
	}

	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	// Fund the wallet using the merchant interface
	cliLogger.WithField("token_length", len(cashuToken)).Debug("Attempting to fund wallet")

	amountReceived, err := s.merchant.Fund(cashuToken)
	if err != nil {
		cliLogger.WithError(err).Error("Failed to fund wallet via merchant")
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to fund wallet: %v", err),
			Timestamp: time.Now(),
		}
	}

	cliLogger.WithField("amount", amountReceived).Info("Successfully funded wallet")

	return CLIResponse{
		Success: true,
		Message: fmt.Sprintf("Successfully funded wallet with %d sats", amountReceived),
		Data: map[string]interface{}{
			"amount_received": amountReceived,
		},
		Timestamp: time.Now(),
	}
}

// handleStatusCommand returns service status
func (s *CLIServer) handleStatusCommand(args []string, flags map[string]string) CLIResponse {
	uptime := time.Since(s.startTime)

	status := ServiceStatus{
		Running:   true,
		Version:   GetVersionInfo(),
		Uptime:    uptime.String(),
		ConfigOK:  s.configManager != nil,
		WalletOK:  s.merchant != nil,
		NetworkOK: true, // TODO: Check actual network status
	}

	return CLIResponse{
		Success:   true,
		Message:   "Service status retrieved",
		Data:      status,
		Timestamp: time.Now(),
	}
}

// handleVersionCommand returns version information
func (s *CLIServer) handleVersionCommand() CLIResponse {
	return CLIResponse{
		Success:   true,
		Message:   GetFormattedVersionInfo(),
		Timestamp: time.Now(),
	}
}

// sendResponse sends a CLIResponse back to the client
func (s *CLIServer) sendResponse(conn net.Conn, response CLIResponse) {
	data, err := json.Marshal(response)
	if err != nil {
		cliLogger.WithError(err).Error("Failed to marshal response")
		return
	}

	conn.Write(data)
	conn.Write([]byte("\n"))
}

func (s *CLIServer) sendProgress(conn net.Conn, step, message string) {
	s.sendResponse(conn, CLIResponse{
		Progress:  step + " " + message,
		Timestamp: time.Now(),
	})
}

// sendError sends an error response to the client
func (s *CLIServer) sendError(conn net.Conn, errorMsg string) {
	response := CLIResponse{
		Success:   false,
		Error:     errorMsg,
		Timestamp: time.Now(),
	}
	s.sendResponse(conn, response)
}

func (s *CLIServer) handleUpstreamCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "Upstream command requires a subcommand (scan, connect, list, remove)",
			Timestamp: time.Now(),
		}
	}

	if s.gatewayManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Gateway manager not available",
			Timestamp: time.Now(),
		}
	}

	subcommand := args[0]
	switch subcommand {
	case "scan":
		return s.handleUpstreamScan()
	case "connect":
		return s.handleUpstreamConnect(args[1:])
	case "list-upstream":
		return s.handleUpstreamList()
	case "remove-upstream":
		if len(args) < 2 {
			return CLIResponse{
				Success:   false,
				Error:     "remove-upstream requires an SSID argument",
				Timestamp: time.Now(),
			}
		}
		return s.handleUpstreamRemove(args[1])
	default:
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Unknown upstream subcommand: %s (supported: scan, connect, list-upstream, remove-upstream)", subcommand),
			Timestamp: time.Now(),
		}
	}
}

func (s *CLIServer) handleUpstreamScan() CLIResponse {
	networks, err := s.gatewayManager.ScanAllRadios()
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to scan networks: %v", err),
			Timestamp: time.Now(),
		}
	}

	var result []UpstreamNetwork
	for _, net := range networks {
		result = append(result, UpstreamNetwork{
			SSID:       net.SSID,
			Signal:     net.Signal,
			Encryption: net.Encryption,
			BSSID:      net.BSSID,
			Radio:      net.Radio,
		})
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Found %d network(s)", len(result)),
		Data:      result,
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleUpstreamConnect(connectArgs []string) CLIResponse {
	if len(connectArgs) < 1 {
		return CLIResponse{
			Success:   false,
			Error:     "connect requires an SSID argument",
			Timestamp: time.Now(),
		}
	}

	ssid := connectArgs[0]
	var passphrase string
	if len(connectArgs) > 1 {
		passphrase = connectArgs[1]
	}

	if err := s.gatewayManager.EnsureRadiosEnabled(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to enable radios: %v", err),
			Timestamp: time.Now(),
		}
	}

	networks, err := s.gatewayManager.ScanAllRadios()
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to scan networks: %v", err),
			Timestamp: time.Now(),
		}
	}

	bestRadio, err := s.gatewayManager.FindBestRadioForSSID(ssid, networks)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("SSID '%s' not found in scan", ssid),
			Timestamp: time.Now(),
		}
	}

	activeSTA, _ := s.gatewayManager.GetActiveSTA()
	if activeSTA != nil {
		activeRadio, _ := s.gatewayManager.GetSTADevice(activeSTA.Name)
		if activeRadio == bestRadio {
			altRadio, altErr := s.gatewayManager.FindAlternateRadioForSSID(ssid, bestRadio, networks)
			if altErr == nil {
				bestRadio = altRadio
			}
		}
	}

	var encryptionStr string
	for _, net := range networks {
		if net.SSID == ssid {
			encryptionStr = net.Encryption
			break
		}
	}

	uciEnc := s.gatewayManager.DetectEncryption(encryptionStr)

	if uciEnc == "none" {
		passphrase = ""
	} else if passphrase == "" {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Passphrase required for encrypted network '%s'", ssid),
			Timestamp: time.Now(),
		}
	}

	if err := s.gatewayManager.EnsureWWANSetup(); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to setup wwan: %v", err),
			Timestamp: time.Now(),
		}
	}

	ifaceName, err := s.gatewayManager.FindOrCreateSTAForSSID(ssid, passphrase, uciEnc, bestRadio)
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to create STA interface: %v", err),
			Timestamp: time.Now(),
		}
	}

	activeIface := ""
	if activeSTA != nil {
		activeIface = activeSTA.Name
	}

	if err := s.gatewayManager.SwitchUpstream(activeIface, ifaceName, ssid); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to connect: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Connected to '%s'", ssid),
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleUpstreamConnectStreaming(conn net.Conn, msg CLIMessage) {
	connectArgs := msg.Args[1:]

	if len(connectArgs) < 1 {
		s.sendResponse(conn, CLIResponse{Success: false, Error: "connect requires an SSID argument", Timestamp: time.Now()})
		return
	}

	ssid := connectArgs[0]
	var passphrase string
	if len(connectArgs) > 1 {
		passphrase = connectArgs[1]
	}

	totalSteps := 7
	step := 0

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), "Enabling radios...")
	if err := s.gatewayManager.EnsureRadiosEnabled(); err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Failed to enable radios: %v", err), Timestamp: time.Now()})
		return
	}

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), fmt.Sprintf("Scanning for '%s'...", ssid))
	networks, err := s.gatewayManager.ScanAllRadios()
	if err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Failed to scan networks: %v", err), Timestamp: time.Now()})
		return
	}

	bestRadio, err := s.gatewayManager.FindBestRadioForSSID(ssid, networks)
	if err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("SSID '%s' not found in scan", ssid), Timestamp: time.Now()})
		return
	}

	var signalStr string
	for _, net := range networks {
		if net.SSID == ssid && net.Radio == bestRadio {
			signalStr = fmt.Sprintf(" (%d dBm on %s)", net.Signal, bestRadio)
			break
		}
	}

	activeSTA, _ := s.gatewayManager.GetActiveSTA()
	if activeSTA != nil {
		activeRadio, _ := s.gatewayManager.GetSTADevice(activeSTA.Name)
		if activeRadio == bestRadio {
			altRadio, altErr := s.gatewayManager.FindAlternateRadioForSSID(ssid, bestRadio, networks)
			if altErr == nil {
				s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps),
					fmt.Sprintf("Using alternate radio %s (avoiding active STA on %s)", altRadio, bestRadio))
				bestRadio = altRadio
				for _, net := range networks {
					if net.SSID == ssid && net.Radio == bestRadio {
						signalStr = fmt.Sprintf(" (%d dBm on %s)", net.Signal, bestRadio)
						break
					}
				}
			}
		}
	}

	var encryptionStr string
	for _, net := range networks {
		if net.SSID == ssid {
			encryptionStr = net.Encryption
			break
		}
	}
	uciEnc := s.gatewayManager.DetectEncryption(encryptionStr)

	if uciEnc == "none" {
		passphrase = ""
	} else if passphrase == "" {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Passphrase required for encrypted network '%s'", ssid), Timestamp: time.Now()})
		return
	}

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), fmt.Sprintf("Found '%s'%s encryption=%s", ssid, signalStr, uciEnc))

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), "Setting up wwan interface...")
	if err := s.gatewayManager.EnsureWWANSetup(); err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Failed to setup wwan: %v", err), Timestamp: time.Now()})
		return
	}

	ifaceName := "upstream_" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToLower(ssid))
	if len(ifaceName) > 40 {
		ifaceName = ifaceName[:40]
	}

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), fmt.Sprintf("Creating STA %s on %s...", ifaceName, bestRadio))
	staIface, err := s.gatewayManager.FindOrCreateSTAForSSID(ssid, passphrase, uciEnc, bestRadio)
	if err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Failed to create STA interface: %v", err), Timestamp: time.Now()})
		return
	}

	activeIface := ""
	if activeSTA != nil {
		activeIface = activeSTA.Name
	}

	crossInfo := ""
	if activeIface != "" {
		activeRadio, _ := s.gatewayManager.GetSTADevice(activeIface)
		if activeRadio != "" && activeRadio != bestRadio {
			crossInfo = fmt.Sprintf(" (cross-radio: %s -> %s)", activeRadio, bestRadio)
		}
	}

	step++
	s.sendProgress(conn, fmt.Sprintf("[%d/%d]", step, totalSteps), fmt.Sprintf("Switching upstream%s... waiting for DHCP", crossInfo))
	if err := s.gatewayManager.SwitchUpstream(activeIface, staIface, ssid); err != nil {
		s.sendResponse(conn, CLIResponse{Success: false, Error: fmt.Sprintf("Failed to connect: %v", err), Timestamp: time.Now()})
		return
	}

	step++
	s.sendResponse(conn, CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Connected to '%s' via %s%s", ssid, staIface, signalStr),
		Timestamp: time.Now(),
	})
}

func (s *CLIServer) handleUpstreamList() CLIResponse {
	sections, err := s.gatewayManager.GetSTASections()
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to list upstreams: %v", err),
			Timestamp: time.Now(),
		}
	}

	var result []UpstreamSTA
	for _, section := range sections {
		status := "disabled"
		if !section.Disabled {
			status = "ACTIVE"
		}
		result = append(result, UpstreamSTA{
			SSID:       section.SSID,
			Status:     status,
			Radio:      section.Device,
			Encryption: section.Encryption,
		})
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("%d upstream STA(s) configured", len(result)),
		Data:      result,
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleUpstreamRemove(ssid string) CLIResponse {
	if err := s.gatewayManager.RemoveDisabledSTA(ssid); err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to remove upstream: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Removed upstream '%s'", ssid),
		Timestamp: time.Now(),
	}
}
