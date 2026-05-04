package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	case "config":
		return s.handleConfigCommand(msg.Args, msg.Flags)
	case "health":
		return s.handleHealthCommand()
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

func isTestnutMint(url string) bool {
	return strings.Contains(strings.ToLower(url), "testnut")
}

func (s *CLIServer) handleCashuDrain(flags map[string]string) CLIResponse {
	if s.merchant == nil {
		return CLIResponse{
			Success:   false,
			Error:     "Merchant not available",
			Timestamp: time.Now(),
		}
	}

	allMintBalances := s.merchant.GetAllMintBalances()
	if len(allMintBalances) == 0 {
		return CLIResponse{
			Success:   false,
			Error:     "No mints found in wallet",
			Timestamp: time.Now(),
		}
	}

	var nonTestnut []string
	for mintURL, balance := range allMintBalances {
		if balance > 0 && !isTestnutMint(mintURL) {
			nonTestnut = append(nonTestnut, fmt.Sprintf("%s (%d sats)", mintURL, balance))
		}
	}
	if len(nonTestnut) > 0 {
		return CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Cannot drain: non-testnut mint(s) have balance: %s. Drain only works reliably with testnut mints.", strings.Join(nonTestnut, ", ")),
			Timestamp: time.Now(),
		}
	}

	var tokens []CashuToken
	var totalDrained uint64

	for mintURL, balance := range allMintBalances {
		if balance == 0 {
			cliLogger.WithField("mint", mintURL).Debug("Skipping mint with zero balance")
			continue
		}

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

	var lines []string
	lines = append(lines, fmt.Sprintf("TollGate Wallet Drain - %s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC")))
	lines = append(lines, fmt.Sprintf("Total: %d sats from %d mint(s)", totalDrained, len(tokens)))
	lines = append(lines, "")
	for i, t := range tokens {
		lines = append(lines, fmt.Sprintf("Mint %d: %s (%d sats)", i+1, t.MintURL, t.Balance))
		lines = append(lines, t.Token)
		lines = append(lines, "")
	}

	filename := fmt.Sprintf("tollgate-drain-%s.txt", time.Now().UTC().Format("20060102-150405"))
	filePath := filepath.Join("/root", filename)
	if err := os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		cliLogger.WithError(err).Error("Failed to write drain file")
	} else {
		cliLogger.WithField("path", filePath).Info("Drain tokens saved")
	}

	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Drained %d sats from %d mint(s). Tokens saved to %s", totalDrained, len(tokens), filePath),
		Data: map[string]interface{}{
			"tokens":     tokens,
			"total_sats": totalDrained,
			"saved_to":   filePath,
		},
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
		Data:      GetFullVersionInfo(),
		Timestamp: time.Now(),
	}
}

func (s *CLIServer) handleHealthCommand() CLIResponse {
	health := map[string]interface{}{
		"status":     "ok",
		"version":    GetVersionInfo(),
		"config_ok":  s.configManager != nil,
		"wallet_ok":  s.merchant != nil,
		"uptime":     time.Since(s.startTime).String(),
	}
	return CLIResponse{
		Success:   true,
		Message:   "healthy",
		Data:      health,
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
