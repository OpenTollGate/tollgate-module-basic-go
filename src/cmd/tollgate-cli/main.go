package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func askConfirmation(message string) bool {
	fmt.Printf("%s (y/N): ", message)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return response == "y" || response == "yes"
}

const (
	SocketPath = "/var/run/tollgate.sock"
)

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
	Progress  string      `json:"progress,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

var jsonOutput bool

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

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "Network operations",
	Long:  "Manage network settings and configurations",
}

var privateCmd = &cobra.Command{
	Use:   "private",
	Short: "Private network operations",
	Long:  "Manage your private network - enable/disable, rename, change password",
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
		if jsonOutput {
			return sendCommandRaw("wallet", []string{"drain", "cashu"}, nil)
		}

		fmt.Println("\n⚠️  WARNING: Draining the wallet will remove ALL funds from the wallet!")
		fmt.Println("The funds will be converted to Cashu tokens that will be saved to a file.")
		fmt.Println("Once drained, the tokens are OUT of the wallet and must be stored securely.")

		if !askConfirmation("\nAre you sure you want to drain the wallet?") {
			fmt.Println("Operation cancelled.")
			return nil
		}

		filename := fmt.Sprintf("wallet_drain_%s.txt", time.Now().Format("2006-01-02_15-04-05"))

		flags := map[string]string{
			"save_to_file": filename,
		}

		fmt.Printf("\nTokens will be saved to: %s\n\n", filename)
		return sendCommandAndDisplay("wallet", []string{"drain", "cashu"}, flags)
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

var fundCmd = &cobra.Command{
	Use:   "fund [cashu-token]",
	Short: "Fund wallet with a Cashu token",
	Long:  "Add funds to the wallet by providing a Cashu token.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var cashuToken string
		if len(args) == 1 {
			cashuToken = strings.TrimSpace(args[0])
		} else if !jsonOutput {
			fmt.Print("Paste your Cashu token: ")
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return fmt.Errorf("failed to read token input")
			}
			cashuToken = strings.TrimSpace(scanner.Text())
		} else {
			return fmt.Errorf("fund command requires a cashu token argument when using --json")
		}

		if cashuToken == "" {
			return fmt.Errorf("no token provided")
		}

		return sendCommandAndDisplay("wallet", []string{"fund", cashuToken}, nil)
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

var privateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show private network status",
	Long:  "Display private network status including SSID, password, and enabled state",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("network", []string{"private", "status"}, nil)
	},
}

var privateEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable private network",
	Long:  "Enable the private WiFi network on both 2.4GHz and 5GHz radios",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("network", []string{"private", "enable"}, nil)
	},
}

var privateDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable private network",
	Long:  "Disable the private WiFi network on both 2.4GHz and 5GHz radios",
	RunE: func(cmd *cobra.Command, args []string) error {
		if jsonOutput {
			return sendCommandRaw("network", []string{"private", "disable"}, nil)
		}

		fmt.Println("\n⚠️  WARNING: Disabling the private network may lock you out of the router!")
		fmt.Println("Make sure you have another way to access the router (e.g., via the public network or physical access).")

		if !askConfirmation("\nAre you sure you want to disable the private network?") {
			fmt.Println("Operation cancelled.")
			return nil
		}

		return sendCommandAndDisplay("network", []string{"private", "disable"}, nil)
	},
}

var privateRenameCmd = &cobra.Command{
	Use:   "rename [new-ssid]",
	Short: "Rename private network SSID",
	Long:  "Change the SSID of the private WiFi network",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("network", []string{"private", "rename", args[0]}, nil)
	},
}

var privateSetPasswordCmd = &cobra.Command{
	Use:   "set-password [new-password]",
	Short: "Set private network password",
	Long:  "Change the password for the private WiFi network. If no password is provided, a random one will be generated.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return sendCommandAndDisplay("network", []string{"private", "set-password"}, nil)
		}
		return sendCommandAndDisplay("network", []string{"private", "set-password", args[0]}, nil)
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

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start TollGate services",
	Long:  "Start NoDogSplash and TollGate services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeServiceCommand("start")
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop TollGate services",
	Long:  "Stop NoDogSplash and TollGate services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeServiceCommand("stop")
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart TollGate services",
	Long:  "Restart NoDogSplash and TollGate services",
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeServiceCommand("restart")
	},
}

var upstreamCmd = &cobra.Command{
	Use:   "upstream",
	Short: "Upstream WiFi management",
	Long:  "Manage upstream WiFi connections - scan, connect, list, remove",
}

var upstreamScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan for available upstream WiFi networks",
	Long:  "Scan all radios and display available WiFi networks",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("upstream", []string{"scan"}, nil)
	},
}

var upstreamConnectCmd = &cobra.Command{
	Use:   "connect <SSID> [passphrase]",
	Short: "Connect to an upstream WiFi network",
	Long:  "Connect to an upstream WiFi network. Disables the current upstream, preserving it as a known candidate.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmdArgs := []string{"connect", args[0]}
		if len(args) > 1 {
			cmdArgs = append(cmdArgs, args[1])
		}
		return sendCommandStreaming("upstream", cmdArgs, nil)
	},
}

var upstreamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured upstream STA interfaces",
	Long:  "Show all configured upstream STA interfaces with active/disabled status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("upstream", []string{"list-upstream"}, nil)
	},
}

var upstreamRemoveCmd = &cobra.Command{
	Use:   "remove <SSID>",
	Short: "Remove a disabled upstream from config",
	Long:  "Remove a disabled upstream STA interface from the wireless configuration. Active upstreams cannot be removed.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("upstream", []string{"remove-upstream", args[0]}, nil)
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show TollGate logs",
	Long:  "Display TollGate service logs from logread",
	RunE: func(cmd *cobra.Command, args []string) error {
		tail, _ := cmd.Flags().GetInt("tail")
		follow, _ := cmd.Flags().GetBool("follow")
		return executeLogsCommand(tail, follow)
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
	Long:  "View and manage TollGate configuration. All subcommands route through the running service.",
}

var configGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get current configuration",
	Long:  "Display the current configuration. With --json, outputs structured JSON.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("config", []string{"get"}, nil)
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a configuration value",
	Long: `Set a configuration value by dot-path key. Changes are persisted immediately.
Examples:
  tollgate config set metric milliseconds
  tollgate config set step_size 44040192
  tollgate config set accepted_mints.0.price_per_step 2
  tollgate config set show_setup false`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("config", []string{"set", args[0], args[1]}, nil)
	},
}

var configSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Get configuration schema",
	Long:  "Output the configuration schema describing all fields, types, defaults, and validation rules.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("config", []string{"schema"}, nil)
	},
}

var configSaveCmd = &cobra.Command{
	Use:   "save [json]",
	Short: "Save full configuration from JSON string",
	Long:  "Replace the entire config.json with the provided JSON string. Use with caution.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("config", []string{"save", args[0]}, nil)
	},
}

var configSaveIdentitiesCmd = &cobra.Command{
	Use:   "save-identities [json]",
	Short: "Save full identities from JSON string",
	Long:  "Replace the entire identities.json with the provided JSON string. Use with caution.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendCommandAndDisplay("config", []string{"save-identities", args[0]}, nil)
	},
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check service health",
	Long:  "Check the health of TollGate service components.",
	RunE: func(cmd *cobra.Command, args []string) error {
		response, err := sendCommand(CLIMessage{
			Command:   "health",
			Args:      []string{},
			Timestamp: time.Now(),
		})
		if err != nil {
			if jsonOutput {
				return printJSON(map[string]interface{}{
					"success":   false,
					"running":   false,
					"socket_ok": false,
					"error":     fmt.Sprintf("Service not reachable: %v", err),
					"timestamp": time.Now().Format(time.RFC3339),
				})
			}
			return fmt.Errorf("Service not reachable: %v", err)
		}
		if jsonOutput {
			return printJSON(response)
		}
		displayResponse(response)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Output results as JSON")

	logsCmd.Flags().IntP("tail", "n", 0, "Number of lines to show from the end (0 = all)")
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output (like tail -f)")

	drainCmd.AddCommand(drainCashuCmd)
	walletCmd.AddCommand(drainCmd, balanceCmd, infoCmd, fundCmd)
	privateCmd.AddCommand(privateStatusCmd, privateEnableCmd, privateDisableCmd, privateRenameCmd, privateSetPasswordCmd)
	networkCmd.AddCommand(privateCmd)
	upstreamCmd.AddCommand(upstreamScanCmd, upstreamConnectCmd, upstreamListCmd, upstreamRemoveCmd)
	configCmd.AddCommand(configGetCmd, configSetCmd, configSchemaCmd, configSaveCmd, configSaveIdentitiesCmd)
	rootCmd.AddCommand(walletCmd, networkCmd, upstreamCmd, statusCmd, versionCmd, startCmd, stopCmd, restartCmd, logsCmd, configCmd, healthCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func sendCommandAndDisplay(command string, args []string, flags map[string]string) error {
	msg := CLIMessage{
		Command:   command,
		Args:      args,
		Flags:     flags,
		Timestamp: time.Now(),
	}

	response, err := sendCommand(msg)
	if err != nil {
		if jsonOutput {
			return printJSON(&CLIResponse{
				Success:   false,
				Error:     fmt.Sprintf("Failed to communicate with TollGate service: %v", err),
				Timestamp: time.Now(),
			})
		}
		return fmt.Errorf("failed to communicate with TollGate service: %v\nMake sure the TollGate service is running", err)
	}

	if jsonOutput {
		return printJSON(response)
	}

	displayResponse(response)

	if !response.Success {
		return fmt.Errorf("command failed")
	}

	return nil
}

func sendCommandRaw(command string, args []string, flags map[string]string) error {
	msg := CLIMessage{
		Command:   command,
		Args:      args,
		Flags:     flags,
		Timestamp: time.Now(),
	}

	response, err := sendCommand(msg)
	if err != nil {
		return printJSON(&CLIResponse{
			Success:   false,
			Error:     fmt.Sprintf("Failed to communicate with TollGate service: %v", err),
			Timestamp: time.Now(),
		})
	}

	return printJSON(response)
}

func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func sendCommand(msg CLIMessage) (*CLIResponse, error) {
	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to TollGate service: %v", err)
	}
	defer conn.Close()

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

func sendCommandStreaming(command string, args []string, flags map[string]string) error {
	msg := CLIMessage{
		Command:   command,
		Args:      args,
		Flags:     flags,
		Timestamp: time.Now(),
	}

	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return fmt.Errorf("failed to communicate with TollGate service: %v\nMake sure the TollGate service is running", err)
	}
	defer conn.Close()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to encode message: %v", err)
	}

	_, err = conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}
	_, err = conn.Write([]byte("\n"))
	if err != nil {
		return fmt.Errorf("failed to send newline: %v", err)
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var response CLIResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}

		if response.Progress != "" {
			fmt.Printf("  %s\n", response.Progress)
			continue
		}

		displayResponse(&response)
		if !response.Success {
			return fmt.Errorf("command failed")
		}
		return nil
	}

	return fmt.Errorf("no response from service")
}

func executeServiceCommand(action string) error {
	if jsonOutput {
		return executeServiceCommandJSON(action)
	}

	var cmds []struct {
		name string
		cmd  *exec.Cmd
	}

	switch action {
	case "start":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "start")},
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "start")},
		}
	case "stop":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "stop")},
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "stop")},
		}
	case "restart":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "restart")},
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "restart")},
		}
	default:
		return fmt.Errorf("unknown service action: %s", action)
	}

	for _, c := range cmds {
		fmt.Printf("Executing: %s %s...\n", c.name, action)
		output, err := c.cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to %s %s: %v\n", action, c.name, err)
			if len(output) > 0 {
				fmt.Fprintf(os.Stderr, "Output: %s\n", string(output))
			}
			return fmt.Errorf("failed to %s %s", action, c.name)
		}
		if len(output) > 0 {
			fmt.Printf("%s\n", string(output))
		}
	}

	fmt.Printf("Successfully %sed services\n", action)
	return nil
}

func executeServiceCommandJSON(action string) error {
	var cmds []struct {
		name string
		cmd  *exec.Cmd
	}

	switch action {
	case "start":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "start")},
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "start")},
		}
	case "stop":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "stop")},
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "stop")},
		}
	case "restart":
		cmds = []struct {
			name string
			cmd  *exec.Cmd
		}{
			{"NoDogSplash", exec.Command("/etc/init.d/nodogsplash", "restart")},
			{"TollGate", exec.Command("/etc/init.d/tollgate-wrt", "restart")},
		}
	default:
		return printJSON(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("unknown service action: %s", action),
		})
	}

	for _, c := range cmds {
		output, err := c.cmd.CombinedOutput()
		if err != nil {
			return printJSON(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("failed to %s %s: %v", action, c.name, err),
				"output":  string(output),
			})
		}
	}

	return printJSON(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Successfully %sed services", action),
	})
}

func executeLogsCommand(tail int, follow bool) error {
	args := []string{"-e", "tollgate"}

	if follow {
		args = append(args, "-f")
	}

	if tail > 0 {
		args = append(args, "-l", fmt.Sprintf("%d", tail))
	}

	cmd := exec.Command("logread", args...)

	if follow {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to read logs: %v", err)
	}

	fmt.Print(string(output))
	return nil
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
		if _, ok := v["tokens"]; ok {
			displayWalletDrainResult(v)
		} else if _, ok := v["ssid"]; ok {
			displayPrivateNetworkInfo(v)
		} else {
			displayMap(v, "")
		}
	case []interface{}:
		if len(v) > 0 {
			if _, ok := v[0].(map[string]interface{}); ok {
				if _, hasRadio := v[0].(map[string]interface{})["radio"]; hasRadio {
					if _, hasStatus := v[0].(map[string]interface{})["status"]; hasStatus {
						displayUpstreamSTAList(v)
					} else {
						displayUpstreamScanResults(v)
					}
				} else {
					jsonData, err := json.MarshalIndent(data, "", "  ")
					if err == nil {
						fmt.Println(string(jsonData))
					}
				}
			}
		} else {
			fmt.Println("No results")
		}
	default:
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err == nil {
			fmt.Println(string(jsonData))
		}
	}
}

func displayPrivateNetworkInfo(data map[string]interface{}) {
	fmt.Println()
	fmt.Println("Private Network Configuration")
	fmt.Println("=============================")

	if ssid, ok := data["ssid"].(string); ok {
		fmt.Printf("SSID:     %s\n", ssid)
	}

	if password, ok := data["password"].(string); ok {
		fmt.Printf("Password: %s\n", password)
	}

	if enabled, ok := data["enabled"].(bool); ok {
		status := "Disabled"
		if enabled {
			status = "Enabled"
		}
		fmt.Printf("Status:   %s\n", status)
	}

	fmt.Println()
}

func displayWalletDrainResult(data map[string]interface{}) {
	fmt.Println("\nWallet Drain Results:")
	fmt.Println("====================")

	if total, ok := data["total_sats"].(float64); ok {
		fmt.Printf("Total drained: %.0f sats\n\n", total)
	}

	var filename string
	if saveToFile, ok := data["save_to_file"].(string); ok && saveToFile != "" {
		filename = saveToFile
	}

	if tokensData, ok := data["tokens"].([]interface{}); ok {
		if len(tokensData) == 0 {
			fmt.Println("No tokens created (all balances are zero)")
			return
		}

		if filename != "" {
			err := saveTokensToFile(filename, tokensData, data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error saving tokens to file: %v\n", err)
			} else {
				fmt.Printf("✓ Tokens saved to: %s\n\n", filename)
			}
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
					fmt.Printf("  Token: %s\n", token)
				}
				fmt.Println()
			}
		}
	}
}

func saveTokensToFile(filename string, tokensData []interface{}, data map[string]interface{}) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString("# TollGate Wallet Drain\n")
	if err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}
	_, err = file.WriteString(fmt.Sprintf("# Date: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	if err != nil {
		return fmt.Errorf("failed to write date: %w", err)
	}

	if total, ok := data["total_sats"].(float64); ok {
		_, err = file.WriteString(fmt.Sprintf("# Total: %.0f sats across %d tokens\n\n", total, len(tokensData)))
		if err != nil {
			return fmt.Errorf("failed to write total: %w", err)
		}
	}

	for i, tokenData := range tokensData {
		if tokenMap, ok := tokenData.(map[string]interface{}); ok {
			_, err = file.WriteString(fmt.Sprintf("## Token %d\n", i+1))
			if err != nil {
				return fmt.Errorf("failed to write token header: %w", err)
			}

			if mintURL, ok := tokenMap["mint_url"].(string); ok {
				_, err = file.WriteString(fmt.Sprintf("Mint: %s\n", mintURL))
				if err != nil {
					return fmt.Errorf("failed to write mint URL: %w", err)
				}
			}

			if balance, ok := tokenMap["balance_sats"].(float64); ok {
				_, err = file.WriteString(fmt.Sprintf("Balance: %.0f sats\n", balance))
				if err != nil {
					return fmt.Errorf("failed to write balance: %w", err)
				}
			}

			if token, ok := tokenMap["token"].(string); ok {
				_, err = file.WriteString(fmt.Sprintf("Token: %s\n\n", token))
				if err != nil {
					return fmt.Errorf("failed to write token: %w", err)
				}
			}
		}
	}

	return nil
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

func displayUpstreamScanResults(networks []interface{}) {
	fmt.Println()
	fmt.Printf("%-30s  %-8s  %-5s  %-20s  %s\n", "SSID", "Signal", "Ch", "Encryption", "Radio")
	fmt.Println(strings.Repeat("-", 80))
	for i, n := range networks {
		if m, ok := n.(map[string]interface{}); ok {
			ssid := ""
			if s, ok := m["ssid"].(string); ok {
				ssid = s
			}
			signal := ""
			if s, ok := m["signal"].(float64); ok {
				signal = fmt.Sprintf("%.0f dBm", s)
			}
			channel := ""
			if s, ok := m["channel"].(string); ok {
				channel = s
			}
			encryption := ""
			if s, ok := m["encryption"].(string); ok {
				encryption = s
			}
			radio := ""
			if s, ok := m["radio"].(string); ok {
				radio = s
			}
			fmt.Printf("%-30s  %-8s  %-5s  %-20s  %s\n", ssid, signal, channel, encryption, radio)
			_ = i
		}
	}
	fmt.Printf("\nTotal: %d network(s)\n", len(networks))
}

func displayUpstreamSTAList(stas []interface{}) {
	fmt.Println()
	fmt.Printf("%-20s  %-10s  %-10s  %s\n", "SSID", "STATUS", "RADIO", "ENCRYPTION")
	fmt.Println(strings.Repeat("-", 55))
	for _, s := range stas {
		if m, ok := s.(map[string]interface{}); ok {
			ssid := ""
			if v, ok := m["ssid"].(string); ok {
				ssid = v
			}
			status := ""
			if v, ok := m["status"].(string); ok {
				status = v
			}
			radio := ""
			if v, ok := m["radio"].(string); ok {
				radio = v
			}
			encryption := ""
			if v, ok := m["encryption"].(string); ok {
				encryption = v
			}
			fmt.Printf("%-20s  %-10s  %-10s  %s\n", ssid, status, radio, encryption)
		}
	}
	fmt.Printf("\n%d upstream STA(s) configured.\n", len(stas))
}
