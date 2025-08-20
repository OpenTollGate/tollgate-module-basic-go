package mint_proxy

import (
	"fmt"
	"net/http"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/sirupsen/logrus"
)

// MintValidator interface for validating mint URLs
type MintValidator interface {
	IsValidMint(mintURL string) bool
	GetAcceptedMints() []config_manager.MintConfig
}

// merchantValidator implements MintValidator using the merchant instance
type merchantValidator struct {
	merchant merchant.MerchantInterface
	logger   *logrus.Entry
}

// NewMerchantValidator creates a new validator that uses the merchant's accepted mints
func NewMerchantValidator(merchantInstance merchant.MerchantInterface) MintValidator {
	return &merchantValidator{
		merchant: merchantInstance,
		logger:   logrus.WithField("module", "mint_proxy.validator"),
	}
}

// IsValidMint checks if a mint URL is in the merchant's accepted list
func (mv *merchantValidator) IsValidMint(mintURL string) bool {
	acceptedMints := mv.merchant.GetAcceptedMints()

	for _, mint := range acceptedMints {
		if mint.URL == mintURL {
			mv.logger.WithField("mint_url", mintURL).Debug("Mint validated successfully")
			return true
		}
	}

	mv.logger.WithField("mint_url", mintURL).Debug("Mint not in accepted list")
	return false
}

// GetAcceptedMints returns the list of accepted mints
func (mv *merchantValidator) GetAcceptedMints() []config_manager.MintConfig {
	return mv.merchant.GetAcceptedMints()
}

// MintProxy is the main service that handles WebSocket connections and mint operations
type MintProxy struct {
	stateManager *StateManager
	validator    MintValidator
	wsHandler    *WebSocketHandler
	logger       *logrus.Entry
	server       *http.Server
}

// NewMintProxy creates a new mint proxy service
func NewMintProxy(merchantInstance merchant.MerchantInterface, port string) (*MintProxy, error) {
	logger := logrus.WithField("module", "mint_proxy")
	logger.WithField("port", port).Info("Creating new mint proxy instance")

	if merchantInstance == nil {
		logger.Error("Merchant instance is nil")
		return nil, fmt.Errorf("merchant instance cannot be nil")
	}

	// Create validator
	logger.Debug("Creating merchant validator")
	validator := NewMerchantValidator(merchantInstance)

	// Log accepted mints
	acceptedMints := validator.GetAcceptedMints()
	logger.WithField("accepted_mints_count", len(acceptedMints)).Info("Validator created with accepted mints")
	for i, mint := range acceptedMints {
		logger.WithFields(logrus.Fields{
			"mint_index": i,
			"mint_url":   mint.URL,
		}).Debug("Accepted mint configured")
	}

	// Create state manager
	logger.Debug("Creating state manager")
	stateManager := NewStateManager()

	// Create WebSocket handler
	logger.Debug("Creating WebSocket handler")
	wsHandler := NewWebSocketHandler(stateManager, validator)

	// Create HTTP server
	logger.WithField("addr", port).Debug("Creating HTTP server")
	server := &http.Server{
		Addr:         port,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	proxy := &MintProxy{
		stateManager: stateManager,
		validator:    validator,
		wsHandler:    wsHandler,
		logger:       logger,
		server:       server,
	}

	// Set up HTTP routes
	logger.Debug("Setting up HTTP routes")
	proxy.setupRoutes()

	logger.Info("Mint proxy instance created successfully")
	return proxy, nil
}

// setupRoutes configures the HTTP routes for the mint proxy
func (mp *MintProxy) setupRoutes() {
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/mint-proxy", mp.wsHandler.HandleWebSocket)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Stats endpoint
	mux.HandleFunc("/stats", mp.handleStats)

	mp.server.Handler = mux
}

// handleStats returns statistics about the mint proxy
func (mp *MintProxy) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := mp.stateManager.GetStats()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Simple JSON response
	fmt.Fprintf(w, `{
		"total_requests": %v,
		"unique_mac_addresses": %v,
		"status_counts": %v
	}`, stats["total_requests"], stats["unique_mac_addresses"], stats["status_counts"])
}

// Start starts the mint proxy server
func (mp *MintProxy) Start() error {
	mp.logger.WithField("addr", mp.server.Addr).Info("Starting mint proxy server")

	// Log the available endpoints
	mp.logger.WithFields(logrus.Fields{
		"websocket_endpoint": "/mint-proxy",
		"health_endpoint":    "/health",
		"stats_endpoint":     "/stats",
	}).Info("Mint proxy endpoints configured")

	// Start the server
	err := mp.server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		mp.logger.WithError(err).Error("Mint proxy server failed to start")
		return err
	}

	mp.logger.Info("Mint proxy server stopped")
	return nil
}

// Stop stops the mint proxy server and cleans up resources
func (mp *MintProxy) Stop() error {
	mp.logger.Info("Stopping mint proxy server")

	// Stop state manager cleanup routines
	mp.stateManager.Stop()

	// Clean up WebSocket handler resources (shared mint clients)
	if err := mp.wsHandler.Close(); err != nil {
		mp.logger.WithError(err).Error("Error closing WebSocket handler")
	}

	// Shutdown HTTP server
	return mp.server.Close()
}

// GetStats returns current mint proxy statistics
func (mp *MintProxy) GetStats() map[string]interface{} {
	return mp.stateManager.GetStats()
}
