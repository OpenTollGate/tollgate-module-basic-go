package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
)

const (
	SocketPath        = "/var/run/tollgate.sock"
	SocketPermissions = 0666
)

var cliLogger = logrus.WithField("module", "cli")

// CLIServer handles Unix socket communication for CLI commands
type CLIServer struct {
	configManager *config_manager.ConfigManager
	merchant      merchant.MerchantInterface
	startTime     time.Time
	listener      net.Listener
	running       bool
}

// NewCLIServer creates a new CLI server instance
func NewCLIServer(configManager *config_manager.ConfigManager, merchant merchant.MerchantInterface) *CLIServer {
	return &CLIServer{
		configManager: configManager,
		merchant:      merchant,
		startTime:     time.Now(),
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
	case "control":
		return s.handleControlCommand(msg.Args, msg.Flags)
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
		return s.handleCashuDrain()
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
func (s *CLIServer) handleCashuDrain() CLIResponse {
	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	// Get accepted mints from merchant
	acceptedMints := s.merchant.GetAcceptedMints()
	if len(acceptedMints) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "No accepted mints configured",
			Timestamp: time.Now(),
		}
	}

	var tokens []CashuToken
	var totalDrained uint64

	// For each mint, check balance and create token if balance > 0
	for _, mint := range acceptedMints {
		balance := s.merchant.GetBalanceByMint(mint.URL)

		if balance == 0 {
			cliLogger.WithField("mint", mint.URL).Debug("Skipping mint with zero balance")
			continue
		}

		// Create payment token for the full balance
		tokenString, err := s.merchant.CreatePaymentToken(mint.URL, balance)
		if err != nil {
			cliLogger.WithFields(logrus.Fields{
				"mint":    mint.URL,
				"balance": balance,
				"error":   err,
			}).Error("Failed to create payment token")

			return CLIResponse{
				Success:   false,
				Error:     fmt.Sprintf("Failed to create token for mint %s: %v", mint.URL, err),
				Timestamp: time.Now(),
			}
		}

		tokens = append(tokens, CashuToken{
			MintURL: mint.URL,
			Balance: balance,
			Token:   tokenString,
		})

		totalDrained += balance

		cliLogger.WithFields(logrus.Fields{
			"mint":    mint.URL,
			"balance": balance,
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

	// Get accepted mints for additional context
	acceptedMints := s.merchant.GetAcceptedMints()

	// Build detailed info including per-mint balances
	mintBalances := make(map[string]uint64)
	for _, mint := range acceptedMints {
		balance := s.merchant.GetBalanceByMint(mint.URL)
		if balance > 0 {
			mintBalances[mint.URL] = balance
		}
	}

	return CLIResponse{
		Success: true,
		Message: fmt.Sprintf("Wallet info - Total: %d sats across %d mints", totalBalance, len(acceptedMints)),
		Data: map[string]interface{}{
			"total_balance": totalBalance,
			"mint_count":    len(acceptedMints),
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
		Version:   "v0.0.4-dev [hardcoded]", // TODO: Get from build info
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
		Success: true,
		Message: "TollGate Core v0.0.4-dev [hardcoded]",
		Data: map[string]interface{}{
			"version":    "v0.0.4-dev [hardcoded]",
			"go_version": runtime.Version(),
			"build_time": "development",
		},
		Timestamp: time.Now(),
	}
}

// handleControlCommand sends remote control commands to another TollGate (TIP-07)
func (s *CLIServer) handleControlCommand(args []string, flags map[string]string) CLIResponse {
	if len(args) < 2 {
		return CLIResponse{
			Success:   false,
			Error:     "Usage: control <command> <tollgate_pubkey> [--args '{\"key\":\"value\"}'] [--device-id <id>] [--timeout <sec>]",
			Timestamp: time.Now(),
		}
	}

	command := args[0]
	tollgatePubkey := args[1]
	
	// Get optional arguments
	argsJSON := flags["args"]
	if argsJSON == "" {
		argsJSON = "{}"
	}
	
	// Get device_id from flags (optional, leave empty for most cases)
	// Note: device_id is optional and may be removed in future protocol versions
	deviceID := flags["device-id"]
	
	timeout := 30
	if timeoutStr, ok := flags["timeout"]; ok {
		fmt.Sscanf(timeoutStr, "%d", &timeout)
	}

	// Get controller identity (use tollgate identity for sending commands)
	identities := s.configManager.GetIdentities()
	if identities == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Identities config not available",
			Timestamp: time.Now(),
		}
	}

	tollgateIdentity, err := identities.GetOwnedIdentity("tollgate")
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get tollgate identity: %v", err),
			Timestamp: time.Now(),
		}
	}

	// Send command and wait for response
	result, err := sendTIP07Command(
		tollgateIdentity.PrivateKey,
		tollgatePubkey,
		command,
		argsJSON,
		deviceID,
		s.configManager.GetConfig().Relays,
		timeout,
	)
	
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Command failed: %v", err),
			Timestamp: time.Now(),
		}
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Command '%s' sent successfully", command),
		Data:      result,
		Timestamp: time.Now(),
	}
}

// sendTIP07Command sends a remote control command per TIP-07
func sendTIP07Command(controllerPrivKey, tollgatePubkey, command, argsJSON, deviceID string, relays []string, timeoutSec int) (map[string]interface{}, error) {
	const (
		CommandEventKind  = 21024
		ResponseEventKind = 21025
	)

	ctx := context.Background()

	// Get controller public key
	controllerPubkey, err := nostr.GetPublicKey(controllerPrivKey)
	if err != nil {
		return nil, fmt.Errorf("invalid controller private key: %w", err)
	}

	// Generate unique nonce
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())

	// Create command event
	tags := nostr.Tags{
		{"p", tollgatePubkey},
		{"cmd", command},
		{"nonce", nonce},
	}
	
	// Only include device_id if provided (optional field)
	if deviceID != "" {
		tags = append(tags, nostr.Tag{"device_id", deviceID})
	}
	
	event := nostr.Event{
		PubKey:    controllerPubkey,
		CreatedAt: nostr.Now(),
		Kind:      CommandEventKind,
		Tags:      tags,
		Content: argsJSON,
	}

	// Sign the event
	if err := event.Sign(controllerPrivKey); err != nil {
		return nil, fmt.Errorf("failed to sign event: %w", err)
	}

	cliLogger.WithFields(logrus.Fields{
		"command":   command,
		"to":        tollgatePubkey,
		"event_id":  event.ID,
		"device_id": deviceID,
	}).Info("Sending TIP-07 command")

	// Publish to relays
	publishCount := 0
	for _, relayURL := range relays {
		relay, err := nostr.RelayConnect(ctx, relayURL)
		if err != nil {
			cliLogger.WithError(err).Warnf("Failed to connect to relay %s", relayURL)
			continue
		}
		defer relay.Close()

		if err := relay.Publish(ctx, event); err != nil {
			cliLogger.WithError(err).Warnf("Failed to publish to %s", relayURL)
		} else {
			cliLogger.Infof("Command published to %s", relayURL)
			publishCount++
		}
	}

	if publishCount == 0 {
		return nil, fmt.Errorf("failed to publish to any relay")
	}

	// Listen for response
	responseChan := make(chan map[string]interface{}, 1)
	errorChan := make(chan error, 1)

	go func() {
		for _, relayURL := range relays {
			go func(url string) {
				relay, err := nostr.RelayConnect(ctx, url)
				if err != nil {
					return
				}
				defer relay.Close()

				// Subscribe to response events
				since := nostr.Timestamp(time.Now().Add(-1 * time.Minute).Unix())
				filter := nostr.Filter{
					Kinds: []int{ResponseEventKind},
					Tags: nostr.TagMap{
						"p":           []string{controllerPubkey},
						"in_reply_to": []string{event.ID},
					},
					Since: &since,
				}

				sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
				if err != nil {
					return
				}

				// Wait for response
				timeout := time.After(time.Duration(timeoutSec) * time.Second)
				for {
					select {
					case respEvent := <-sub.Events:
						if respEvent == nil {
							continue
						}

						// Parse response
						var response map[string]interface{}
						if err := json.Unmarshal([]byte(respEvent.Content), &response); err != nil {
							continue
						}

						// Add event metadata
						response["event_id"] = respEvent.ID
						response["from_pubkey"] = respEvent.PubKey

						select {
						case responseChan <- response:
						default:
						}
						return

					case <-timeout:
						return
					}
				}
			}(relayURL)
		}
	}()

	// Wait for response or timeout
	select {
	case response := <-responseChan:
		cliLogger.Info("Received TIP-07 response")
		return response, nil
	case err := <-errorChan:
		return nil, err
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		return nil, fmt.Errorf("timeout waiting for response (%ds)", timeoutSec)
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

// sendError sends an error response to the client
func (s *CLIServer) sendError(conn net.Conn, errorMsg string) {
	response := CLIResponse{
		Success:   false,
		Error:     errorMsg,
		Timestamp: time.Now(),
	}
	s.sendResponse(conn, response)
}
