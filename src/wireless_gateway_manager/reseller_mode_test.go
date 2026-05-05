package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/mock"
)

type MockConnector struct {
	mock.Mock
}

func (m *MockConnector) Connect(gateway Gateway, password string) error {
	args := m.Called(gateway, password)
	return args.Error(0)
}

func (m *MockConnector) GetConnectedSSID() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockConnector) ExecuteUCI(args ...string) (string, error) {
	callArgs := make([]interface{}, len(args))
	for i, arg := range args {
		callArgs[i] = arg
	}
	result := m.Called(callArgs...)
	return result.String(0), result.Error(1)
}

func (m *MockConnector) Disconnect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) Reconnect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) GetSTASections() ([]STASection, error) {
	args := m.Called()
	return args.Get(0).([]STASection), args.Error(1)
}

func (m *MockConnector) GetActiveSTA() (*STASection, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*STASection), args.Error(1)
}

func (m *MockConnector) FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error) {
	args := m.Called(ssid, passphrase, encryption, radio)
	return args.String(0), args.Error(1)
}

func (m *MockConnector) RemoveDisabledSTA(ssid string) error {
	args := m.Called(ssid)
	return args.Error(0)
}

func (m *MockConnector) SwitchUpstream(activeIface, candidateIface, candidateSSID string) error {
	args := m.Called(activeIface, candidateIface, candidateSSID)
	return args.Error(0)
}

func (m *MockConnector) GetSTADevice(ifaceName string) (string, error) {
	args := m.Called(ifaceName)
	return args.String(0), args.Error(1)
}

func (m *MockConnector) EnsureWWANSetup() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) EnsureRadiosEnabled() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) GetSTANetdev(sectionName string) (string, error) {
	args := m.Called(sectionName)
	return args.String(0), args.Error(1)
}

func (m *MockConnector) CleanupStaleSTAs() error {
	args := m.Called()
	return args.Error(0)
}

type MockScanner struct {
	mock.Mock
}

func (m *MockScanner) ScanAllRadios() ([]NetworkInfo, error) {
	args := m.Called()
	return args.Get(0).([]NetworkInfo), args.Error(1)
}

func (m *MockScanner) GetRadios() ([]string, error) {
	args := m.Called()
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockScanner) DetectEncryption(encryptionStr string) string {
	args := m.Called(encryptionStr)
	return args.String(0)
}

func (m *MockScanner) FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error) {
	args := m.Called(ssid, networks)
	return args.String(0), args.Error(1)
}

type MockVendorElementProcessor struct {
	mock.Mock
}

func (m *MockVendorElementProcessor) ExtractAndScore(network NetworkInfo) (map[string]interface{}, int, error) {
	args := m.Called(network)
	return args.Get(0).(map[string]interface{}), args.Int(1), args.Error(2)
}

func (m *MockVendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	args := m.Called(elements)
	return args.Error(0)
}

func (m *MockVendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	args := m.Called()
	return args.Get(0).(map[string]string), args.Error(1)
}

func TestResellerModeEnabled_ScanAllRadios(t *testing.T) {
	mockScanner := &MockScanner{}
	mockScanner.On("ScanAllRadios").Return([]NetworkInfo{
		{SSID: "TollGate-ABC", BSSID: "00:11:22:33:44:55", Signal: -50, Encryption: "none"},
		{SSID: "OpenNetwork", BSSID: "66:77:88:99:AA:BB", Signal: -60, Encryption: "none"},
		{SSID: "TollGate-XYZ", BSSID: "CC:DD:EE:FF:00:11", Signal: -70, Encryption: "none"},
	}, nil)

	networks, err := mockScanner.ScanAllRadios()
	if err != nil {
		t.Fatalf("ScanAllRadios failed: %v", err)
	}

	tollGateCount := 0
	for _, net := range networks {
		if len(net.SSID) >= 9 && net.SSID[:9] == "TollGate-" {
			tollGateCount++
		}
	}
	if tollGateCount != 2 {
		t.Errorf("Expected 2 TollGate networks, got %d", tollGateCount)
	}

	mockScanner.AssertExpectations(t)
}
