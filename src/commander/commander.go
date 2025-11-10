package commander

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

const (
	// TIP-07 event kinds
	CommandEventKind  = 21024
	ResponseEventKind = 21025
)

// Commander handles remote commands per TIP-07
type Commander struct {
	configManager      *config_manager.ConfigManager
	tollgatePrivateKey string
	tollgatePubKey     string
	ledger             *CommandLedger
	mu                 sync.Mutex
}

// CommandLedger tracks executed commands for replay protection
// Stores command event IDs that we've processed to prevent replays
type CommandLedger struct {
	ProcessedCommandIDs []string `json:"processed_command_ids"` // Event IDs of commands we've processed
	LastReboot          int64    `json:"last_reboot"`           // Unix timestamp of last reboot
	mu                  sync.Mutex
}

// CommandRequest represents the parsed command from the event
type CommandRequest struct {
	Command   string                 `json:"cmd"`
	Nonce     string                 `json:"nonce"`
	Args      map[string]interface{} `json:"args,omitempty"`
	IssuedAt  int64                  `json:"issued_at"`
	DeviceID  string                 `json:"device_id"`
}

// CommandResponse represents the response to send back
type CommandResponse struct {
	Status    string                 `json:"status"`
	Timestamp int64                  `json:"timestamp"`
	Message   string                 `json:"message,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// NewCommander creates a TIP-07 compliant Commander instance
func NewCommander(configManager *config_manager.ConfigManager) (*Commander, error) {
	identities := configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}

	// Use tollgate identity for TIP-07
	tollgateIdentity, err := identities.GetOwnedIdentity("tollgate")
	if err != nil {
		return nil, fmt.Errorf("tollgate identity not found: %w", err)
	}

	tollgatePubKey, err := nostr.GetPublicKey(tollgateIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get tollgate public key: %w", err)
	}

	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	// Load or create command ledger
	ledger, err := loadOrCreateLedger(config.Control.LedgerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load ledger: %w", err)
	}

	return &Commander{
		configManager:      configManager,
		tollgatePrivateKey: tollgateIdentity.PrivateKey,
		tollgatePubKey:     tollgatePubKey,
		ledger:             ledger,
	}, nil
}

// ListenForCommands listens for TIP-07 command events
func (c *Commander) ListenForCommands() {
	config := c.configManager.GetConfig()
	if config == nil || !config.Control.Enabled {
		log.Printf("Commander: Remote control disabled in config")
		return
	}

	log.Printf("Commander: Starting TIP-07 remote control listener for device %s", config.Control.DeviceID)
	log.Printf("Commander: Authorized controllers: %v", config.Control.AllowedPubkeys)

	ctx := context.Background()

	for {
		log.Printf("Commander: Connecting to relays to listen for commands...")

		for _, relayURL := range config.Relays {
			go c.listenOnRelay(ctx, relayURL)
		}

		// Sleep and then reconnect
		time.Sleep(30 * time.Minute)
	}
}

// listenOnRelay connects to a relay and listens for TIP-07 command events
func (c *Commander) listenOnRelay(ctx context.Context, relayURL string) {
	retryDelay := 5 * time.Second

	for {
		relay, err := c.configManager.GetPublicPool().EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Commander: Failed to connect to relay %s: %v", relayURL, err)
			time.Sleep(retryDelay)
			retryDelay *= 2
			if retryDelay > 5*time.Minute {
				retryDelay = 5 * time.Minute
			}
			continue
		}

		log.Printf("Commander: Connected to relay %s", relayURL)
		retryDelay = 5 * time.Second

		// Subscribe to TIP-07 command events (kind 21024)
		since := nostr.Timestamp(time.Now().Add(-5 * time.Minute).Unix())
		filter := nostr.Filter{
			Kinds: []int{CommandEventKind},
			Tags: nostr.TagMap{
				"p": []string{c.tollgatePubKey}, // Commands addressed to us
			},
			Since: &since,
		}

		sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
		if err != nil {
			log.Printf("Commander: Failed to subscribe on relay %s: %v", relayURL, err)
			time.Sleep(retryDelay)
			continue
		}

		log.Printf("Commander: Subscribed to TIP-07 commands on relay %s", relayURL)

		// Process incoming command events
		for event := range sub.Events {
			c.handleCommandEvent(event)
		}

		log.Printf("Commander: Relay %s disconnected, reconnecting...", relayURL)
		time.Sleep(retryDelay)
	}
}

// handleCommandEvent validates and processes a TIP-07 command event
func (c *Commander) handleCommandEvent(event *nostr.Event) {
	log.Printf("Commander: Received command event %s from %s", event.ID, event.PubKey)

	// Validate the command
	if err := c.validateCommand(event); err != nil {
		log.Printf("Commander: Command validation failed: %v", err)
		c.sendErrorResponse(event, "unauthorized", err.Error())
		return
	}

	// Parse command from event
	cmd, err := c.parseCommand(event)
	if err != nil {
		log.Printf("Commander: Failed to parse command: %v", err)
		c.sendErrorResponse(event, "invalid_command", err.Error())
		return
	}

	// Generate deterministic response event ID for replay check
	// We check if we've already responded to this command event
	if c.ledger.IsExecuted(event.ID) {
		log.Printf("Commander: Replay detected - already processed command %s", event.ID)
		// Don't send another error - we already responded
		return
	}

	// Mark as processed immediately to prevent concurrent replays
	c.ledger.RecordExecution(event.ID)
	config := c.configManager.GetConfig()
	if config != nil {
		if err := c.ledger.Save(config.Control.LedgerPath); err != nil {
			log.Printf("Commander: Warning - failed to save ledger: %v", err)
		}
	}

	// Execute the command (this may send acknowledgment + completion responses)
	log.Printf("Commander: Executing command: %s", cmd.Command)
	c.executeCommand(cmd, event)
}

// validateCommand performs TIP-07 validation
func (c *Commander) validateCommand(event *nostr.Event) error {
	config := c.configManager.GetConfig()
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	// Check if sender is authorized
	authorized := false
	for _, pubkey := range config.Control.AllowedPubkeys {
		if event.PubKey == pubkey {
			authorized = true
			break
		}
	}
	if !authorized {
		return fmt.Errorf("unauthorized pubkey: %s", event.PubKey)
	}

	// Check command age
	commandAge := time.Now().Unix() - int64(event.CreatedAt)
	if commandAge > int64(config.Control.CommandTimeoutSec) {
		return fmt.Errorf("command too old: %d seconds (max %d)", commandAge, config.Control.CommandTimeoutSec)
	}

	if commandAge < -60 {
		return fmt.Errorf("command timestamp in future: %d seconds", -commandAge)
	}

	return nil
}

// parseCommand extracts command details from the event
// Note: device_id is optional and may be removed in future protocol versions
func (c *Commander) parseCommand(event *nostr.Event) (*CommandRequest, error) {
	var cmd CommandRequest

	// Get command from tags
	cmdTag := event.Tags.GetFirst([]string{"cmd"})
	if cmdTag == nil || len(*cmdTag) < 2 {
		return nil, fmt.Errorf("missing cmd tag")
	}
	cmd.Command = strings.ToLower(strings.TrimSpace((*cmdTag)[1]))

	// Get device_id from tags (optional)
	deviceTag := event.Tags.GetFirst([]string{"device_id"})
	if deviceTag != nil && len(*deviceTag) >= 2 {
		cmd.DeviceID = (*deviceTag)[1]
	}

	// Get nonce from tags
	nonceTag := event.Tags.GetFirst([]string{"nonce"})
	if nonceTag != nil && len(*nonceTag) >= 2 {
		cmd.Nonce = (*nonceTag)[1]
	}

	// Parse content if present
	if event.Content != "" {
		var content map[string]interface{}
		if err := json.Unmarshal([]byte(event.Content), &content); err == nil {
			if args, ok := content["args"].(map[string]interface{}); ok {
				cmd.Args = args
			}
		}
	}

	cmd.IssuedAt = int64(event.CreatedAt)

	return &cmd, nil
}

// executeCommand executes the requested command per TIP-07
// This is extensible - new commands can be added here:
// - Configuration commands (set_price, update_config)
// - Diagnostic commands (get_logs, network_status)
// - Management commands (restart_service, clear_cache)
// Each command can send single or multiple responses as needed
func (c *Commander) executeCommand(cmd *CommandRequest, event *nostr.Event) {
	switch cmd.Command {
	case "uptime":
		response := c.handleUptime()
		c.sendResponse(event, response)
	case "reboot":
		c.handleRebootWithLifecycle(cmd, event)
	case "status":
		response := c.handleStatus()
		c.sendResponse(event, response)
	// Future commands:
	// case "set_price":
	//     c.handleSetPrice(cmd, event)
	// case "exec":
	//     c.handleArbitraryCommand(cmd, event)
	default:
		response := &CommandResponse{
			Status:    "error",
			Timestamp: time.Now().Unix(),
			Error:     "invalid_command",
			Message:   fmt.Sprintf("Unknown command: %s", cmd.Command),
		}
		c.sendResponse(event, response)
	}
}

// handleUptime executes the uptime command
func (c *Commander) handleUptime() *CommandResponse {
	// Get system uptime in seconds
	cmd := exec.Command("cat", "/proc/uptime")
	output, err := cmd.Output()
	if err != nil {
		return &CommandResponse{
			Status:    "error",
			Timestamp: time.Now().Unix(),
			Error:     "execution_failed",
			Message:   fmt.Sprintf("Failed to get uptime: %v", err),
		}
	}

	uptimeStr := strings.Split(string(output), " ")[0]
	var uptimeSec float64
	fmt.Sscanf(uptimeStr, "%f", &uptimeSec)

	// Get load average
	cmd = exec.Command("cat", "/proc/loadavg")
	loadOutput, _ := cmd.Output()
	loadParts := strings.Split(string(loadOutput), " ")
	loadAvg := []string{loadParts[0], loadParts[1], loadParts[2]}

	return &CommandResponse{
		Status:    "ok",
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"uptime_sec": int64(uptimeSec),
			"load_avg":   loadAvg,
		},
	}
}

// handleRebootWithLifecycle handles reboot with acknowledgment response
// Success = scheduled within 60 seconds, no need to track actual execution
func (c *Commander) handleRebootWithLifecycle(cmd *CommandRequest, event *nostr.Event) {
	config := c.configManager.GetConfig()
	if config == nil {
		response := &CommandResponse{
			Status:    "error",
			Timestamp: time.Now().Unix(),
			Error:     "execution_failed",
			Message:   "Config is nil",
		}
		c.sendResponse(event, response)
		return
	}

	// Check reboot rate limit
	timeSinceLastReboot := time.Now().Unix() - c.ledger.LastReboot
	if c.ledger.LastReboot > 0 && timeSinceLastReboot < int64(config.Control.RebootMinIntervalSec) {
		response := &CommandResponse{
			Status:    "error",
			Timestamp: time.Now().Unix(),
			Error:     "reboot_too_soon",
			Message: fmt.Sprintf("Last reboot was %d seconds ago, minimum interval is %d seconds",
				timeSinceLastReboot, config.Control.RebootMinIntervalSec),
		}
		c.sendResponse(event, response)
		return
	}

	// Get delay from args, default 60 seconds per TIP-07
	delaySec := 60
	if cmd.Args != nil {
		if delay, ok := cmd.Args["delay_sec"].(float64); ok {
			delaySec = int(delay)
		}
	}

	log.Printf("Commander: REBOOT command accepted, scheduling reboot in %d seconds...", delaySec)

	// Send acknowledgment - this is the success response
	ackResponse := &CommandResponse{
		Status:    "ok",
		Timestamp: time.Now().Unix(),
		Message:   fmt.Sprintf("Reboot scheduled in %d seconds", delaySec),
		Data: map[string]interface{}{
			"delay_sec": delaySec,
		},
	}
	c.sendResponse(event, ackResponse)

	// Update ledger before reboot
	c.ledger.LastReboot = time.Now().Unix()
	if err := c.ledger.Save(config.Control.LedgerPath); err != nil {
		log.Printf("Commander: Failed to save ledger: %v", err)
	}

	// Schedule reboot - fire and forget, no error handling needed
	go func() {
		time.Sleep(time.Duration(delaySec) * time.Second)
		
		// Sync filesystem before reboot
		syncCmd := exec.Command("sync")
		syncCmd.Run()
		
		// Execute reboot
		rebootCmd := exec.Command("reboot")
		rebootCmd.Run()
	}()
}

// handleStatus returns comprehensive device status
func (c *Commander) handleStatus() *CommandResponse {
	config := c.configManager.GetConfig()

	// Get uptime
	cmd := exec.Command("cat", "/proc/uptime")
	output, _ := cmd.Output()
	uptimeStr := strings.Split(string(output), " ")[0]
	var uptimeSec float64
	fmt.Sscanf(uptimeStr, "%f", &uptimeSec)

	// Get memory info
	cmd = exec.Command("cat", "/proc/meminfo")
	memOutput, _ := cmd.Output()
	memLines := strings.Split(string(memOutput), "\n")
	var memFree int64
	for _, line := range memLines {
		if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d", &memFree)
			break
		}
	}

	return &CommandResponse{
		Status:    "ok",
		Timestamp: time.Now().Unix(),
		Data: map[string]interface{}{
			"version":       config.ConfigVersion,
			"uptime_sec":    int64(uptimeSec),
			"memory_free_kb": memFree,
			"device_id":     config.Control.DeviceID,
		},
	}
}

// sendResponse sends a TIP-07 compliant response event
func (c *Commander) sendResponse(commandEvent *nostr.Event, response *CommandResponse) {
	config := c.configManager.GetConfig()
	if config == nil {
		log.Printf("Commander: Cannot send response - config is nil")
		return
	}

	// Get command name from original event
	cmdTag := commandEvent.Tags.GetFirst([]string{"cmd"})
	cmdName := ""
	if cmdTag != nil && len(*cmdTag) >= 2 {
		cmdName = (*cmdTag)[1]
	}

	// Marshal response to JSON
	contentBytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("Commander: Failed to marshal response: %v", err)
		return
	}

	// Create TIP-07 response event (kind 21025)
	event := nostr.Event{
		PubKey:    c.tollgatePubKey,
		CreatedAt: nostr.Now(),
		Kind:      ResponseEventKind,
		Tags: nostr.Tags{
			{"p", commandEvent.PubKey},
			{"cmd", cmdName},
			{"in_reply_to", commandEvent.ID},
			{"status", response.Status},
		},
		Content: string(contentBytes),
	}

	// Only include device_id if it was in the command (optional field)
	deviceTag := commandEvent.Tags.GetFirst([]string{"device_id"})
	if deviceTag != nil && len(*deviceTag) >= 2 && (*deviceTag)[1] != "" {
		event.Tags = append(event.Tags, nostr.Tag{"device_id", (*deviceTag)[1]})
	}

	// Sign the event
	if err := event.Sign(c.tollgatePrivateKey); err != nil {
		log.Printf("Commander: Failed to sign response: %v", err)
		return
	}

	// Publish to all configured relays
	published := false
	for _, relayURL := range config.Relays {
		relay, err := c.configManager.GetPublicPool().EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Commander: Failed to connect to relay %s: %v", relayURL, err)
			continue
		}

		if err := relay.Publish(context.Background(), event); err != nil {
			log.Printf("Commander: Failed to publish response to %s: %v", relayURL, err)
		} else {
			log.Printf("Commander: Response %s published to %s", event.ID, relayURL)
			published = true
		}
	}

	if !published {
		log.Printf("Commander: WARNING - Failed to publish response to any relay")
	}
}

// sendErrorResponse sends an error response
func (c *Commander) sendErrorResponse(commandEvent *nostr.Event, errorCode, message string) {
	response := &CommandResponse{
		Status:    "error",
		Timestamp: time.Now().Unix(),
		Error:     errorCode,
		Message:   message,
	}
	c.sendResponse(commandEvent, response)
}

// Ledger management functions

func loadOrCreateLedger(path string) (*CommandLedger, error) {
	// Try to load existing ledger
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new ledger
			ledger := &CommandLedger{
				ProcessedCommandIDs: []string{},
				LastReboot:          0,
			}
			// Save initial ledger to ensure directory exists
			if saveErr := ledger.Save(path); saveErr != nil {
				log.Printf("Commander: Warning - could not save initial ledger: %v", saveErr)
			}
			return ledger, nil
		}
		return nil, err
	}

	var ledger CommandLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return nil, err
	}

	// Initialize slice if nil (backward compatibility)
	if ledger.ProcessedCommandIDs == nil {
		ledger.ProcessedCommandIDs = []string{}
	}

	return &ledger, nil
}

func (l *CommandLedger) IsExecuted(commandEventID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, id := range l.ProcessedCommandIDs {
		if id == commandEventID {
			return true
		}
	}
	return false
}

func (l *CommandLedger) RecordExecution(commandEventID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ProcessedCommandIDs = append(l.ProcessedCommandIDs, commandEventID)

	// Keep only last 1000 command IDs to prevent unbounded growth
	if len(l.ProcessedCommandIDs) > 1000 {
		l.ProcessedCommandIDs = l.ProcessedCommandIDs[len(l.ProcessedCommandIDs)-1000:]
	}
}

func (l *CommandLedger) Save(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure directory exists (path includes filename, extract directory)
	dir := path[:strings.LastIndex(path, "/")]
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
