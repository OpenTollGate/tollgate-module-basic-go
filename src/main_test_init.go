//go:build test

package main

import (
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant" // Import the merchant package
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/mock"
)

// MockConfigManager is a mock implementation of config_manager.ConfigManager
type MockConfigManager struct {
	mock.Mock
}


// MockMerchant is a mock implementation of merchant.MerchantService
type MockMerchant struct {
	mock.Mock
}

// Ensure MockMerchant implements merchant.MerchantService
var _ merchant.MerchantService = (*MockMerchant)(nil)

func (m *MockConfigManager) LoadConfig() (*config_manager.Config, error) {
	args := m.Called()
	return args.Get(0).(*config_manager.Config), args.Error(1)
}

func (m *MockConfigManager) LoadInstallConfig() (*config_manager.InstallConfig, error) {
	args := m.Called()
	return args.Get(0).(*config_manager.InstallConfig), args.Error(1)
}

func (m *MockConfigManager) EnsureInitializedConfig() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockConfigManager) SaveConfig(config *config_manager.Config) error {
	args := m.Called(config)
	return args.Error(0)
}

func (m *MockConfigManager) SaveInstallConfig(installConfig *config_manager.InstallConfig) error {
	args := m.Called(installConfig)
	return args.Error(0)
}

func (m *MockConfigManager) GetNIP94Event(installationID string) (string, error) {
	args := m.Called(installationID)
	return args.String(0), args.Error(1)
}



func (m *MockMerchant) PurchaseSession(event nostr.Event) (*nostr.Event, error) {
	args := m.Called(event)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	args := m.Called(level, code, message, customerPubkey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) GetAdvertisement() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockMerchant) StartPayoutRoutine() {
	m.Called()
}

func init() {

	// Initialize global variables used by main.go with mock implementations
	// This prevents main.go's initializeApplication from being called and
	// trying to access /etc/tollgate/config.json
	
	// Create a mock config manager
	mockConfig := new(MockConfigManager)
	// Set up expectations for methods that might be called during tests
	// For example, if LoadConfig is called, return a dummy config and no error
	mockConfig.On("LoadConfig").Return(&config_manager.Config{}, nil)
	mockConfig.On("LoadInstallConfig").Return(&config_manager.InstallConfig{}, nil)
	mockConfig.On("EnsureInitializedConfig").Return(nil)
	mockConfig.On("GetNIP94Event", mock.Anything).Return("", nil) // Dummy return for NIP94 event

	configManager = mockConfig

	// Create a mock merchant instance
	mockMerchant := new(MockMerchant)
	mockMerchant.On("PurchaseSession", mock.Anything).Return(&nostr.Event{}, nil)
	mockMerchant.On("CreateNoticeEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&nostr.Event{}, nil)
	mockMerchant.On("GetAdvertisement").Return("Test Advertisement")
	mockMerchant.On("StartPayoutRoutine").Return()

	merchantInstance = mockMerchant
}