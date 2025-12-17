package chandler

import (
	"fmt"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/sirupsen/logrus"
)

// CustomerSessionTracker tracks data usage for a downstream customer session.
type CustomerSessionTracker struct {
	macAddress     string
	interfaceName  string
	allotmentBytes uint64
	startBytes     uint64
	done           chan bool
	mu             sync.RWMutex
}

// NewCustomerSessionTracker creates a new tracker for customer sessions.
func NewCustomerSessionTracker(macAddress, interfaceName string, allotmentBytes uint64) *CustomerSessionTracker {
	return &CustomerSessionTracker{
		macAddress:     macAddress,
		interfaceName:  interfaceName,
		allotmentBytes: allotmentBytes,
		done:           make(chan bool),
	}
}

// Start begins monitoring the data usage for the customer.
func (t *CustomerSessionTracker) Start() error {
	initialBytes, err := getInterfaceBytes(t.interfaceName)
	if err != nil {
		return fmt.Errorf("failed to get initial byte count for %s: %w", t.interfaceName, err)
	}

	t.mu.Lock()
	t.startBytes = initialBytes
	t.mu.Unlock()

	go t.monitor()

	logger.WithFields(logrus.Fields{
		"mac_address": t.macAddress,
		"interface":   t.interfaceName,
		"allotment":   utils.BytesToHumanReadable(t.allotmentBytes),
		"start_bytes": utils.BytesToHumanReadable(initialBytes),
	}).Info("Started customer session tracker.")

	return nil
}

// Stop halts the monitoring of the session.
func (t *CustomerSessionTracker) Stop() {
	close(t.done)
}

func (t *CustomerSessionTracker) monitor() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			logger.WithField("mac_address", t.macAddress).Info("Customer session tracker stopped.")
			return
		case <-ticker.C:
			currentBytes, err := getInterfaceBytes(t.interfaceName)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"mac_address": t.macAddress,
					"interface":   t.interfaceName,
					"error":       err,
				}).Error("Failed to get current bytes for customer session")
				continue
			}

			t.mu.RLock()
			startBytes := t.startBytes
			allotment := t.allotmentBytes
			t.mu.RUnlock()

			var usage uint64
			if currentBytes >= startBytes {
				usage = currentBytes - startBytes
			}

			if usage >= allotment {
				logger.WithFields(logrus.Fields{
					"mac_address": t.macAddress,
					"usage":       utils.BytesToHumanReadable(usage),
					"allotment":   utils.BytesToHumanReadable(allotment),
				}).Info("Customer data allotment reached. Closing gate.")

				if err := valve.CloseGate(t.macAddress); err != nil {
					logger.WithFields(logrus.Fields{
						"mac_address": t.macAddress,
						"error":       err,
					}).Error("Failed to close gate for customer")
				}
				t.Stop()
				return
			}
		}
	}
}