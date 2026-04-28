package wireless_gateway_manager

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
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
	connector ConnectorInterface
	scanner   ScannerInterface
	reseller  ResellerModeChecker
	config    UpstreamManagerConfig
	stopChan  chan struct{}
}

type ConfigReadResult struct {
	ResellerMode bool
}

func DefaultUpstreamManagerConfig() UpstreamManagerConfig {
	return UpstreamManagerConfig{
		ScanInterval:  300 * time.Second,
		FastCheck:     30 * time.Second,
		LostThreshold: 2,
		HysteresisDB:  12,
		SignalFloor:   -85,
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
	return &UpstreamManager{
		connector: connector,
		scanner:   scanner,
		reseller:  reseller,
		config:    config,
		stopChan:  make(chan struct{}),
	}
}

func (um *UpstreamManager) Start(ctx context.Context) {
	logger.Info("Starting upstream manager")

	if err := um.connector.EnsureRadiosEnabled(); err != nil {
		logger.WithError(err).Warn("Failed to ensure radios enabled on startup")
	}
	if err := um.connector.EnsureWWANSetup(); err != nil {
		logger.WithError(err).Warn("Failed to ensure wwan setup on startup")
	}

	time.Sleep(10 * time.Second)

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

		if um.isResellerModeActive() {
			continue
		}

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
			associated := um.isSTAAssociated(activeSTA.Device)
			if !associated {
				shouldScan = true
				reason = "not-associated"
			} else {
				currentSignal, _ = um.getCurrentSignal(activeSTA.Device)
				connected := um.checkConnectivity(activeSTA.Device)
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
					lostCount++
					logger.WithField("lost_count", lostCount).Info("Connectivity lost")
					if lostCount >= um.config.LostThreshold {
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

			um.runScanCycle(activeIface, activeSSID, currentSignal, reason)
			scanCounter = 0
			lostCount = 0
		}
	}
}

func (um *UpstreamManager) Stop() {
	close(um.stopChan)
}

func (um *UpstreamManager) isResellerModeActive() bool {
	if um.reseller == nil {
		return false
	}
	return um.reseller.IsResellerModeActive()
}

func (um *UpstreamManager) runScanCycle(activeIface, activeSSID string, currentSignal int, reason string) {
	networks, err := um.scanner.ScanAllRadios()
	if err != nil {
		logger.WithError(err).Warn("Scan failed, retrying next cycle")
		return
	}

	candidate, err := um.findStrongestCandidate(networks)
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
		}
	}
}

func (um *UpstreamManager) findStrongestCandidate(networks []NetworkInfo) (*Candidate, error) {
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

func (um *UpstreamManager) checkConnectivity(staDevice string) bool {
	if !um.isSTAAssociated(staDevice) {
		return false
	}

	gw := um.getDefaultGateway()
	if gw == "" {
		return false
	}

	cmd := exec.Command("ping", "-c", "1", "-W", "2", gw)
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

func (um *UpstreamManager) getDefaultGateway() string {
	cmd := exec.Command("ip", "route")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "default") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "via" && i+1 < len(fields) {
					return fields[i+1]
				}
			}
		}
	}
	return ""
}
