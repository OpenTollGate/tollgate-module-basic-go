package config_manager

// MultiWANConfig records optional multi-uplink policy intent. It has no runtime
// side effects; consumers validate and derive policy in the multi_wan package.
type MultiWANConfig struct {
	Enabled          bool                `json:"enabled"`
	Engine           string              `json:"engine"`
	DefaultObjective string              `json:"default_objective"`
	AllowChange      string              `json:"allow_change"`
	MarkBase         string              `json:"mark_base"`
	HealthCheck      MultiWANHealthCheck `json:"health_check"`
	Sampler          MultiWANSampler     `json:"sampler"`
	Eligibility      MultiWANEligibility `json:"eligibility"`
	Uplinks          []MultiWANUplink    `json:"uplinks"`
	Objectives       []MultiWANObjective `json:"objectives"`
}

type MultiWANHealthCheck struct {
	IntervalS int      `json:"interval_s"`
	Down      int      `json:"down"`
	Up        int      `json:"up"`
	TrackIPs  []string `json:"track_ips"`
}

type MultiWANSampler struct {
	WindowS    int `json:"window_s"`
	MinSamples int `json:"min_samples"`
}

type MultiWANEligibility struct {
	MinSamples int     `json:"min_samples"`
	MaxLossPct float64 `json:"max_loss_pct"`
	WeightMax  int     `json:"weight_max"`
}

type MultiWANUplink struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Source string        `json:"source"`
	Cost   *MultiWANCost `json:"cost,omitempty"`
}

type MultiWANCost struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type MultiWANObjective struct {
	ID             string         `json:"id"`
	Label          string         `json:"label"`
	Mode           string         `json:"mode"`
	WeightOverride map[string]int `json:"weight_override,omitempty"`
}
