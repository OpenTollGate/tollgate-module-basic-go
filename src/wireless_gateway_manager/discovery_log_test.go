package wireless_gateway_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiscoveryLogger_LogScan(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "discovery.jsonl")
	dl := NewDiscoveryLogger(logPath)

	networks := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-Alpha", Signal: -55, IsTollGate: true, PricePerStep: 1, StepSize: 22020096, Radio: "radio0"},
		{BSSID: "aa:bb:cc:dd:ee:02", SSID: "HomeWiFi", Signal: -70, IsTollGate: false, Radio: "radio0"},
		{BSSID: "aa:bb:cc:dd:ee:03", SSID: "TollGate-Beta", Signal: -62, IsTollGate: true, PricePerStep: 2, StepSize: 22020096, Radio: "radio1"},
	}

	dl.LogScan(networks)

	data, err := os.ReadFile(logPath)
	assert.NoError(t, err)

	lines := splitLines(data)
	assert.Equal(t, 3, len(lines), "should write one JSONL line per network")

	var first DiscoveryEntry
	assert.NoError(t, json.Unmarshal(lines[0], &first))
	assert.Equal(t, "TollGate-Alpha", first.SSID)
	assert.Equal(t, true, first.IsTollGate)
	assert.Equal(t, -55, first.Signal)
	assert.Equal(t, 1, first.PricePerStep)
}

func TestDiscoveryLogger_RegistryAccumulation(t *testing.T) {
	dl := NewDiscoveryLogger(filepath.Join(t.TempDir(), "test.jsonl"))

	scan1 := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-X", Signal: -60, IsTollGate: true, PricePerStep: 1},
	}
	scan2 := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-X", Signal: -50, IsTollGate: true, PricePerStep: 1},
		{BSSID: "aa:bb:cc:dd:ee:02", SSID: "TollGate-Y", Signal: -65, IsTollGate: true, PricePerStep: 3},
	}

	dl.LogScan(scan1)
	dl.LogScan(scan2)

	summary := dl.Summary()
	assert.Equal(t, 2, summary.TotalScans)
	assert.Equal(t, 2, summary.TollGateCount)

	var xFound, yFound bool
	for _, tg := range summary.KnownTollGates {
		if tg.BSSID == "aa:bb:cc:dd:ee:01" {
			xFound = true
			assert.Equal(t, 2, tg.SampleCount, "TollGate-X should have 2 samples")
			assert.Equal(t, -50, tg.BestSignal, "best signal should be -50")
			assert.Equal(t, -60, tg.WorstSignal, "worst signal should be -60")
		}
		if tg.BSSID == "aa:bb:cc:dd:ee:02" {
			yFound = true
			assert.Equal(t, 1, tg.SampleCount)
		}
	}
	assert.True(t, xFound, "TollGate-X should be in registry")
	assert.True(t, yFound, "TollGate-Y should be in registry")
}

func TestDiscoveryLogger_LoadExistingLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "existing.jsonl")

	networks := []NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-Old", Signal: -58, IsTollGate: true, PricePerStep: 1},
	}
	dl1 := NewDiscoveryLogger(logPath)
	dl1.LogScan(networks)

	dl2 := NewDiscoveryLogger(logPath)
	err := dl2.LoadExistingLog()
	assert.NoError(t, err)

	summary := dl2.Summary()
	assert.Equal(t, 1, summary.TotalScans)
	assert.Equal(t, 1, summary.TollGateCount)
	assert.Equal(t, 1, len(summary.KnownTollGates))
	assert.Equal(t, "TollGate-Old", summary.KnownTollGates[0].SSID)
}

func TestDiscoveryLogger_EmptyScan(t *testing.T) {
	dl := NewDiscoveryLogger(filepath.Join(t.TempDir(), "empty.jsonl"))
	dl.LogScan([]NetworkInfo{})

	summary := dl.Summary()
	assert.Equal(t, 1, summary.TotalScans)
	assert.Equal(t, 0, summary.TollGateCount)
	assert.Equal(t, 0, len(summary.KnownTollGates))
}

func TestDiscoveryLogger_PriceUpdate(t *testing.T) {
	dl := NewDiscoveryLogger(filepath.Join(t.TempDir(), "price.jsonl"))

	dl.LogScan([]NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-X", Signal: -60, IsTollGate: true, PricePerStep: 1},
	})
	dl.LogScan([]NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-X", Signal: -62, IsTollGate: true, PricePerStep: 5},
	})

	summary := dl.Summary()
	assert.Equal(t, 1, len(summary.KnownTollGates))
	assert.Equal(t, 5, summary.KnownTollGates[0].PricePerStep, "should track latest price")
}

func TestDiscoveryLogger_LogProbe(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "discovery.jsonl")
	dl := NewDiscoveryLogger(logPath)

	dl.LogScan([]NetworkInfo{
		{BSSID: "aa:bb:cc:dd:ee:01", SSID: "TollGate-X", Signal: -55, IsTollGate: true},
	})

	dl.LogProbe("aa:bb:cc:dd:ee:01", ProbeResult{
		GatewayIP:     "10.0.0.1",
		RTT:           15 * time.Millisecond,
		ResponseBytes: 512,
		Reachable:     true,
	})

	summary := dl.Summary()
	assert.Equal(t, 1, len(summary.KnownTollGates))
	tg := summary.KnownTollGates[0]
	assert.Equal(t, int64(15), tg.LastRTTMs)
	assert.Equal(t, int64(15), tg.BestRTTMs)
	assert.Equal(t, 1, tg.ProbeCount)

	dl.LogProbe("aa:bb:cc:dd:ee:01", ProbeResult{
		GatewayIP:     "10.0.0.1",
		RTT:           8 * time.Millisecond,
		ResponseBytes: 512,
		Reachable:     true,
	})

	summary = dl.Summary()
	tg = summary.KnownTollGates[0]
	assert.Equal(t, int64(8), tg.LastRTTMs)
	assert.Equal(t, int64(8), tg.BestRTTMs)
	assert.Equal(t, 2, tg.ProbeCount)
}
