//go:build !linux
// +build !linux

package crowsnest

import (
	"fmt"
	"log"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// stubNetworkMonitor is a stub implementation for non-Linux systems
type stubNetworkMonitor struct {
	config   *CrowsnestConfig
	events   chan NetworkEvent
	stopChan chan struct{}
	running  bool
}

// NewNetworkMonitor creates a stub network monitor for non-Linux systems
func NewNetworkMonitor(config *config_manager.CrowsnestConfig) NetworkMonitor {
	log.Printf("Warning: Using stub network monitor - netlink functionality only available on Linux")
	return &stubNetworkMonitor{
		config:   config,
		events:   make(chan NetworkEvent, 100),
		stopChan: make(chan struct{}),
	}
}

// Start begins the stub monitor (does nothing on non-Linux)
func (nm *stubNetworkMonitor) Start() error {
	if nm.running {
		return fmt.Errorf("stub network monitor is already running")
	}

	log.Printf("Starting stub network monitor (no actual monitoring on non-Linux systems)")
	nm.running = true

	// Send a fake interface up event for testing purposes
	go func() {
		time.Sleep(2 * time.Second)
		testEvent := NetworkEvent{
			Type:          EventInterfaceUp,
			InterfaceName: "eth0",
			InterfaceInfo: &InterfaceInfo{
				Name:        "eth0",
				MacAddress:  "00:11:22:33:44:55",
				IPAddresses: []string{"192.168.1.100"},
				IsUp:        true,
			},
			GatewayIP: "192.168.1.1",
			Timestamp: time.Now(),
		}

		select {
		case nm.events <- testEvent:
			log.Printf("Sent test network event for testing")
		case <-nm.stopChan:
			return
		}
	}()

	return nil
}

// Stop stops the stub monitor
func (nm *stubNetworkMonitor) Stop() error {
	if !nm.running {
		return nil
	}

	log.Printf("Stopping stub network monitor")
	close(nm.stopChan)
	nm.running = false
	close(nm.events)

	return nil
}

// Events returns the events channel
func (nm *stubNetworkMonitor) Events() <-chan NetworkEvent {
	return nm.events
}

// GetCurrentInterfaces returns empty interface list for stub
func (nm *stubNetworkMonitor) GetCurrentInterfaces() ([]*InterfaceInfo, error) {
	return []*InterfaceInfo{}, nil
}

// GetGatewayForInterface returns empty gateway for stub
func (nm *stubNetworkMonitor) GetGatewayForInterface(interfaceName string) string {
	return ""
}
