// Package wan_health reads the upstream Wi-Fi health snapshot produced by the
// fleet collector (see hermes-fleet docs/DFS-DETECTION.md) and turns it into a
// small, portal-friendly status plus an optional toast.
package wan_health

import (
	"encoding/json"
	"fmt"
	"os"
)

// Status is the on-disk contract (contracts/wan-health.schema.json).
type Status struct {
	TS                string  `json:"ts"`
	SSID              string  `json:"ssid"`
	Channel           int     `json:"channel"`
	DFS               bool    `json:"dfs"`
	CSA1h             int     `json:"csa_count_1h"`
	LastCSA           string  `json:"last_csa,omitempty"`
	CarrierLoss1h     int     `json:"carrier_loss_1h"`
	Signal            int     `json:"signal,omitempty"`
	TxBitrate         float64 `json:"tx_bitrate,omitempty"`
	RecommendEthernet bool    `json:"recommend_ethernet"`
	Reason            string  `json:"reason,omitempty"`
}

// Toast is the UI-facing hint. Active=false means the portal should not show
// anything.
type Toast struct {
	Active        bool   `json:"active"`
	Level         string `json:"level,omitempty"`
	Title         string `json:"title,omitempty"`
	Message       string `json:"message,omitempty"`
	SnoozeSeconds int    `json:"snooze_seconds,omitempty"`
}

// IsDFSChannel reports whether a 5 GHz channel requires DFS. The ranges are
// 52-64 and 100-144 (the common ETSI/FCC DFS bands).
func IsDFSChannel(channel int) bool {
	return (channel >= 52 && channel <= 64) || (channel >= 100 && channel <= 144)
}

// Load reads and normalises a wan-health.json snapshot.
func Load(path string) (Status, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if !status.DFS && IsDFSChannel(status.Channel) {
		status.DFS = true
	}
	if status.DFS || status.CSA1h > 0 || status.CarrierLoss1h > 0 {
		status.RecommendEthernet = true
	}
	return status, nil
}

// Toast returns the portal hint for this snapshot.
func (s Status) Toast() Toast {
	if !s.DFS && s.CSA1h == 0 && s.CarrierLoss1h == 0 {
		return Toast{}
	}
	t := Toast{
		Active:        true,
		Level:         "warning",
		Title:         "Upstream Wi-Fi instability",
		SnoozeSeconds: 3600,
	}
	switch {
	case s.DFS || s.CSA1h > 0:
		t.Message = fmt.Sprintf(
			"Upstream Wi-Fi is on a DFS channel and switched channels %d time(s) in the last hour. Ethernet would be more reliable.",
			s.CSA1h,
		)
	case s.CarrierLoss1h > 0:
		t.Message = fmt.Sprintf(
			"Upstream Wi-Fi dropped %d time(s) in the last hour. Ethernet would be more reliable.",
			s.CarrierLoss1h,
		)
	}
	if s.Reason != "" {
		t.Message += " (" + s.Reason + ")"
	}
	return t
}
