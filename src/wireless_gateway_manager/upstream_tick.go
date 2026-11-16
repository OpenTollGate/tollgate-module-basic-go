package wireless_gateway_manager

import (
	"time"

	"github.com/sirupsen/logrus"
)

type upstreamLoopState struct {
	scanCounter int
	lostCount   int
}

// runConnectivityTick is one iteration of Start's control loop. Observation
// precedes radio maintenance so even the first portal verdict prevents churn.
func (um *UpstreamManager) runConnectivityTick(startupGraceEnd time.Time, state *upstreamLoopState) {
	generation := um.connectionGeneration.Load()
	maintain := func() {
		um.tryOperation(func() {
			if err := um.connector.EnsureRadiosEnabled(); err != nil {
				logger.WithError(err).Warn("Failed to ensure radios enabled")
			}
		})
	}
	inStartupGrace := um.now().Before(startupGraceEnd)
	if inStartupGrace && um.portalLoginDisposition(generation) == portalOrdinary {
		maintain()
		logger.Debug("Skipping connectivity check during startup grace period")
		return
	}
	active, err := um.connector.GetActiveSTA()
	if err != nil {
		logger.WithError(err).Warn("Failed to get active STA")
	}
	id := upstreamIdentity{generation: generation}
	var signal int
	reason := "scheduled"
	shouldScan := false
	disposition := portalOrdinary
	if active == nil {
		disposition = um.observePortalLogin(id, false, ConnectivityResult{Reason: "no-active-upstream"})
		shouldScan, reason = true, "no-active-upstream"
	} else {
		id.iface, id.ssid = active.Name, active.SSID
		device := active.Device
		if netdev, err := um.connector.GetSTANetdev(active.Name); err == nil && netdev != "" {
			device = netdev
		}
		associated := um.isSTAAssociated(device)
		if !associated {
			disposition = um.observePortalLogin(id, false, ConnectivityResult{Reason: "not-associated"})
			shouldScan, reason = true, "not-associated"
		} else {
			id.bssid = um.getAssociatedBSSID(device)
			result := um.probeConnectivityForStartup(device)
			if um.connectionGeneration.Load() != generation {
				return
			}
			disposition = um.observePortalLogin(id, um.isSTAAssociated(device) && result.Reason != "not-associated", result)
			switch {
			case disposition == portalHold:
				state.lostCount = 0
				return
			case disposition == portalFallback:
				shouldScan, reason = true, portalExpiredReason
			case disposition == portalSuperseded || disposition == portalDeferred:
				return
			case result.OK:
				if state.lostCount > 0 {
					logger.WithField("lost_count", state.lostCount).Info("Connectivity restored")
				}
				state.lostCount = 0
				state.scanCounter++
				scanCycles := int(um.config.ScanInterval / um.config.FastCheck)
				if scanCycles < 1 {
					scanCycles = 1
				}
				shouldScan = state.scanCounter >= scanCycles
			default:
				if um.isPaused() {
					return
				}
				state.lostCount++
				threshold := um.config.LostThreshold
				if um.isTollGateConnection.Load() {
					threshold = um.config.TollGateLostThreshold
				}
				logger.WithField("lost_count", state.lostCount).Info("Connectivity lost")
				if state.lostCount >= threshold {
					shouldScan, reason = true, "emergency"
				}
			}
			signal, _ = um.getCurrentSignal(device)
		}
	}
	if disposition == portalSuperseded || disposition == portalDeferred {
		return
	}
	maintain()
	// A login episode keeps probing during grace so success can clear it,
	// while the existing grace still defers every switch/scan opportunity.
	if inStartupGrace {
		if reason == portalExpiredReason {
			um.portalFallbackDeferred("startup-grace")
		}
		return
	}
	if !shouldScan {
		return
	}
	// Manual joins remain authoritative, including a mid-join unassociated STA.
	if um.isPaused() {
		if reason == portalExpiredReason {
			um.portalFallbackDeferred("manual-pause")
		}
		return
	}
	logger.WithFields(logrus.Fields{"active_ssid": id.ssid, "signal": signal, "reason": reason}).Info("Running upstream scan cycle")
	// A timed-out login with no saved alternative must retain the current
	// association. Do not run destructive duplicate-STA cleanup in this lane.
	if reason != portalExpiredReason {
		um.tryOperation(func() {
			if err := um.connector.CleanupStaleSTAs(); err != nil {
				logger.WithError(err).Warn("Failed to cleanup stale STAs before switch")
			}
		})
	}
	if um.connectionGeneration.Load() == generation {
		um.runScanCycleGeneration(generation, id.iface, id.ssid, id.bssid, signal, reason, um.isResellerModeActive())
	}
	state.scanCounter, state.lostCount = 0, 0
}
