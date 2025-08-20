package mint_proxy

import "time"

// WebSocket message types for client-server communication

// ClientMessage represents incoming messages from the client
type ClientMessage struct {
	Type    string `json:"type"`
	MintURL string `json:"mint_url,omitempty"`
	Amount  uint64 `json:"amount,omitempty"`
}

// ServerMessage represents outgoing messages to the client
type ServerMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Invoice   string `json:"invoice,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Tokens    string `json:"tokens,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Message type constants
const (
	// Client -> Server message types
	MessageTypeMintRequest = "mint_request"

	// Server -> Client message types
	MessageTypeInvoiceReady = "invoice_ready"
	MessageTypeTokensReady  = "tokens_ready"
	MessageTypeError        = "error"
)

// Error codes
const (
	ErrorCodeInvalidMint      = "invalid_mint"
	ErrorCodeMintUnavailable  = "mint_unavailable"
	ErrorCodeInvoiceExpired   = "invoice_expired"
	ErrorCodePaymentFailed    = "payment_failed"
	ErrorCodeTokenClaimFailed = "token_claim_failed"
	ErrorCodeRequestNotFound  = "request_not_found"
	ErrorCodeInternalError    = "internal_error"
	ErrorCodeInvalidMessage   = "invalid_message"
)

// MintRequest represents the internal state of a mint request
type MintRequest struct {
	RequestID  string    `json:"request_id"`
	MACAddress string    `json:"mac_address"`
	MintURL    string    `json:"mint_url"`
	Amount     uint64    `json:"amount"`
	Invoice    string    `json:"invoice"`
	Status     string    `json:"status"`
	Tokens     string    `json:"tokens"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Status constants for MintRequest
const (
	StatusPending         = "pending"
	StatusInvoiceReady    = "invoice_ready"
	StatusPaid            = "paid"
	StatusTokensDelivered = "tokens_delivered"
	StatusExpired         = "expired"
	StatusError           = "error"
)

// Default configuration values
const (
	DefaultRequestTimeout       = 30 * time.Minute
	DefaultCleanupInterval      = 5 * time.Minute
	DefaultPaymentCheckInterval = 10 * time.Second
)
