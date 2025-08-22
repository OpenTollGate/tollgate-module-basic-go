package wireless_gateway_manager

import (
	"os/exec"
	"time"
)

func NewNetworkMonitor(connector *Connector) *NetworkMonitor {
	return &NetworkMonitor{
		connector: connector,
		ticker:    time.NewTicker(30 * time.Second),
		stopChan:  make(chan struct{}),
	}
}

func (nm *NetworkMonitor) Start() {
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
	err := ping(pingTarget)
	if err != nil {
		nm.pingFailures++
		nm.pingSuccesses = 0
		logger.WithField("consecutive_failures", nm.pingFailures).Warn("Ping failed")
		if nm.pingFailures >= consecutiveFailures && !nm.isInSafeMode {
			logger.Warn("Entering SafeMode due to lost connectivity")
			if err := nm.connector.SetAPSSIDSafeMode(); err != nil {
				logger.WithError(err).Error("Failed to enter SafeMode")
			} else {
				nm.isInSafeMode = true
			}
		}
	} else {
		nm.pingSuccesses++
		nm.pingFailures = 0
		logger.WithField("consecutive_successes", nm.pingSuccesses).Debug("Ping successful")
		if nm.pingSuccesses >= consecutiveSuccesses && nm.isInSafeMode {
			logger.Info("Restoring AP from SafeMode due to restored connectivity")
			if err := nm.connector.RestoreAPSSIDFromSafeMode(); err != nil {
				logger.WithError(err).Error("Failed to restore AP from SafeMode")
			} else {
				nm.isInSafeMode = false
			}
		}
	}
}

func ping(host string) error {
	cmd := exec.Command("ping", "-c", "1", "-W", "5", host)
	return cmd.Run()
}

func (nm *NetworkMonitor) IsConnected() bool {
	return nm.pingSuccesses > 0
}

func (nm *NetworkMonitor) IsInSafeMode() bool {
	return nm.isInSafeMode
}
