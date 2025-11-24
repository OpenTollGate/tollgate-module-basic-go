package wireless_gateway_manager

import (
	"os/exec"
	"time"
)

const pingFailureThreshold = 3

func NewNetworkMonitor(forceScanChan chan struct{}) *NetworkMonitor {
	return &NetworkMonitor{
		ticker:        time.NewTicker(30 * time.Second),
		stopChan:      make(chan struct{}),
		forceScanChan: forceScanChan,
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
		if nm.pingFailures >= pingFailureThreshold {
			logger.Warn("Ping failure threshold reached, forcing a network scan")
			// Non-blocking send
			select {
			case nm.forceScanChan <- struct{}{}:
			default:
			}
		}
	} else {
		nm.pingSuccesses++
		nm.pingFailures = 0
		logger.WithField("consecutive_successes", nm.pingSuccesses).Debug("Ping successful")
	}
}

func ping(host string) error {
	cmd := exec.Command("ping", "-c", "1", "-W", "5", host)
	return cmd.Run()
}

func (nm *NetworkMonitor) IsConnected() bool {
	return nm.pingSuccesses > 0
}


// Ensure NetworkMonitor implements NetworkMonitorInterface
var _ NetworkMonitorInterface = (*NetworkMonitor)(nil)