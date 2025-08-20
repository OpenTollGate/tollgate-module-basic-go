package mint_proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// WebSocketHandler manages WebSocket connections for the mint proxy
type WebSocketHandler struct {
	upgrader     websocket.Upgrader
	stateManager *StateManager
	validator    MintValidator
	logger       *logrus.Entry
	// Track active connections by MAC address
	connections map[string][]*websocket.Conn
	connMutex   sync.RWMutex
	// Shared mint clients by URL to avoid wallet conflicts
	mintClients map[string]*MintClient
	clientMutex sync.RWMutex
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(stateManager *StateManager, validator MintValidator) *WebSocketHandler {
	return &WebSocketHandler{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Allow all origins for now - in production you might want to restrict this
				return true
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		stateManager: stateManager,
		validator:    validator,
		logger:       logrus.WithField("module", "mint_proxy.websocket_handler"),
		connections:  make(map[string][]*websocket.Conn),
		mintClients:  make(map[string]*MintClient),
	}
}

// HandleWebSocket handles incoming WebSocket connections
func (wsh *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Get client's IP and MAC address
	clientIP := getClientIP(r)
	macAddress, err := getMACFromIP(clientIP)
	if err != nil {
		wsh.logger.WithError(err).WithField("client_ip", clientIP).Error("Failed to get MAC address")
		http.Error(w, "Failed to identify client", http.StatusBadRequest)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := wsh.upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsh.logger.WithError(err).WithField("mac_address", macAddress).Error("Failed to upgrade to WebSocket")
		return
	}
	defer conn.Close()

	wsh.logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"client_ip":   clientIP,
	}).Info("WebSocket connection established")

	// Register connection
	wsh.registerConnection(macAddress, conn)
	defer wsh.unregisterConnection(macAddress, conn)

	// Handle messages from client
	wsh.handleConnection(conn, macAddress)
}

// registerConnection adds a connection to the tracking map
func (wsh *WebSocketHandler) registerConnection(macAddress string, conn *websocket.Conn) {
	wsh.connMutex.Lock()
	defer wsh.connMutex.Unlock()

	if wsh.connections[macAddress] == nil {
		wsh.connections[macAddress] = make([]*websocket.Conn, 0)
	}
	wsh.connections[macAddress] = append(wsh.connections[macAddress], conn)

	wsh.logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"conn_count":  len(wsh.connections[macAddress]),
	}).Debug("Registered WebSocket connection")
}

// unregisterConnection removes a connection from the tracking map
func (wsh *WebSocketHandler) unregisterConnection(macAddress string, conn *websocket.Conn) {
	wsh.connMutex.Lock()
	defer wsh.connMutex.Unlock()

	connections := wsh.connections[macAddress]
	if connections == nil {
		return
	}

	// Remove the connection
	for i, c := range connections {
		if c == conn {
			wsh.connections[macAddress] = append(connections[:i], connections[i+1:]...)
			break
		}
	}

	// Clean up empty slice
	if len(wsh.connections[macAddress]) == 0 {
		delete(wsh.connections, macAddress)
	}

	wsh.logger.WithField("mac_address", macAddress).Debug("Unregistered WebSocket connection")
}

// handleConnection processes messages from a WebSocket connection
func (wsh *WebSocketHandler) handleConnection(conn *websocket.Conn, macAddress string) {
	for {
		// Read message from client
		_, messageBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wsh.logger.WithError(err).WithField("mac_address", macAddress).Error("WebSocket read error")
			}
			break
		}

		// Parse client message
		var clientMsg ClientMessage
		if err := json.Unmarshal(messageBytes, &clientMsg); err != nil {
			wsh.logger.WithError(err).WithField("mac_address", macAddress).Error("Failed to parse client message")
			wsh.sendError(conn, ErrorCodeInvalidMessage, "Invalid message format")
			continue
		}

		wsh.logger.WithFields(logrus.Fields{
			"mac_address":  macAddress,
			"message_type": clientMsg.Type,
		}).Debug("Received client message")

		// Handle the message
		wsh.handleClientMessage(conn, macAddress, &clientMsg)
	}
}

// handleClientMessage processes different types of client messages
func (wsh *WebSocketHandler) handleClientMessage(conn *websocket.Conn, macAddress string, msg *ClientMessage) {
	switch msg.Type {
	case MessageTypeMintRequest:
		wsh.handleMintRequest(conn, macAddress, msg)
	default:
		wsh.logger.WithFields(logrus.Fields{
			"mac_address":  macAddress,
			"message_type": msg.Type,
		}).Error("Unknown message type")
		wsh.sendError(conn, ErrorCodeInvalidMessage, "Unknown message type: "+msg.Type)
	}
}

// handleMintRequest processes a mint request from the client
func (wsh *WebSocketHandler) handleMintRequest(conn *websocket.Conn, macAddress string, msg *ClientMessage) {
	// Validate request
	if msg.MintURL == "" {
		wsh.sendError(conn, ErrorCodeInvalidMessage, "mint_url is required")
		return
	}
	if msg.Amount == 0 {
		wsh.sendError(conn, ErrorCodeInvalidMessage, "amount must be greater than 0")
		return
	}

	// Validate mint URL against accepted mints
	if !wsh.validator.IsValidMint(msg.MintURL) {
		wsh.logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"mint_url":    msg.MintURL,
		}).Warning("Mint not in accepted list")
		wsh.sendError(conn, ErrorCodeInvalidMint, "Mint not in accepted list")
		return
	}

	// Create request in state manager
	request := wsh.stateManager.CreateRequest(macAddress, msg.MintURL, msg.Amount)

	wsh.logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"request_id":  request.RequestID,
		"mint_url":    msg.MintURL,
		"amount":      msg.Amount,
	}).Info("Processing mint request")

	// Start async processing of the mint request
	go wsh.processMintRequest(request)
}

// getMintClient gets or creates a shared mint client for the given URL
func (wsh *WebSocketHandler) getMintClient(mintURL string) (*MintClient, error) {
	wsh.clientMutex.RLock()
	client, exists := wsh.mintClients[mintURL]
	wsh.clientMutex.RUnlock()

	if exists {
		wsh.logger.WithField("mint_url", mintURL).Debug("Reusing existing mint client")
		return client, nil
	}

	// Create new client if doesn't exist
	wsh.clientMutex.Lock()
	defer wsh.clientMutex.Unlock()

	// Double-check in case another goroutine created it
	if client, exists := wsh.mintClients[mintURL]; exists {
		return client, nil
	}

	wsh.logger.WithField("mint_url", mintURL).Debug("Creating new shared mint client")
	client, err := NewMintClient(mintURL)
	if err != nil {
		return nil, err
	}

	wsh.mintClients[mintURL] = client
	return client, nil
}

// processMintRequest handles the async processing of a mint request
func (wsh *WebSocketHandler) processMintRequest(request *MintRequest) {
	// Get or create shared mint client
	mintClient, err := wsh.getMintClient(request.MintURL)
	if err != nil {
		wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to get mint client")
		wsh.stateManager.UpdateRequestStatus(request.RequestID, StatusError)
		wsh.broadcastError(request.MACAddress, ErrorCodeMintUnavailable, "Failed to connect to mint")
		return
	}

	// Request invoice from mint
	invoiceResp, err := mintClient.RequestInvoice(request.MintURL, request.Amount)
	if err != nil {
		wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to request invoice")
		wsh.stateManager.UpdateRequestStatus(request.RequestID, StatusError)
		wsh.broadcastError(request.MACAddress, ErrorCodeMintUnavailable, "Failed to get invoice from mint")
		return
	}

	// Update request with invoice
	if err := wsh.stateManager.SetInvoice(request.RequestID, invoiceResp.PaymentRequest); err != nil {
		wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to set invoice")
		return
	}

	// Send invoice to client
	serverMsg := ServerMessage{
		Type:      MessageTypeInvoiceReady,
		RequestID: request.RequestID,
		Invoice:   invoiceResp.PaymentRequest,
		ExpiresAt: invoiceResp.ExpiresAt.Unix(),
	}
	wsh.broadcastToMAC(request.MACAddress, &serverMsg)

	// Start monitoring payment
	wsh.monitorPayment(request, mintClient, invoiceResp.PaymentHash)
}

// monitorPayment monitors a Lightning payment and mints tokens when paid
func (wsh *WebSocketHandler) monitorPayment(request *MintRequest, mintClient *MintClient, paymentHash string) {
	ticker := time.NewTicker(DefaultPaymentCheckInterval)
	defer ticker.Stop()

	timeout := time.After(DefaultRequestTimeout)

	wsh.logger.WithFields(logrus.Fields{
		"request_id":   request.RequestID,
		"payment_hash": paymentHash,
	}).Debug("Started payment monitoring")

	for {
		select {
		case <-ticker.C:
			// Check payment status
			status, err := mintClient.CheckPaymentStatus(request.MintURL, paymentHash)
			if err != nil {
				wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to check payment status")
				continue
			}

			if status.Paid {
				wsh.logger.WithField("request_id", request.RequestID).Info("Payment confirmed, minting tokens")

				// Payment confirmed - mint tokens immediately
				tokens, err := mintClient.MintTokens(request.MintURL, paymentHash, request.Amount)
				if err != nil {
					wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to mint tokens")
					wsh.stateManager.UpdateRequestStatus(request.RequestID, StatusError)
					wsh.broadcastError(request.MACAddress, ErrorCodeTokenClaimFailed, "Failed to mint tokens")
					return
				}

				// Store tokens and mark as delivered
				if err := wsh.stateManager.SetTokens(request.RequestID, tokens); err != nil {
					wsh.logger.WithError(err).WithField("request_id", request.RequestID).Error("Failed to set tokens")
					return
				}

				// Send tokens to client
				serverMsg := ServerMessage{
					Type:      MessageTypeTokensReady,
					RequestID: request.RequestID,
					Tokens:    tokens,
				}
				wsh.broadcastToMAC(request.MACAddress, &serverMsg)

				wsh.logger.WithField("request_id", request.RequestID).Info("Tokens delivered to client")
				return
			}

		case <-timeout:
			wsh.logger.WithField("request_id", request.RequestID).Warning("Payment monitoring timeout")
			wsh.stateManager.UpdateRequestStatus(request.RequestID, StatusExpired)
			wsh.broadcastError(request.MACAddress, ErrorCodeInvoiceExpired, "Payment timeout - invoice expired")
			return
		}
	}
}

// sendError sends an error message to a specific connection
func (wsh *WebSocketHandler) sendError(conn *websocket.Conn, code, message string) {
	serverMsg := ServerMessage{
		Type:    MessageTypeError,
		Code:    code,
		Message: message,
	}

	msgBytes, err := json.Marshal(serverMsg)
	if err != nil {
		wsh.logger.WithError(err).Error("Failed to marshal error message")
		return
	}

	if err := conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		wsh.logger.WithError(err).Error("Failed to send error message")
	}
}

// broadcastError sends an error message to all connections for a MAC address
func (wsh *WebSocketHandler) broadcastError(macAddress, code, message string) {
	serverMsg := ServerMessage{
		Type:    MessageTypeError,
		Code:    code,
		Message: message,
	}
	wsh.broadcastToMAC(macAddress, &serverMsg)
}

// broadcastToMAC sends a message to all connections for a specific MAC address
func (wsh *WebSocketHandler) broadcastToMAC(macAddress string, msg *ServerMessage) {
	wsh.connMutex.RLock()
	connections := wsh.connections[macAddress]
	wsh.connMutex.RUnlock()

	if connections == nil {
		wsh.logger.WithField("mac_address", macAddress).Debug("No connections found for MAC address")
		return
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		wsh.logger.WithError(err).Error("Failed to marshal server message")
		return
	}

	// Send to all connections for this MAC address
	for _, conn := range connections {
		if err := conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
			wsh.logger.WithError(err).WithField("mac_address", macAddress).Error("Failed to send message to connection")
		}
	}

}

// Close cleans up resources including shared mint clients
func (wsh *WebSocketHandler) Close() error {
	wsh.clientMutex.Lock()
	defer wsh.clientMutex.Unlock()

	wsh.logger.Info("Closing WebSocket handler and cleaning up mint clients")

	// Close all shared mint clients
	for mintURL, client := range wsh.mintClients {
		wsh.logger.WithField("mint_url", mintURL).Debug("Closing shared mint client")
		if err := client.Close(); err != nil {
			wsh.logger.WithError(err).WithField("mint_url", mintURL).Error("Error closing mint client")
		}
	}

	// Clear the map
	wsh.mintClients = make(map[string]*MintClient)

	return nil
}

// Utility functions that need to be implemented or imported
func getClientIP(r *http.Request) string {
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

// getMACFromIP gets MAC address from IP using the same logic as main.go
func getMACFromIP(ip string) (string, error) {
	cmdIn := `cat /tmp/dhcp.leases | cut -f 2,3,4 -s -d" " | grep -i ` + ip + ` | cut -f 1 -s -d" "`
	commandOutput, err := exec.Command("sh", "-c", cmdIn).Output()

	commandOutputString := string(commandOutput)
	if err != nil {
		return "", fmt.Errorf("error getting MAC address for IP %s: %w (command output: %s)", ip, err, commandOutputString)
	}

	macAddress := strings.TrimSpace(commandOutputString)
	if macAddress == "" {
		return "", fmt.Errorf("no MAC address found for IP %s", ip)
	}

	return macAddress, nil
}
