package wireless_gateway_manager

import (
	"time"
)

const pingFailureThreshold = 3

func NewNetworkMonitor(connector ConnectorInterface, forceScanChan chan struct{}) *NetworkMonitor {
	return &NetworkMonitor{
		connector:     connector,
		ticker:        time.NewTicker(30 * time.Second),
		stopChan:      make(chan struct{}),
		forceScanChan: forceScanChan,
	}
}

func (nm *NetworkMonitor) Start(gatewayManager *GatewayManager) {
	nm.gatewayManager = gatewayManager
	logger.Info("Starting network monitor")
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

func (nm *NetworkMonitor) Stop() {
	logger.Info("Stopping network monitor")
	close(nm.stopChan)
}

func (nm *NetworkMonitor) checkConnectivity() {
	nm.gatewayManager.scanningMutex.Lock()
	isScanning := nm.gatewayManager.isScanning
	nm.gatewayManager.scanningMutex.Unlock()

	if isScanning {
		logger.Info("Gateway scan in progress, skipping connectivity check.")
		return
	}

	if time.Since(nm.gatewayManager.lastConnectionAttempt) < 120*time.Second {
		logger.Info("In grace period after connection attempt, skipping connectivity check.")
		return
	}

	online, err := nm.connector.CheckInternetConnectivity()
	if err != nil {
		// Log the error from the check itself, but still treat it as a failure.
		logger.WithError(err).Warn("An error occurred during the connectivity check")
	}

	if !online {
		nm.pingFailures++
		nm.pingSuccesses = 0
		logger.WithField("consecutive_failures", nm.pingFailures).Warn("Connectivity check failed")
		if nm.pingFailures >= pingFailureThreshold {
			logger.Warn("Connectivity failure threshold reached, forcing a network scan")
			// Non-blocking send
			select {
			case nm.forceScanChan <- struct{}{}:
			default:
			}
		}
	} else {
		nm.pingSuccesses++
		nm.pingFailures = 0
		logger.WithField("consecutive_successes", nm.pingSuccesses).Debug("Connectivity check successful")
	}
}

func (nm *NetworkMonitor) IsConnected() bool {
	return nm.pingSuccesses > 0
}

// Ensure NetworkMonitor implements NetworkMonitorInterface
var _ NetworkMonitorInterface = (*NetworkMonitor)(nil)
