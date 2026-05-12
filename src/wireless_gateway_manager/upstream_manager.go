package wireless_gateway_manager

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Candidate struct {
	Signal    int
	Radio     string
	IfaceName string
	SSID      string
}

type ResellerModeChecker interface {
	IsResellerModeActive() bool
}

type UpstreamManager struct {
	connector  ConnectorInterface
	scanner    ScannerInterface
	reseller   ResellerModeChecker
	config     UpstreamManagerConfig
	stopChan   chan struct{}
	pauseMu    sync.Mutex
	pauseUntil time.Time
	blacklist   map[string]time.Time
	blacklistMu sync.Mutex
	failMu           sync.Mutex
	consecutiveFails int
	cooldownUntil    time.Time
	connectivityCheckFn    func() bool
	isTollGateConnection   bool
}

type ConfigReadResult struct {
	ResellerMode bool
}

func DefaultUpstreamManagerConfig() UpstreamManagerConfig {
	return UpstreamManagerConfig{
		ScanInterval:           300 * time.Second,
		FastCheck:              30 * time.Second,
		LostThreshold:          2,
		HysteresisDB:           12,
		SignalFloor:            -85,
		BlacklistTTL:           60 * time.Minute,
		EmergencyPenalty:       20,
		MaxConsecutiveFailures: 3,
		SwitchCooldown:         10 * time.Minute,
		StartupGracePeriod:     90 * time.Second,
		PostSwitchWait:         5 * time.Second,
		StartupSettle:         15 * time.Second,
		StartupRetryInterval:  10 * time.Second,
		StartupScanInterval:   10 * time.Second,
	}
}

func NewUpstreamManager(connector ConnectorInterface, scanner ScannerInterface, reseller ResellerModeChecker, config UpstreamManagerConfig) *UpstreamManager {
	if config.ScanInterval == 0 {
		config.ScanInterval = 300 * time.Second
	}
	if config.FastCheck == 0 {
		config.FastCheck = 30 * time.Second
	}
	if config.LostThreshold == 0 {
		config.LostThreshold = 2
	}
	if config.HysteresisDB == 0 {
		config.HysteresisDB = 12
	}
	if config.SignalFloor == 0 {
		config.SignalFloor = -85
	}
	if config.BlacklistTTL == 0 {
		config.BlacklistTTL = 60 * time.Minute
	}
	if config.EmergencyPenalty == 0 {
		config.EmergencyPenalty = 20
	}
	if config.MaxConsecutiveFailures == 0 {
		config.MaxConsecutiveFailures = 3
	}
	if config.SwitchCooldown == 0 {
		config.SwitchCooldown = 10 * time.Minute
	}
	if config.StartupGracePeriod == 0 {
		config.StartupGracePeriod = 90 * time.Second
	}
	if config.PostSwitchWait == 0 {
		config.PostSwitchWait = 5 * time.Second
	}
	if config.StartupSettle == 0 {
		config.StartupSettle = 15 * time.Second
	}
	if config.TollGateLostThreshold == 0 {
		config.TollGateLostThreshold = 6
	}
	if config.StartupRetryInterval == 0 {
		config.StartupRetryInterval = 10 * time.Second
	}
	if config.StartupScanInterval == 0 {
		config.StartupScanInterval = 10 * time.Second
	}
	return &UpstreamManager{
		connector: connector,
		scanner:   scanner,
		reseller:  reseller,
		config:    config,
		stopChan:  make(chan struct{}),
		blacklist: make(map[string]time.Time),
	}
}

func (um *UpstreamManager) startupConnectivityCheck() {
	activeSTA, err := um.connector.GetActiveSTA()
	if err != nil || activeSTA == nil {
		logger.WithError(err).Info("Startup check: no active STA, nothing to verify")
		return
	}

	staNetdev := activeSTA.Device
	if netdev, err := um.connector.GetSTANetdev(activeSTA.Name); err == nil && netdev != "" {
		staNetdev = netdev
	}

	logger.WithField("settle_seconds", um.config.StartupSettle.Seconds()).Info("Startup check: waiting for STA to settle")
	time.Sleep(um.config.StartupSettle)

	const startupRetries = 3
	connected := false
	for attempt := 1; attempt <= startupRetries; attempt++ {
		if um.checkConnectivityForStartup(staNetdev) {
			connected = true
			break
		}
		if attempt == 1 {
			logger.Info("Startup check: no internet after settle, nudging netifd with ifup wwan")
			exec.Command("ifup", "wwan").Run()
		}
		if attempt < startupRetries {
			logger.WithFields(logrus.Fields{
				"attempt":   attempt,
				"remaining": startupRetries - attempt,
			}).Info("Startup check: no internet yet, retrying")
			time.Sleep(um.config.StartupRetryInterval)
		}
	}

	if connected {
		logger.WithField("ssid", activeSTA.SSID).Info("Startup check: active STA has internet, all good")
		return
	}

	logger.WithField("ssid", activeSTA.SSID).Warn("Startup check: active STA has no internet, triggering emergency scan")

	const startupScanRetries = 3
	switched := false
	for scanAttempt := 1; scanAttempt <= startupScanRetries; scanAttempt++ {
		logger.WithFields(logrus.Fields{
			"attempt":   scanAttempt,
			"remaining": startupScanRetries - scanAttempt,
		}).Info("Startup check: scanning for alternative upstream")

		um.purgeBlacklist()
		networks, err := um.scanner.ScanAllRadios()
		if err != nil {
			logger.WithError(err).Warn("Startup check: scan failed")
			if scanAttempt < startupScanRetries {
				time.Sleep(um.config.StartupScanInterval)
			}
			continue
		}

		isReseller := um.isResellerModeActive()
		candidate, err := um.findCandidates(networks, isReseller, true)
		if err != nil || candidate == nil {
			logger.Info("Startup check: no candidate found in scan")
			if scanAttempt < startupScanRetries {
				time.Sleep(um.config.StartupScanInterval)
			}
			continue
		}

		logger.WithFields(logrus.Fields{
			"ssid":   candidate.SSID,
			"signal": candidate.Signal,
		}).Info("Startup check: candidate found, switching")

		if err := um.connector.SwitchUpstream(activeSTA.Name, candidate.IfaceName, candidate.SSID); err != nil {
			logger.WithError(err).Warn("Startup check: switch failed")
			um.recordSwitchFailure()
			if scanAttempt < startupScanRetries {
				time.Sleep(um.config.StartupScanInterval)
			}
			continue
		}

		um.resetSwitchFailures()
		um.blacklistSSID(activeSTA.SSID)
		go um.verifyPostSwitchConnectivity(candidate.SSID)
		switched = true
		break
	}

	if !switched {
		logger.Warn("Startup check: no working upstream found after all scan retries, deferring to main loop")
	}
}

func (um *UpstreamManager) checkConnectivityForStartup(staDevice string) bool {
	if um.connectivityCheckFn != nil {
		return um.connectivityCheckFn()
	}
	return um.checkConnectivity(staDevice)
}

func (um *UpstreamManager) Start(ctx context.Context) {
	logger.Info("Starting upstream manager")

	if err := um.connector.EnsureRadiosEnabled(); err != nil {
		logger.WithError(err).Warn("Failed to ensure radios enabled on startup")
	}
	if err := um.connector.EnsureWWANSetup(); err != nil {
		logger.WithError(err).Warn("Failed to ensure wwan setup on startup")
	}
	if err := um.connector.CleanupStaleSTAs(); err != nil {
		logger.WithError(err).Warn("Failed to cleanup stale STAs on startup")
	}

	um.startupConnectivityCheck()

	startupGraceEnd := time.Now().Add(um.config.StartupGracePeriod)
	logger.WithField("grace_seconds", um.config.StartupGracePeriod.Seconds()).Info("Startup grace period active")

	scanCounter := 0
	lostCount := 0
	scanCycles := int(um.config.ScanInterval / um.config.FastCheck)
	if scanCycles < 1 {
		scanCycles = 1
	}

	ticker := time.NewTicker(um.config.FastCheck)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Upstream manager shutting down (context cancelled)")
			return
		case <-um.stopChan:
			logger.Info("Upstream manager shutting down (stop requested)")
			return
		case <-ticker.C:
		}

		if err := um.connector.EnsureRadiosEnabled(); err != nil {
			logger.WithError(err).Warn("Failed to ensure radios enabled")
		}

		inStartupGrace := time.Now().Before(startupGraceEnd)
		if inStartupGrace {
			logger.Debug("Skipping connectivity check during startup grace period")
			continue
		}

		isReseller := um.isResellerModeActive()

		activeSTA, err := um.connector.GetActiveSTA()
		if err != nil {
			logger.WithError(err).Warn("Failed to get active STA")
		}

		var currentSignal int
		shouldScan := false
		reason := "scheduled"

		if activeSTA == nil {
			shouldScan = true
			reason = "no-active-upstream"
		} else {
			staNetdev := activeSTA.Device
			if netdev, err := um.connector.GetSTANetdev(activeSTA.Name); err == nil && netdev != "" {
				staNetdev = netdev
			} else {
				logger.WithFields(logrus.Fields{
					"section": activeSTA.Name,
					"error":   err,
				}).Debug("Could not resolve STA netdev, falling back to radio name")
			}

			associated := um.isSTAAssociated(staNetdev)
			if !associated {
				shouldScan = true
				reason = "not-associated"
			} else {
				currentSignal, _ = um.getCurrentSignal(staNetdev)
	connected := um.checkConnectivityForStartup(staNetdev)
				if connected {
					if lostCount > 0 {
						logger.WithField("lost_count", lostCount).Info("Connectivity restored")
					}
					lostCount = 0
					scanCounter++
					if scanCounter >= scanCycles {
						shouldScan = true
						reason = "scheduled"
					}
			} else {
				if um.isPaused() {
					continue
				}
				lostCount++
				lostThreshold := um.config.LostThreshold
				if um.isTollGateConnection {
					lostThreshold = um.config.TollGateLostThreshold
				}
				logger.WithField("lost_count", lostCount).Info("Connectivity lost")
					if lostCount >= lostThreshold {
						if err := um.connector.CleanupStaleSTAs(); err != nil {
							logger.WithError(err).Warn("Failed to cleanup stale STAs during emergency")
						}
						shouldScan = true
						reason = "emergency"
					}
				}
			}
		}

		if shouldScan {
			activeIface := ""
			activeSSID := ""
			if activeSTA != nil {
				activeIface = activeSTA.Name
				activeSSID = activeSTA.SSID
			}

			logger.WithFields(logrus.Fields{
				"active_ssid": activeSSID,
				"signal":      currentSignal,
				"reason":      reason,
			}).Info("Running upstream scan cycle")

			if err := um.connector.CleanupStaleSTAs(); err != nil {
				logger.WithError(err).Warn("Failed to cleanup stale STAs before switch")
			}

			um.runScanCycle(activeIface, activeSSID, currentSignal, reason, isReseller)
			scanCounter = 0
			lostCount = 0
		}
	}
}

func (um *UpstreamManager) Stop() {
	close(um.stopChan)
}

func (um *UpstreamManager) PauseConnectivityChecks(d time.Duration) {
	um.pauseMu.Lock()
	um.pauseUntil = time.Now().Add(d)
	um.pauseMu.Unlock()
	logger.WithField("duration", d).Info("Pausing connectivity checks")
}

func (um *UpstreamManager) isPaused() bool {
	um.pauseMu.Lock()
	paused := time.Now().Before(um.pauseUntil)
	um.pauseMu.Unlock()
	return paused
}

func (um *UpstreamManager) isInCooldown() bool {
	um.failMu.Lock()
	cooldown := time.Now().Before(um.cooldownUntil)
	um.failMu.Unlock()
	return cooldown
}

func (um *UpstreamManager) recordSwitchFailure() {
	um.failMu.Lock()
	um.consecutiveFails++
	if um.consecutiveFails >= um.config.MaxConsecutiveFailures {
		um.cooldownUntil = time.Now().Add(um.config.SwitchCooldown)
		logger.WithFields(logrus.Fields{
			"failures":        um.consecutiveFails,
			"cooldown_minutes": um.config.SwitchCooldown.Minutes(),
		}).Warn("Circuit breaker triggered: entering cooldown")
	}
	um.failMu.Unlock()
}

func (um *UpstreamManager) resetSwitchFailures() {
	um.failMu.Lock()
	if um.consecutiveFails > 0 {
		um.consecutiveFails = 0
		um.cooldownUntil = time.Time{}
		logger.Info("Switch failure counter reset")
	}
	um.failMu.Unlock()
}

func (um *UpstreamManager) blacklistSSID(ssid string) {
	um.blacklistMu.Lock()
	um.blacklist[ssid] = time.Now()
	um.blacklistMu.Unlock()
	logger.WithField("ssid", ssid).Info("Blacklisted SSID (no internet)")
}

func (um *UpstreamManager) isBlacklisted(ssid string) bool {
	um.blacklistMu.Lock()
	t, exists := um.blacklist[ssid]
	um.blacklistMu.Unlock()
	if !exists {
		return false
	}
	return time.Since(t) < um.config.BlacklistTTL
}

func (um *UpstreamManager) purgeBlacklist() {
	um.blacklistMu.Lock()
	now := time.Now()
	for ssid, t := range um.blacklist {
		if now.Sub(t) >= um.config.BlacklistTTL {
			delete(um.blacklist, ssid)
			logger.WithField("ssid", ssid).Debug("Purged expired blacklist entry")
		}
	}
	um.blacklistMu.Unlock()
}

func (um *UpstreamManager) isResellerModeActive() bool {
	if um.reseller == nil {
		return false
	}
	return um.reseller.IsResellerModeActive()
}

func (um *UpstreamManager) runScanCycle(activeIface, activeSSID string, currentSignal int, reason string, isReseller bool) {
	if um.isInCooldown() {
		logger.WithField("reason", reason).Info("In cooldown period, skipping scan cycle")
		return
	}

	um.purgeBlacklist()

	networks, err := um.scanner.ScanAllRadios()
	if err != nil {
		logger.WithError(err).Warn("Scan failed, retrying next cycle")
		return
	}

	isEmergency := reason == "emergency"

	candidate, err := um.findCandidates(networks, isReseller, isEmergency)
	if err != nil || candidate == nil {
		logger.WithField("reason", reason).Info("No known upstream candidates available")
		return
	}

	logger.WithFields(logrus.Fields{
		"ssid":   candidate.SSID,
		"signal": candidate.Signal,
		"reason": reason,
	}).Info("Best candidate found")

	shouldSwitch := false

	if activeIface == "" {
		shouldSwitch = true
		logger.WithField("reason", reason).Info("No active upstream, connecting")
	} else if currentSignal == 0 {
		shouldSwitch = true
		logger.WithField("reason", reason).Info("Active upstream not associated")
	} else if currentSignal < um.config.SignalFloor {
		shouldSwitch = true
		logger.WithFields(logrus.Fields{
			"signal": currentSignal,
			"floor":  um.config.SignalFloor,
		}).Info("Active signal below floor")
	} else {
		diff := candidate.Signal - currentSignal
		if diff >= um.config.HysteresisDB {
			shouldSwitch = true
			logger.WithFields(logrus.Fields{
				"diff": diff,
			}).Info("Candidate significantly stronger")
		}
	}

	if shouldSwitch {
		if err := um.connector.SwitchUpstream(activeIface, candidate.IfaceName, candidate.SSID); err != nil {
			logger.WithError(err).Warn("Failed to switch upstream")
			um.recordSwitchFailure()
		} else {
			um.resetSwitchFailures()
			if isEmergency && activeSSID != "" {
				um.blacklistSSID(activeSSID)
			}
			go um.verifyPostSwitchConnectivity(candidate.SSID)
		}
	}
}

func (um *UpstreamManager) waitForDefaultRoute(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("ip", "route", "show", "default")
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		routeOutput := out.String()
		if err == nil && strings.Contains(routeOutput, " via ") {
			logger.WithField("route", strings.TrimSpace(routeOutput)).Debug("Post-switch: default route found")
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	logger.WithField("timeout", timeout).Warn("Post-switch: timed out waiting for default route")
	return false
}

func (um *UpstreamManager) verifyPostSwitchConnectivity(ssid string) {
	logger.WithField("ssid", ssid).Info("Post-switch: verifying connectivity")
	if um.waitForDefaultRoute(30 * time.Second) {
		if um.probeTollGateGateway() {
			um.isTollGateConnection = true
			logger.WithField("ssid", ssid).Info("Post-switch: TollGate detected, skipping internet blacklist check")
			return
		}
	}

	um.isTollGateConnection = false
	cmd := exec.Command("ping", "-c", "1", "-W", "5", "9.9.9.9")
	if cmd.Run() != nil {
		um.blacklistSSID(ssid)
		logger.WithField("ssid", ssid).Warn("Blacklisted new upstream: no internet after switch")
	} else {
		logger.WithField("ssid", ssid).Info("Post-switch connectivity verified")
	}
}

func (um *UpstreamManager) probeTollGateGateway() bool {
	cmd := exec.Command("ip", "route", "show", "default")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return false
	}

	fields := strings.Fields(out.String())
	gatewayIP := ""
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			gatewayIP = fields[i+1]
			break
		}
	}
	if gatewayIP == "" {
		return false
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + gatewayIP + ":2121/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func (um *UpstreamManager) findCandidates(networks []NetworkInfo, isReseller bool, isEmergency bool) (*Candidate, error) {
	if isReseller {
		return um.findResellerCandidates(networks, isEmergency)
	}
	return um.findKnownCandidates(networks)
}

func (um *UpstreamManager) findKnownCandidates(networks []NetworkInfo) (*Candidate, error) {
	sections, err := um.connector.GetSTASections()
	if err != nil {
		return nil, err
	}

	knownSSIDs := make(map[string]string)
	for _, section := range sections {
		if section.Disabled {
			knownSSIDs[section.SSID] = section.Name
		}
	}

	if len(knownSSIDs) == 0 {
		return nil, fmt.Errorf("no known upstreams")
	}

	var best *Candidate
	for _, net := range networks {
		ifaceName, ok := knownSSIDs[net.SSID]
		if !ok {
			continue
		}
		if um.isBlacklisted(net.SSID) {
			continue
		}
		if best == nil || net.Signal > best.Signal {
			best = &Candidate{
				Signal:    net.Signal,
				Radio:     net.Radio,
				IfaceName: ifaceName,
				SSID:      net.SSID,
			}
		}
	}

	return best, nil
}

func (um *UpstreamManager) findResellerCandidates(networks []NetworkInfo, isEmergency bool) (*Candidate, error) {
	sections, _ := um.connector.GetSTASections()
	existingSTAs := make(map[string]string)
	disabledSTAs := make(map[string]string)
	activeSSID := ""
	for _, section := range sections {
		existingSTAs[section.SSID] = section.Name
		if section.Disabled {
			disabledSTAs[section.SSID] = section.Name
		} else {
			activeSSID = section.SSID
		}
	}

	type scoredCandidate struct {
		candidate *Candidate
		score     int
	}

	var best *scoredCandidate

	for _, net := range networks {
		if net.SSID == activeSSID {
			continue
		}
		if um.isBlacklisted(net.SSID) {
			continue
		}

		ifaceName, isExisting := existingSTAs[net.SSID]

		isTollGate := strings.HasPrefix(net.SSID, "TollGate-")

		if isTollGate {
			enc := strings.ToLower(net.Encryption)
			if enc != "" && enc != "none" && enc != "open" {
				continue
			}
			if !isExisting {
				radio, err := um.scanner.FindBestRadioForSSID(net.SSID, networks)
				if err != nil {
					logger.WithFields(logrus.Fields{
						"ssid":  net.SSID,
						"error": err,
					}).Debug("No radio found for TollGate SSID")
					continue
				}
				iface, err := um.connector.FindOrCreateSTAForSSID(net.SSID, "", "none", radio)
				if err != nil {
					logger.WithError(err).Warn("Failed to create STA for TollGate SSID")
					continue
				}
				ifaceName = iface
				logger.WithFields(logrus.Fields{
					"ssid":   net.SSID,
					"iface":  ifaceName,
					"radio":  radio,
					"signal": net.Signal,
				}).Info("Created STA for TollGate candidate")
			}
		} else if _, isDisabled := disabledSTAs[net.SSID]; !isDisabled {
			continue
		}

		score := net.Signal
		if isEmergency && isTollGate {
			score -= um.config.EmergencyPenalty
			logger.WithFields(logrus.Fields{
				"ssid":     net.SSID,
				"original": net.Signal,
				"score":    score,
			}).Debug("Penalizing TollGate during emergency scan")
		}

		if best == nil || score > best.score {
			best = &scoredCandidate{
				candidate: &Candidate{
					Signal:    net.Signal,
					Radio:     net.Radio,
					IfaceName: ifaceName,
					SSID:      net.SSID,
				},
				score: score,
			}
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no candidates found (TollGate or known fallback)")
	}
	return best.candidate, nil
}

func (um *UpstreamManager) checkConnectivity(staDevice string) bool {
	if !um.isSTAAssociated(staDevice) {
		return false
	}

	cmd := exec.Command("ping", "-c", "1", "-W", "3", "9.9.9.9")
	return cmd.Run() == nil
}

func (um *UpstreamManager) CheckConnectivity() bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "3", "9.9.9.9")
	return cmd.Run() == nil
}

func (um *UpstreamManager) isSTAAssociated(staDevice string) bool {
	if staDevice == "" {
		return false
	}
	cmd := exec.Command("iwinfo", staDevice, "info")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return false
	}
	output := out.String()
	return strings.Contains(output, "Access Point:") || strings.Contains(output, "Associated with")
}

func (um *UpstreamManager) getCurrentSignal(staDevice string) (int, error) {
	if staDevice == "" {
		return 0, fmt.Errorf("no STA device")
	}

	cmd := exec.Command("iwinfo", staDevice, "assoclist")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() == nil {
		lines := strings.Split(out.String(), "\n")
		if len(lines) > 0 {
			fields := strings.Fields(lines[0])
			for i, f := range fields {
				if f == "dBm" && i > 0 {
					var sig int
					if _, err := fmt.Sscanf(fields[i-1], "%d", &sig); err == nil {
						return sig, nil
					}
				}
			}
		}
	}

	cmd = exec.Command("iwinfo", staDevice, "info")
	out.Reset()
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return 0, fmt.Errorf("failed to get signal from iwinfo info")
	}

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "Signal:") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Signal:" && i+1 < len(fields) {
					sigStr := strings.TrimSuffix(fields[i+1], "dBm")
					var sig int
					if _, err := fmt.Sscanf(sigStr, "%d", &sig); err == nil {
						return sig, nil
					}
				}
			}
		}
	}

	return 0, fmt.Errorf("could not determine signal strength")
}
