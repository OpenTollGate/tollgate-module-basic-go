package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	SocketPath = "/var/run/tollgate.sock"
)

// Simple message types to avoid module dependencies
type CLIMessage struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

type CLIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

var rootCmd = &cobra.Command{
	Use:   "tollgate",
	Short: "TollGate CLI - Control your TollGate instance",
	Long: `TollGate CLI provides command-line access to your running TollGate service.
You can check status, manage wallet, and control various aspects of the service.`,
}

var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Wallet operations",
	Long:  "Manage your TollGate wallet - check balance, drain funds, view information",
}

var drainCmd = &cobra.Command{
	Use:   "drain",
	Short: "Drain wallet funds",
	Long:  "Transfer wallet funds using different methods",
}

var drainCashuCmd = &cobra.Command{
	Use:   "cashu",
	Short: "Drain wallet to Cashu tokens",
	Long:  "Create Cashu tokens for each mint containing all available balance",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("wallet", []string{"drain", "cashu"}, nil)
	},
}

var balanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Show wallet balance",
	Long:  "Display current wallet balance in satoshis",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("wallet", []string{"balance"}, nil)
	},
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show wallet information",
	Long:  "Display detailed wallet information including balance, addresses, and keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("wallet", []string{"info"}, nil)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	Long:  "Display TollGate service status including uptime, modules, and health",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("status", []string{}, nil)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  "Display TollGate version and build information",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("version", []string{}, nil)
	},
}

func init() {
	// Build command tree
	drainCmd.AddCommand(drainCashuCmd)
	walletCmd.AddCommand(drainCmd, balanceCmd, infoCmd)
	rootCmd.AddCommand(walletCmd, statusCmd, versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func sendCommandAndDisplay(command string, args []string, flags map[string]string) error {
	// Create CLI message
	msg := CLIMessage{
		Command:   command,
		Args:      args,
		Flags:     flags,
		Timestamp: time.Now(),
	}

	// Send command to service
	response, err := sendCommand(msg)
	if err != nil {
		return fmt.Errorf("failed to communicate with TollGate service: %v\nMake sure the TollGate service is running", err)
	}

	// Display response
	displayResponse(response)

	if !response.Success {
		return fmt.Errorf("command failed")
	}

	return nil
}

func sendCommand(msg CLIMessage) (*CLIResponse, error) {
	// Connect to Unix socket
	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to TollGate service: %v", err)
	}
	defer conn.Close()

	// Send message
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode message: %v", err)
	}

	_, err = conn.Write(data)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %v", err)
	}

	_, err = conn.Write([]byte("\n"))
	if err != nil {
		return nil, fmt.Errorf("failed to send newline: %v", err)
	}

	// Read response
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return nil, fmt.Errorf("no response from service")
	}

	var response CLIResponse
	err = json.Unmarshal(scanner.Bytes(), &response)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	return &response, nil
}

func displayResponse(response *CLIResponse) {
	if response.Success {
		if response.Message != "" {
			fmt.Println(response.Message)
		}

		if response.Data != nil {
			displayData(response.Data)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", response.Error)
	}
}

func displayData(data interface{}) {
	switch v := data.(type) {
	case map[string]interface{}:
		// Check if this is a WalletDrainResult
		if _, ok := v["tokens"]; ok {
			displayWalletDrainResult(v)
		} else {
			displayMap(v, "")
		}
	default:
		// Fallback to JSON pretty print
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err == nil {
			fmt.Println(string(jsonData))
		}
	}
}

func displayWalletDrainResult(data map[string]interface{}) {
	fmt.Println("\nWallet Drain Results:")
	fmt.Println("====================")

	if total, ok := data["total_sats"].(float64); ok {
		fmt.Printf("Total drained: %.0f sats\n\n", total)
	}

	if tokensData, ok := data["tokens"].([]interface{}); ok {
		if len(tokensData) == 0 {
			fmt.Println("No tokens created (all balances are zero)")
			return
		}

		for i, tokenData := range tokensData {
			if tokenMap, ok := tokenData.(map[string]interface{}); ok {
				fmt.Printf("Token %d:\n", i+1)
				if mintURL, ok := tokenMap["mint_url"].(string); ok {
					fmt.Printf("  Mint: %s\n", mintURL)
				}
				if balance, ok := tokenMap["balance_sats"].(float64); ok {
					fmt.Printf("  Balance: %.0f sats\n", balance)
				}
				if token, ok := tokenMap["token"].(string); ok {
					// Print full token - user needs complete token to spend
					fmt.Printf("  Token: %s\n", token)
				}
				fmt.Println()
			}
		}
	}
}

func displayMap(m map[string]interface{}, prefix string) {
	for key, value := range m {
		switch v := value.(type) {
		case string:
			fmt.Printf("%s%s: %s\n", prefix, key, v)
		case int, int64, uint64, float64:
			fmt.Printf("%s%s: %v\n", prefix, key, v)
		case bool:
			fmt.Printf("%s%s: %v\n", prefix, key, v)
		case map[string]interface{}:
			fmt.Printf("%s%s:\n", prefix, key)
			displayMap(v, prefix+"  ")
		default:
			if strings.Contains(fmt.Sprintf("%T", v), "slice") {
				fmt.Printf("%s%s: %v\n", prefix, key, v)
			} else {
				fmt.Printf("%s%s: %v\n", prefix, key, v)
			}
		}
	}
}
