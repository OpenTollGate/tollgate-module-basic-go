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
	StartupSettle          time.Duration
	StartupRetryInterval   time.Duration
	StartupScanInterval    time.Duration
	VendorIEDiscovery      bool
}

type Connector struct {
	DHCPTimeout time.Duration

	// runUCI runs one uci invocation; nil means the real binary (router
	// state in /etc/config/wireless). Tests inject an in-memory fake so the
	// STA convergence logic is exercised for real (#817 — the mock-level
	// tests asserted canned returns and never touched it).
	runUCI func(args ...string) (string, error)
}

type Scanner struct {
	Connector *Connector
	// vendorIEDiscovery enables the `iw` scan pass that supplies information
	// elements. `iwinfo` prints NO elements (measured on tollgate-A04E,
	// 2026-10-16), so without this the vendor element — and therefore
	// IsTollGate/PricePerStep/StepSize — can never be populated. Wired from
	// config.VendorIEDiscovery via NewScanner.
	vendorIEDiscovery bool
}

// NewScanner builds a Scanner with vendor-element discovery wired from config,
// so the flag that gated this producer is actually read.
func NewScanner(c *Connector, vendorIEDiscovery bool) *Scanner {
	return &Scanner{Connector: c, vendorIEDiscovery: vendorIEDiscovery}
}

// SetVendorIEDiscovery flips the iw-scan pass on an existing Scanner.
func (s *Scanner) SetVendorIEDiscovery(on bool) { s.vendorIEDiscovery = on }

type NetworkInfo struct {
	BSSID        string
	SSID         string
	Signal       int
	Encryption   string
	PricePerStep int
	StepSize     int
	RawIEs       []byte
	Radio        string
	Band         string // "2g"/"5g", or "unknown" when no band info exists
	IsTollGate   bool
}

type TollGateAdvertisement struct {
	Version     uint8
	IsReseller  bool
	HasInternet bool
	OpenNetwork bool
	MintURL     string
	Pubkey      []byte
	// PricePerStep and StepSize are the pricing hint. Zero means "not
	// advertised in the IE" — the signed kind-10021 advertisement on :2121
	// is the authority for price, and a zero here must be read as unknown,
	// never as free.
	PricePerStep uint16
	StepSize     uint16
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
