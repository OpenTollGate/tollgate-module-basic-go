package cli

import "time"

// CLIMessage represents communication between CLI client and service
type CLIMessage struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// CLIResponse represents a response from the service
type CLIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// WalletInfo represents wallet information
type WalletInfo struct {
	Balance     uint64 `json:"balance_sats"`
	Address     string `json:"address,omitempty"`
	DrainTarget string `json:"drain_target,omitempty"`
}

// CashuToken represents a Cashu token for a specific mint
type CashuToken struct {
	MintURL string `json:"mint_url"`
	Balance uint64 `json:"balance_sats"`
	Token   string `json:"token"`
}

// WalletDrainResult represents the result of draining a wallet
type WalletDrainResult struct {
	Success bool         `json:"success"`
	Tokens  []CashuToken `json:"tokens"`
	Total   uint64       `json:"total_sats"`
}

// ServiceStatus represents basic service status
type ServiceStatus struct {
	Running   bool   `json:"running"`
	Version   string `json:"version"`
	Uptime    string `json:"uptime"`
	ConfigOK  bool   `json:"config_ok"`
	WalletOK  bool   `json:"wallet_ok"`
	NetworkOK bool   `json:"network_ok"`
}
