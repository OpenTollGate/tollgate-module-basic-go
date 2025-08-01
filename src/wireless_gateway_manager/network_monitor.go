package wireless_gateway_manager

import (
	"log"
	"os/exec"
	"time"
)

const (
	pingTarget           = "8.8.8.8"
	consecutiveFailures  = 5
	consecutiveSuccesses = 5
)

type NetworkMonitor struct {
	log           *log.Logger
	connector     *Connector
	pingFailures  int
	pingSuccesses int
	isAPDisabled  bool
	ticker        *time.Ticker
	stopChan      chan struct{}
}

func NewNetworkMonitor(logger *log.Logger, connector *Connector) *NetworkMonitor {
	return &NetworkMonitor{
		log:       logger,
		connector: connector,
		ticker:    time.NewTicker(30 * time.Second),
		stopChan:  make(chan struct{}),
	}
}

func (nm *NetworkMonitor) Start() {
	nm.log.Println("[wireless_gateway_manager] Starting network monitor")
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
	nm.log.Println("[wireless_gateway_manager] Stopping network monitor")
	close(nm.stopChan)
}

func (nm *NetworkMonitor) checkConnectivity() {
	err := ping(pingTarget)
	if err != nil {
		nm.pingFailures++
		nm.pingSuccesses = 0
		nm.log.Printf("[wireless_gateway_manager] Ping failed. Consecutive failures: %d", nm.pingFailures)
		if nm.pingFailures >= consecutiveFailures && !nm.isAPDisabled {
			nm.log.Println("[wireless_gateway_manager] Disabling local AP due to lost connectivity")
			if err := nm.connector.DisableLocalAP(); err != nil {
				nm.log.Printf("[wireless_gateway_manager] ERROR: Failed to disable local AP: %v", err)
			} else {
				nm.isAPDisabled = true
			}
		}
	} else {
		nm.pingSuccesses++
		nm.pingFailures = 0
		nm.log.Printf("[wireless_gateway_manager] Ping successful. Consecutive successes: %d", nm.pingSuccesses)
		if nm.pingSuccesses >= consecutiveSuccesses && nm.isAPDisabled {
			nm.log.Println("[wireless_gateway_manager] Re-enabling local AP due to restored connectivity")
			if err := nm.connector.EnableLocalAP(); err != nil {
				nm.log.Printf("[wireless_gateway_manager] ERROR: Failed to enable local AP: %v", err)
			} else {
				nm.isAPDisabled = false
			}
		}
	}
}

func ping(host string) error {
	cmd := exec.Command("ping", "-c", "1", "-W", "5", host)
	return cmd.Run()
}
