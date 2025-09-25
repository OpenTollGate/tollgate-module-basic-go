package wireless_gateway_manager

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/stretchr/testify/mock"
)

// MockConnector is a mock implementation of the Connector interface for testing
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

func (m *MockConnector) SetAPSSIDSafeMode() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) RestoreAPSSIDFromSafeMode() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) UpdateLocalAPSSID(pricePerStep, stepSize int) error {
	args := m.Called(pricePerStep, stepSize)
	return args.Error(0)
}

func (m *MockConnector) Disconnect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConnector) Reconnect() error {
	args := m.Called()
	return args.Error(0)
}

// MockScanner is a mock implementation of the Scanner interface for testing
type MockScanner struct {
	mock.Mock
}

func (m *MockScanner) ScanWirelessNetworks() ([]NetworkInfo, error) {
	args := m.Called()
	return args.Get(0).([]NetworkInfo), args.Error(1)
}

// MockVendorElementProcessor is a mock implementation of the VendorElementProcessor interface for testing
type MockVendorElementProcessor struct {
	mock.Mock
}

// MockNetworkMonitor is a mock implementation of the NetworkMonitor for testing
type MockNetworkMonitor struct {
	mock.Mock
}

func (m *MockNetworkMonitor) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockNetworkMonitor) IsInSafeMode() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockNetworkMonitor) Start() {
	m.Called()
}

func (m *MockNetworkMonitor) Stop() {
	m.Called()
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

func TestResellerModeDisabled(t *testing.T) {
	// Create a mock config manager with reseller mode disabled
	cm := &config_manager.ConfigManager{}
	// Use reflection to set the config directly for testing
	config := config_manager.NewDefaultConfig()
	config.ResellerMode = false
	// Use reflection to set the private config field
	setConfigField(cm, config)

	// Create mock components
	mockConnector := &MockConnector{}
	mockScanner := &MockScanner{}
	mockVendorProcessor := &MockVendorElementProcessor{}
	mockNetworkMonitor := &MockNetworkMonitor{}

	// Create a GatewayManager with mock components
	gm := &GatewayManager{
		connector:       mockConnector,
		scanner:         mockScanner,
		vendorProcessor: mockVendorProcessor,
		networkMonitor:  mockNetworkMonitor,
		cm:              cm,
	}

	// Call ScanWirelessNetworks
	gm.ScanWirelessNetworks(context.Background())

	// Verify that no methods were called on the mocks since reseller mode is disabled
	// This test passes if no panic occurs and no methods are called
	mockConnector.AssertExpectations(t)
	mockScanner.AssertExpectations(t)
	mockVendorProcessor.AssertExpectations(t)
	mockNetworkMonitor.AssertExpectations(t)
}

func TestResellerModeEnabled_FilterTollGateNetworks(t *testing.T) {
	// Create a mock config manager with reseller mode enabled
	cm := &config_manager.ConfigManager{}
	// Use reflection to set the config directly for testing
	config := config_manager.NewDefaultConfig()
	config.ResellerMode = true
	// Use reflection to set the private config field
	setConfigField(cm, config)

	// Create mock components
	mockConnector := &MockConnector{}
	mockScanner := &MockScanner{}
	mockVendorProcessor := &MockVendorElementProcessor{}
	mockNetworkMonitor := &MockNetworkMonitor{}

	// Set up mock expectations
	mockScanner.On("ScanWirelessNetworks").Return([]NetworkInfo{
		{SSID: "TollGate-ABC", BSSID: "00:11:22:33:44:55", Signal: -50, Encryption: "Open"},
		{SSID: "OpenNetwork", BSSID: "66:77:88:99:AA:BB", Signal: -60, Encryption: "Open"},
		{SSID: "TollGate-XYZ", BSSID: "CC:DD:EE:FF:00:11", Signal: -70, Encryption: "Open"},
	}, nil)

	// Set up vendor processor expectations for TollGate networks
	mockVendorProcessor.On("ExtractAndScore", mock.AnythingOfType("NetworkInfo")).Return(map[string]interface{}{}, 100, nil)
	
	// Set up network monitor expectations
	mockNetworkMonitor.On("IsConnected").Return(true)
	mockNetworkMonitor.On("IsInSafeMode").Return(false)
	
	// Set up connector expectations
	mockConnector.On("GetConnectedSSID").Return("TollGate-ABC", nil)
	mockConnector.On("UpdateLocalAPSSID", 1, 20000).Return(nil)
	
	// Create a GatewayManager with mock components
	gm := &GatewayManager{
		connector:       mockConnector,
		scanner:         mockScanner,
		vendorProcessor: mockVendorProcessor,
		networkMonitor:  mockNetworkMonitor,
		cm:              cm,
	}

	// Call ScanWirelessNetworks
	gm.ScanWirelessNetworks(context.Background())

	// Verify that only TollGate networks were processed
	// The mockVendorProcessor should only be called for TollGate networks
	mockScanner.AssertExpectations(t)
	// We expect ExtractAndScore to be called twice (once for each TollGate network)
	mockVendorProcessor.AssertNumberOfCalls(t, "ExtractAndScore", 2)
	mockConnector.AssertExpectations(t)
	mockNetworkMonitor.AssertExpectations(t)
}

func TestResellerModeEnabled_SkipEncryptedTollGateNetworks(t *testing.T) {
	// Create a mock config manager with reseller mode enabled
	cm := &config_manager.ConfigManager{}
	// Use reflection to set the config directly for testing
	config := config_manager.NewDefaultConfig()
	config.ResellerMode = true
	// Use reflection to set the private config field
	setConfigField(cm, config)

	// Create mock components
	mockConnector := &MockConnector{}
	mockScanner := &MockScanner{}
	mockVendorProcessor := &MockVendorElementProcessor{}
	mockNetworkMonitor := &MockNetworkMonitor{}

	// Set up mock expectations
	mockScanner.On("ScanWirelessNetworks").Return([]NetworkInfo{
		{SSID: "TollGate-ABC", BSSID: "00:11:22:33:44:55", Signal: -50, Encryption: "WPA2"},
		{SSID: "TollGate-XYZ", BSSID: "CC:DD:EE:FF:00:11", Signal: -70, Encryption: "Open"},
	}, nil)

	// Set up vendor processor expectations for the open TollGate network
	mockVendorProcessor.On("ExtractAndScore", mock.MatchedBy(func(network NetworkInfo) bool {
		return network.SSID == "TollGate-XYZ" && network.Encryption == "Open"
	})).Return(map[string]interface{}{}, 100, nil)
	
	// Set up network monitor expectations
	mockNetworkMonitor.On("IsConnected").Return(true)
	mockNetworkMonitor.On("IsInSafeMode").Return(false)
	
	// Set up connector expectations
	mockConnector.On("GetConnectedSSID").Return("TollGate-XYZ", nil)
	mockConnector.On("UpdateLocalAPSSID", 1, 20000).Return(nil)
	
	// Create a GatewayManager with mock components
	gm := &GatewayManager{
		connector:       mockConnector,
		scanner:         mockScanner,
		vendorProcessor: mockVendorProcessor,
		networkMonitor:  mockNetworkMonitor,
		cm:              cm,
	}

	// Call ScanWirelessNetworks
	gm.ScanWirelessNetworks(context.Background())

	// Verify that only the open TollGate network was processed
	mockScanner.AssertExpectations(t)
	// We expect ExtractAndScore to be called only once for the open TollGate network
	mockVendorProcessor.AssertNumberOfCalls(t, "ExtractAndScore", 1)
	mockConnector.AssertExpectations(t)
	mockNetworkMonitor.AssertExpectations(t)
}
// Helper function to set the private config field using reflection
func setConfigField(cm *config_manager.ConfigManager, config *config_manager.Config) {
	// Get the reflect.Value of the ConfigManager instance
	cmValue := reflect.ValueOf(cm).Elem()
	
	// Find the config field by name
	configField := cmValue.FieldByName("config")
	
	// Make the field writable if it's not already
	if !configField.CanSet() {
		// If the field is not exported, we need to use unsafe operations
		// This is a workaround for setting private fields in tests
		configField = reflect.NewAt(configField.Type(), unsafe.Pointer(configField.UnsafeAddr())).Elem()
	}
	
	// Set the new config value
	configField.Set(reflect.ValueOf(config))
}