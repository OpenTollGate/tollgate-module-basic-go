package wireless_gateway_manager

type NetworkEvent struct {
	Type      string
	SSID      string
	BSSID     string
	Band      string
	Frequency int
	Signal    int
	Is5G      bool
}

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
	CleanupStaleSTAs() error
}

type ScannerInterface interface {
	ScanAllRadios() ([]NetworkInfo, error)
	GetRadios() ([]string, error)
	DetectEncryption(encryptionStr string) string
	FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error)
}
