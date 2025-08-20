// Package wireless_gateway_manager implements the GatewayManager for managing Wi-Fi gateways.
package wireless_gateway_manager

import (
	"os/exec"
	"time"
)

// IsConnected returns true if the network is considered connected.
func (nm *NetworkMonitor) IsConnected() bool {
	return nm.pingFailures < consecutiveFailures
}

// NewNetworkMonitor initializes and returns a new NetworkMonitor.
func NewNetworkMonitor(connector *Connector) *NetworkMonitor {
	return &NetworkMonitor{
		connector: connector,
		stopChan:  make(chan struct{}),
	}
}

// Start begins the network monitoring process.
func (nm *NetworkMonitor) Start() {
	logger.Info("Starting network monitor")
	nm.ticker = time.NewTicker(15 * time.Second)
	go func() {
		for {
			select {
			case <-nm.ticker.C:
				nm.checkConnectivity()
			case <-nm.stopChan:
				nm.ticker.Stop()
				return
			}
		}
	}()
}

// Stop halts the network monitoring process.
func (nm *NetworkMonitor) Stop() {
	close(nm.stopChan)
}

func (nm *NetworkMonitor) checkConnectivity() {
	err := exec.Command("ping", "-c", "1", pingTarget).Run()
	if err != nil {
		nm.pingFailures++
		nm.pingSuccesses = 0
		logger.WithField("consecutive_failures", nm.pingFailures).Warn("Ping failed")
		if nm.pingFailures >= consecutiveFailures && !nm.isInSafeMode {
			nm.setSafeMode(true)
		}
	} else {
		nm.pingSuccesses++
		if nm.pingSuccesses >= consecutiveSuccesses {
			nm.pingFailures = 0
		}
		if nm.isInSafeMode {
			nm.setSafeMode(false)
		}
	}
}

func (nm *NetworkMonitor) setSafeMode(enable bool) {
	if enable {
		logger.Warn("Entering SafeMode due to lost connectivity")
		nm.isInSafeMode = true
		// Prepend "SafeMode-" to the SSIDs of the local APs
		if err := nm.connector.SetSafeModeSSID(true); err != nil {
			logger.WithError(err).Error("Failed to set SafeMode SSID")
		}
	} else {
		logger.Info("Exiting SafeMode, connectivity restored")
		nm.isInSafeMode = false
		// Remove "SafeMode-" from the SSIDs of the local APs
		if err := nm.connector.SetSafeModeSSID(false); err != nil {
			logger.WithError(err).Error("Failed to restore normal SSID")
		}
	}
}
