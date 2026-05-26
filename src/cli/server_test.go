package cli

import (
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockCLIConnector struct {
	mock.Mock
}

func (m *MockCLIConnector) Connect(gateway wireless_gateway_manager.Gateway, password string) error {
	return m.Called(gateway, password).Error(0)
}
func (m *MockCLIConnector) GetConnectedSSID() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}
func (m *MockCLIConnector) Disconnect() error                    { return m.Called().Error(0) }
func (m *MockCLIConnector) Reconnect() error                     { return m.Called().Error(0) }
func (m *MockCLIConnector) ExecuteUCI(args ...string) (string, error) {
	ia := make([]interface{}, len(args))
	for i, a := range args {
		ia[i] = a
	}
	args2 := m.Called(ia...)
	return args2.String(0), args2.Error(1)
}
func (m *MockCLIConnector) GetSTASections() ([]wireless_gateway_manager.STASection, error) {
	args := m.Called()
	return args.Get(0).([]wireless_gateway_manager.STASection), args.Error(1)
}
func (m *MockCLIConnector) GetActiveSTA() (*wireless_gateway_manager.STASection, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*wireless_gateway_manager.STASection), args.Error(1)
}
func (m *MockCLIConnector) FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error) {
	args := m.Called(ssid, passphrase, encryption, radio)
	return args.String(0), args.Error(1)
}
func (m *MockCLIConnector) RemoveDisabledSTA(ssid string) error {
	return m.Called(ssid).Error(0)
}
func (m *MockCLIConnector) SwitchUpstream(activeIface, candidateIface, candidateSSID string) error {
	return m.Called(activeIface, candidateIface, candidateSSID).Error(0)
}
func (m *MockCLIConnector) GetSTADevice(ifaceName string) (string, error) {
	args := m.Called(ifaceName)
	return args.String(0), args.Error(1)
}
func (m *MockCLIConnector) EnsureWWANSetup() error { return m.Called().Error(0) }
func (m *MockCLIConnector) EnsureRadiosEnabled() error {
	return m.Called().Error(0)
}
func (m *MockCLIConnector) GetSTANetdev(sectionName string) (string, error) {
	args := m.Called(sectionName)
	return args.String(0), args.Error(1)
}
func (m *MockCLIConnector) CleanupStaleSTAs() error { return m.Called().Error(0) }

type MockCLIScanner struct {
	mock.Mock
}

func (m *MockCLIScanner) ScanAllRadios() ([]wireless_gateway_manager.NetworkInfo, error) {
	args := m.Called()
	return args.Get(0).([]wireless_gateway_manager.NetworkInfo), args.Error(1)
}
func (m *MockCLIScanner) GetRadios() ([]string, error) {
	args := m.Called()
	return args.Get(0).([]string), args.Error(1)
}
func (m *MockCLIScanner) DetectEncryption(encryptionStr string) string {
	return m.Called(encryptionStr).String(0)
}
func (m *MockCLIScanner) FindBestRadioForSSID(ssid string, networks []wireless_gateway_manager.NetworkInfo) (string, error) {
	args := m.Called(ssid, networks)
	return args.String(0), args.Error(1)
}

func newTestCLIServer(connector wireless_gateway_manager.ConnectorInterface, scanner wireless_gateway_manager.ScannerInterface) *CLIServer {
	return &CLIServer{
		configManager:   nil,
		merchant:        nil,
		connector:       connector,
		scanner:         scanner,
		upstreamManager: nil,
		startTime:       time.Now(),
	}
}

func TestCLIServer_handleStatusCommand(t *testing.T) {
	connector := &MockCLIConnector{}
	scanner := &MockCLIScanner{}
	s := newTestCLIServer(connector, scanner)

	resp := s.handleStatusCommand([]string{}, nil)
	assert.True(t, resp.Success)
	status, ok := resp.Data.(ServiceStatus)
	assert.True(t, ok)
	assert.True(t, status.Running)
	assert.False(t, status.NetworkOK)
}

func TestCLIServer_handleUpstreamScanCommand(t *testing.T) {
	connector := &MockCLIConnector{}
	scanner := &MockCLIScanner{}
	scanner.On("ScanAllRadios").Return([]wireless_gateway_manager.NetworkInfo{
		{SSID: "TestNet", Signal: -50, Encryption: "psk2", Radio: "radio0"},
	}, nil)
	s := newTestCLIServer(connector, scanner)

	resp := s.processCommand(CLIMessage{Command: "upstream", Args: []string{"scan"}})
	assert.True(t, resp.Success)
}

func TestCLIServer_handleUpstreamScan_NilConnector(t *testing.T) {
	s := &CLIServer{startTime: time.Now()}
	resp := s.processCommand(CLIMessage{Command: "upstream", Args: []string{"scan"}})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "not available")
}

func TestCLIServer_handleUpstreamConnect_NoSSID(t *testing.T) {
	connector := &MockCLIConnector{}
	scanner := &MockCLIScanner{}
	s := newTestCLIServer(connector, scanner)

	resp := s.processCommand(CLIMessage{Command: "upstream", Args: []string{"connect"}})
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "SSID")
}

func TestCLIServer_handleUpstreamList(t *testing.T) {
	connector := &MockCLIConnector{}
	scanner := &MockCLIScanner{}
	connector.On("GetSTASections").Return([]wireless_gateway_manager.STASection{
		{Name: "upstream_test", SSID: "TestNet", Device: "radio0", Disabled: false},
	}, nil)
	s := newTestCLIServer(connector, scanner)

	resp := s.processCommand(CLIMessage{Command: "upstream", Args: []string{"list-upstream"}})
	assert.True(t, resp.Success)
	connector.AssertExpectations(t)
}

func TestCLIServer_handleUpstreamRemove(t *testing.T) {
	connector := &MockCLIConnector{}
	scanner := &MockCLIScanner{}
	connector.On("RemoveDisabledSTA", "OldNet").Return(nil)
	s := newTestCLIServer(connector, scanner)

	resp := s.processCommand(CLIMessage{Command: "upstream", Args: []string{"remove-upstream", "OldNet"}})
	assert.True(t, resp.Success)
	connector.AssertExpectations(t)
}

func TestCLIServer_manualPauseDuration_Default(t *testing.T) {
	s := &CLIServer{startTime: time.Now()}
	d := s.manualPauseDuration()
	assert.Equal(t, 120*time.Second, d)
}

func TestCLIServer_manualPauseDuration_ConfigOverride(t *testing.T) {
	cm := &config_manager.ConfigManager{}
	cfg := config_manager.NewDefaultConfig()
	cfg.UpstreamWifi.ManualPauseSeconds = 60
	setCLIConfigField(cm, cfg)

	s := &CLIServer{configManager: cm, startTime: time.Now()}
	d := s.manualPauseDuration()
	assert.Equal(t, 60*time.Second, d)
}

func setCLIConfigField(cm *config_manager.ConfigManager, config *config_manager.Config) {
	cmValue := reflect.ValueOf(cm).Elem()
	configField := cmValue.FieldByName("config")
	if !configField.CanSet() {
		configField = reflect.NewAt(configField.Type(), unsafe.Pointer(configField.UnsafeAddr())).Elem()
	}
	configField.Set(reflect.ValueOf(config))
}
