package wireless_gateway_manager

import (
	"time"
)

type STASection struct {
	Name       string
	SSID       string
	Device     string
	Encryption string
	Disabled   bool
}

type UpstreamManagerConfig struct {
	ScanInterval           time.Duration
	FastCheck              time.Duration
	LostThreshold          int
	TollGateLostThreshold  int
	HysteresisDB           int
	SignalFloor            int
	BlacklistTTL           time.Duration
	EmergencyPenalty       int
	MaxConsecutiveFailures int
	SwitchCooldown         time.Duration
	StartupGracePeriod     time.Duration
	PostSwitchWait         time.Duration
	StartupSettle         time.Duration
	StartupRetryInterval  time.Duration
	StartupScanInterval   time.Duration
}

type Connector struct {
	DHCPTimeout time.Duration
}

type Scanner struct {
	Connector *Connector
}

type NetworkInfo struct {
	BSSID        string
	SSID         string
	Signal       int
	Encryption   string
	PricePerStep int
	StepSize     int
	RawIEs       []byte
	Radio        string
}

type VendorElementProcessor struct {
	connector *Connector
}

type Gateway struct {
	BSSID          string            `json:"bssid"`
	SSID           string            `json:"ssid"`
	Signal         int               `json:"signal"`
	Encryption     string            `json:"encryption"`
	PricePerStep   int               `json:"price_per_step"`
	StepSize       int               `json:"step_size"`
	Score          int               `json:"score"`
	VendorElements map[string]string `json:"vendor_elements"`
}
