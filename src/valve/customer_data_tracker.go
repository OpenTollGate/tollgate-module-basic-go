package valve

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DataBaseline stores the baseline data usage when tracking starts for a customer
type DataBaseline struct {
	Downloaded uint64
	Uploaded   uint64
	Timestamp  time.Time
}

// Customer data tracking state
var (
	dataBaselines      = make(map[string]*DataBaseline)
	dataBaselinesMutex = &sync.RWMutex{}
)

// SetDataBaseline captures the current data usage as a baseline for a MAC address
// This should be called when opening a gate for data-based sessions
func SetDataBaseline(macAddress string) error {
	// Get current stats
	downloaded, uploaded, err := GetClientStats(macAddress)
	if err != nil {
		// Client might not be in ndsctl yet, use zero baseline
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Warn("Could not get initial client stats, using zero baseline")
		downloaded = 0
		uploaded = 0
	}

	baseline := &DataBaseline{
		Downloaded: downloaded,
		Uploaded:   uploaded,
		Timestamp:  time.Now(),
	}

	dataBaselinesMutex.Lock()
	dataBaselines[macAddress] = baseline
	dataBaselinesMutex.Unlock()

	logger.WithFields(logrus.Fields{
		"mac_address":         macAddress,
		"baseline_downloaded": downloaded,
		"baseline_uploaded":   uploaded,
		"baseline_total":      downloaded + uploaded,
	}).Info("Set data baseline for customer tracking")

	return nil
}

// GetDataUsageSinceBaseline returns the data usage since the baseline was set
// Returns the usage in bytes (downloaded + uploaded since baseline)
// Returns error if no baseline exists or if client stats cannot be retrieved
func GetDataUsageSinceBaseline(macAddress string) (usageBytes uint64, err error) {
	dataBaselinesMutex.RLock()
	baseline, exists := dataBaselines[macAddress]
	dataBaselinesMutex.RUnlock()

	if !exists {
		return 0, fmt.Errorf("no data baseline found for MAC %s", macAddress)
	}

	// Get current stats
	currentDownloaded, currentUploaded, err := GetClientStats(macAddress)
	if err != nil {
		return 0, fmt.Errorf("failed to get current stats: %w", err)
	}

	// Calculate usage since baseline
	var usageDownloaded, usageUploaded uint64
	if currentDownloaded >= baseline.Downloaded {
		usageDownloaded = currentDownloaded - baseline.Downloaded
	}
	if currentUploaded >= baseline.Uploaded {
		usageUploaded = currentUploaded - baseline.Uploaded
	}

	totalUsage := usageDownloaded + usageUploaded

	logger.WithFields(logrus.Fields{
		"mac_address":         macAddress,
		"baseline_downloaded": baseline.Downloaded,
		"baseline_uploaded":   baseline.Uploaded,
		"current_downloaded":  currentDownloaded,
		"current_uploaded":    currentUploaded,
		"usage_downloaded":    usageDownloaded,
		"usage_uploaded":      usageUploaded,
		"total_usage":         totalUsage,
	}).Debug("Calculated data usage since baseline")

	return totalUsage, nil
}

// ClearDataBaseline removes the data baseline for a MAC address
// This should be called when closing a gate
func ClearDataBaseline(macAddress string) {
	dataBaselinesMutex.Lock()
	delete(dataBaselines, macAddress)
	dataBaselinesMutex.Unlock()

	logger.WithField("mac_address", macAddress).Debug("Cleared data baseline for customer")
}

// HasDataBaseline checks if a data baseline exists for a MAC address
func HasDataBaseline(macAddress string) bool {
	dataBaselinesMutex.RLock()
	defer dataBaselinesMutex.RUnlock()
	_, exists := dataBaselines[macAddress]
	return exists
}
