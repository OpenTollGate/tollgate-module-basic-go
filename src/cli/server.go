package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
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
	case "network":
		return s.handleNetworkCommand(msg.Args, msg.Flags)
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

// sendError sends an error response to the client
func (s *CLIServer) sendError(conn net.Conn, errorMsg string) {
	response := CLIResponse{
		Success:   false,
		Error:     errorMsg,
		Timestamp: time.Now(),
	}
	s.sendResponse(conn, response)
}
