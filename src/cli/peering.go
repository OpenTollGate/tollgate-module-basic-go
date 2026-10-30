package cli

import (
	"fmt"
	"time"
)

// PeeringSession is the CLI-facing, serialisable view of one active upstream
// peer session. The admin board polls it to show what "stacking SATs" is doing.
type PeeringSession struct {
	GatewayIP       string `json:"gateway_ip"`
	InterfaceName   string `json:"interface_name,omitempty"`
	Pubkey          string `json:"pubkey,omitempty"`
	MintURL         string `json:"mint_url,omitempty"`
	Metric          string `json:"metric,omitempty"`
	PricePerStep    uint64 `json:"price_per_step,omitempty"`
	PriceUnit       string `json:"price_unit,omitempty"`
	StepSize        uint64 `json:"step_size,omitempty"`
	TotalAllotment  uint64 `json:"total_allotment"`
	TotalSpentSats  uint64 `json:"total_spent_sats"`
	Usage           uint64 `json:"usage"`
	Remaining       uint64 `json:"remaining"`
	Status          string `json:"status"`
	BootstrapPhase  string `json:"bootstrap_phase,omitempty"`
	BootstrapActive bool   `json:"bootstrap_active,omitempty"`
}

// PeeringStatusProvider is implemented by an adapter around the upstream
// session manager, keeping the CLI package independent of that module.
type PeeringStatusProvider interface {
	PeeringStatus() []PeeringSession
}

func (s *CLIServer) handleUpstreamStatus() CLIResponse {
	data := []PeeringSession{}
	if s.peeringProvider != nil {
		data = s.peeringProvider.PeeringStatus()
	}
	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("%d active peer session(s)", len(data)),
		Data:      data,
		Timestamp: time.Now(),
	}
}

// handleUpstreamDisconnect stops peering and asks the upstream manager to fall
// back to a known good non-TollGate upstream. If no fallback passes its
// connectivity check, the manager keeps the peer active and this returns an
// error rather than silently taking the router offline.
func (s *CLIServer) handleUpstreamDisconnect() CLIResponse {
	if s.upstreamManager == nil {
		return CLIResponse{
			Success:   false,
			Error:     "upstream manager is not available",
			Timestamp: time.Now(),
		}
	}
	// The manager must not race the manual fallback's DHCP wait.
	s.upstreamManager.PauseConnectivityChecks(s.manualPauseDuration())
	ssid, err := s.upstreamManager.FallbackToKnownGoodNonTollGate()
	if err != nil {
		return CLIResponse{
			Success:   false,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}
	}
	return CLIResponse{
		Success:   true,
		Message:   fmt.Sprintf("Stopped peering; fell back to '%s'", ssid),
		Timestamp: time.Now(),
	}
}
