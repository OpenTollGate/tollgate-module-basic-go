package chandler

import (
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestLoggerChandler(t *testing.T) {
	// Create a new logger chandler
	chandler := NewLoggerChandler()

	// Test HandleUpstreamTollgate
	testEvent := &nostr.Event{
		ID:      "test-id-123",
		PubKey:  "test-pubkey",
		Kind:    10021,
		Content: "test TollGate advertisement",
	}

	upstream := &UpstreamTollgate{
		InterfaceName: "eth0",
		MacAddress:    "00:11:22:33:44:55",
		GatewayIP:     "192.168.1.1",
		Advertisement: testEvent,
		DiscoveredAt:  time.Now(),
	}

	// Test connection
	err := chandler.HandleUpstreamTollgate(upstream)
	if err != nil {
		t.Errorf("HandleUpstreamTollgate failed: %v", err)
	}

	// Verify the connection was stored
	loggerChandler := chandler.(*loggerChandler)
	connections := loggerChandler.GetUpstreamTollgates()
	if len(connections) != 1 {
		t.Errorf("Expected 1 connection, got %d", len(connections))
	}

	storedConnection, exists := connections["eth0"]
	if !exists {
		t.Error("Expected connection for eth0 not found")
	}

	if storedConnection.GatewayIP != "192.168.1.1" {
		t.Errorf("Expected gateway IP 192.168.1.1, got %s", storedConnection.GatewayIP)
	}

	// Test disconnection
	err = chandler.HandleDisconnect("eth0")
	if err != nil {
		t.Errorf("HandleDisconnect failed: %v", err)
	}

	// Verify the connection was removed
	connections = loggerChandler.GetUpstreamTollgates()
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections after disconnect, got %d", len(connections))
	}

	// Test stats
	stats := loggerChandler.GetStats()
	if stats["active_connections"] != 0 {
		t.Errorf("Expected 0 active connections, got %d", stats["active_connections"])
	}
}

func TestLoggerChandlerDisconnectNonExistentInterface(t *testing.T) {
	chandler := NewLoggerChandler()

	// Test disconnecting an interface that was never connected
	err := chandler.HandleDisconnect("nonexistent")
	if err != nil {
		t.Errorf("HandleDisconnect for nonexistent interface failed: %v", err)
	}

	// Should not crash and should handle gracefully
	loggerChandler := chandler.(*loggerChandler)
	stats := loggerChandler.GetStats()
	if stats["active_connections"] != 0 {
		t.Errorf("Expected 0 active connections, got %d", stats["active_connections"])
	}
}
