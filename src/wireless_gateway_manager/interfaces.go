// Package wireless_gateway_manager defines interfaces for dependency injection.
package wireless_gateway_manager

// NetworkEvent represents a network event.
type NetworkEvent struct {
	Type      string
	SSID      string
	BSSID     string
	Band      string
	Frequency int
	Signal    int
	Is5G      bool
}

// ConnectorInterface defines the methods for network connection operations.
type ConnectorInterface interface {
	Connect(gateway Gateway, password string) error
	GetConnectedSSID() (string, error)
	Disconnect() error
	Reconnect() error
	ExecuteUCI(args ...string) (string, error)
	GetSTASections() ([]STASection, error)
	GetActiveSTA() (*STASection, error)
	FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error)
	RemoveDisabledSTA(ssid string) error
	SwitchUpstream(activeIface, candidateIface, candidateSSID string) error
	GetSTADevice(ifaceName string) (string, error)
	EnsureWWANSetup() error
	EnsureRadiosEnabled() error
	GetSTANetdev(sectionName string) (string, error)
}

// ScannerInterface defines the methods for network scanning operations.
type ScannerInterface interface {
	ScanAllRadios() ([]NetworkInfo, error)
	GetRadios() ([]string, error)
	DetectEncryption(encryptionStr string) string
	FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error)
}

// NetworkMonitorInterface defines the methods for network monitoring operations.
type NetworkMonitorInterface interface {
	Start()
	Stop()
	IsConnected() bool
}

// VendorElementProcessorInterface defines the methods for vendor element processing.
type VendorElementProcessorInterface interface {
	ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error)
	SetLocalAPVendorElements(elements map[string]string) error
	GetLocalAPVendorElements() (map[string]string, error)
}
