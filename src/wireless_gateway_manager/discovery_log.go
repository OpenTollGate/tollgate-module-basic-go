package wireless_gateway_manager

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	logrus "github.com/sirupsen/logrus"
)

const DefaultDiscoveryLogPath = "/tmp/tollgate-discovery.jsonl"
const DiscoveryLogMaxBytes = 1 << 20

type DiscoveryEntry struct {
	Timestamp    time.Time `json:"ts"`
	BSSID        string    `json:"bssid"`
	SSID         string    `json:"ssid"`
	Signal       int       `json:"signal"`
	Radio        string    `json:"radio"`
	IsTollGate   bool      `json:"is_tollgate"`
	PricePerStep int       `json:"price_per_step,omitempty"`
	StepSize     int       `json:"step_size,omitempty"`
	Encryption   string    `json:"encryption,omitempty"`
}

type ProbeEntry struct {
	Timestamp     time.Time `json:"ts"`
	BSSID         string    `json:"bssid"`
	GatewayIP     string    `json:"gateway_ip"`
	RTTMs         int64     `json:"rtt_ms"`
	ResponseBytes int       `json:"response_bytes"`
	Reachable     bool      `json:"reachable"`
}

type KnownTollGate struct {
	BSSID        string    `json:"bssid"`
	SSID         string    `json:"ssid"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	BestSignal   int       `json:"best_signal"`
	WorstSignal  int       `json:"worst_signal"`
	SampleCount  int       `json:"sample_count"`
	PricePerStep int       `json:"price_per_step,omitempty"`
	StepSize     int       `json:"step_size,omitempty"`
	LastRTTMs    int64     `json:"last_rtt_ms,omitempty"`
	BestRTTMs    int64     `json:"best_rtt_ms,omitempty"`
	ProbeCount   int       `json:"probe_count,omitempty"`
}

type DiscoverySummary struct {
	TotalScans     int             `json:"total_scans"`
	TollGateCount  int             `json:"tollgate_count"`
	RegularCount   int             `json:"regular_count"`
	KnownTollGates []KnownTollGate `json:"known_tollgates"`
}

type DiscoveryLogger struct {
	mu       sync.Mutex
	path     string
	registry map[string]*KnownTollGate
	scans    int
	logger   *logrus.Entry
}

func NewDiscoveryLogger(path string) *DiscoveryLogger {
	if path == "" {
		path = DefaultDiscoveryLogPath
	}
	return &DiscoveryLogger{
		path:     path,
		registry: make(map[string]*KnownTollGate),
		logger:   logger.WithField("module", "discovery_log"),
	}
}

func (dl *DiscoveryLogger) LogScan(networks []NetworkInfo) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	dl.scans++

	tgCount := 0
	for _, net := range networks {
		if net.IsTollGate {
			tgCount++
		}
		dl.updateRegistry(net)
	}

	entries := make([]DiscoveryEntry, 0, len(networks))
	now := time.Now()
	for _, net := range networks {
		entries = append(entries, DiscoveryEntry{
			Timestamp:    now,
			BSSID:        net.BSSID,
			SSID:         net.SSID,
			Signal:       net.Signal,
			Radio:        net.Radio,
			IsTollGate:   net.IsTollGate,
			PricePerStep: net.PricePerStep,
			StepSize:     net.StepSize,
			Encryption:   net.Encryption,
		})
	}

	if err := dl.appendJSONL(entries); err != nil {
		dl.logger.WithError(err).Warn("Failed to append discovery log")
	}

	if tgCount > 0 {
		dl.logger.WithFields(logrus.Fields{
			"total":       len(networks),
			"tollgates":   tgCount,
			"scan_number": dl.scans,
		}).Info("Discovery scan logged")
	}
}

func (dl *DiscoveryLogger) LogProbe(bssid string, result ProbeResult) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	entry := ProbeEntry{
		Timestamp:     time.Now(),
		BSSID:         bssid,
		GatewayIP:     result.GatewayIP,
		RTTMs:         result.RTT.Milliseconds(),
		ResponseBytes: result.ResponseBytes,
		Reachable:     result.Reachable,
	}

	probeLogPath := dl.path + ".probes"
	f, err := os.OpenFile(probeLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		dl.logger.WithError(err).Warn("Failed to append probe log")
	} else {
		enc := json.NewEncoder(f)
		enc.Encode(entry)
		f.Close()
	}

	if existing, ok := dl.registry[bssid]; ok {
		existing.LastRTTMs = entry.RTTMs
		existing.ProbeCount++
		if existing.BestRTTMs == 0 || entry.RTTMs < existing.BestRTTMs {
			existing.BestRTTMs = entry.RTTMs
		}
	}

	if result.Reachable {
		dl.logger.WithFields(logrus.Fields{
			"bssid":  bssid,
			"rtt_ms": entry.RTTMs,
			"bytes":  entry.ResponseBytes,
		}).Info("TollGate probed")
	}
}

func (dl *DiscoveryLogger) updateRegistry(net NetworkInfo) {
	existing, ok := dl.registry[net.BSSID]
	if !ok {
		dl.registry[net.BSSID] = &KnownTollGate{
			BSSID:        net.BSSID,
			SSID:         net.SSID,
			FirstSeen:    time.Now(),
			LastSeen:     time.Now(),
			BestSignal:   net.Signal,
			WorstSignal:  net.Signal,
			SampleCount:  1,
			PricePerStep: net.PricePerStep,
			StepSize:     net.StepSize,
		}
		return
	}

	existing.LastSeen = time.Now()
	existing.SampleCount++
	if net.Signal > existing.BestSignal {
		existing.BestSignal = net.Signal
	}
	if existing.WorstSignal == 0 || net.Signal < existing.WorstSignal {
		existing.WorstSignal = net.Signal
	}
	if net.PricePerStep > 0 {
		existing.PricePerStep = net.PricePerStep
	}
	if net.StepSize > 0 {
		existing.StepSize = net.StepSize
	}
}

func (dl *DiscoveryLogger) appendJSONL(entries []DiscoveryEntry) error {
	if info, err := os.Stat(dl.path); err == nil && info.Size() > DiscoveryLogMaxBytes {
		os.Rename(dl.path, dl.path+".old")
		dl.logger.WithField("rotated_bytes", info.Size()).Debug("Discovery log rotated")
	}

	f, err := os.OpenFile(dl.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func (dl *DiscoveryLogger) Summary() DiscoverySummary {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	tgCount := 0
	regularCount := 0
	known := make([]KnownTollGate, 0)

	for _, v := range dl.registry {
		if v.PricePerStep > 0 || hasTollGateSSID(v.SSID) {
			tgCount++
		} else {
			regularCount++
		}
		known = append(known, *v)
	}

	return DiscoverySummary{
		TotalScans:     dl.scans,
		TollGateCount:  tgCount,
		RegularCount:   regularCount,
		KnownTollGates: known,
	}
}

func (dl *DiscoveryLogger) LoadExistingLog() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	data, err := os.ReadFile(dl.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry DiscoveryEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		dl.scans++
		dl.updateRegistry(NetworkInfo{
			BSSID:        entry.BSSID,
			SSID:         entry.SSID,
			Signal:       entry.Signal,
			Radio:        entry.Radio,
			IsTollGate:   entry.IsTollGate,
			PricePerStep: entry.PricePerStep,
			StepSize:     entry.StepSize,
			Encryption:   entry.Encryption,
		})
	}

	dl.logger.WithField("entries_loaded", len(lines)).Info("Discovery log loaded")
	return nil
}

func hasTollGateSSID(ssid string) bool {
	return len(ssid) >= 9 && ssid[:9] == "TollGate-"
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
