package valve

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ErrClientCountersUnavailable reports that ndsctl could not answer with the
// client's byte counters (`ndsctl json <mac>`): NoDogSplash has no record of the
// client yet, or the command failed. It is returned alongside a recorded
// baseline rather than instead of one — see SetDataBaseline.
var ErrClientCountersUnavailable = errors.New("client counters unavailable")

// ErrDataBaselineMissing reports that no metering baseline exists for a MAC, so
// its usage cannot be measured.
var ErrDataBaselineMissing = errors.New("no data baseline")

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
//
// It ALWAYS records a baseline, because a bytes session with no baseline is a
// session nothing can meter (and its gate therefore stays open unmetered). When
// ndsctl cannot report the client's counters the baseline is zero — metering
// starts from the gate opening — and ErrClientCountersUnavailable is returned so
// the caller can escalate the gap. Callers must not fail a customer's paid grant
// over that error: the money is spent and the gate is open, so the honest
// reaction is to make the gap observable, not to take the access away.
func SetDataBaseline(macAddress string) error {
	// Get current stats
	downloaded, uploaded, err := GetClientStats(macAddress)
	if err != nil {
		// Client might not be in ndsctl yet, use zero baseline
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Could not read the client's counters: recording the metering baseline from zero (metering starts at the gate opening)")
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

	if err != nil {
		return fmt.Errorf("%w for MAC %s: %v", ErrClientCountersUnavailable, macAddress, err)
	}
	return nil
}

// GetDataUsageSinceBaseline returns the data usage since the baseline was set
// Returns the usage in bytes (downloaded + uploaded since baseline)
//
// It returns ErrDataBaselineMissing when there is no baseline, and
// ErrClientCountersUnavailable when ndsctl cannot report the client's counters.
// The earlier contract answered 0 usage for that second case, which made a
// client the module cannot see indistinguishable from an idle one: a session
// metered against a permanent 0 never reaches its allotment, so its gate was
// never closed and the customer's usage was never counted (C1-2c). Callers must
// handle the error — re-establishing the baseline or closing a gate that cannot
// be metered — instead of treating it as "no usage yet".
func GetDataUsageSinceBaseline(macAddress string) (usageBytes uint64, err error) {
	dataBaselinesMutex.RLock()
	baseline, exists := dataBaselines[macAddress]
	dataBaselinesMutex.RUnlock()

	if !exists {
		return 0, fmt.Errorf("%w found for MAC %s", ErrDataBaselineMissing, macAddress)
	}

	// Get current stats
	currentDownloaded, currentUploaded, err := GetClientStats(macAddress)
	if err != nil {
		// The client is not in ndsctl (yet), or ndsctl failed: there is no usage
		// figure at all, which is not the same thing as zero usage.
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Debug("Could not read the client's counters for metering")
		return 0, fmt.Errorf("%w for MAC %s: %v", ErrClientCountersUnavailable, macAddress, err)
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
