package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnector_GetSTASections(t *testing.T) {
	m := &MockConnector{}
	expected := []STASection{
		{Name: "upstream_test", SSID: "TestNet", Device: "radio0", Disabled: true},
	}
	m.On("GetSTASections").Return(expected, nil)

	sections, err := m.GetSTASections()
	assert.NoError(t, err)
	assert.Len(t, sections, 1)
	assert.Equal(t, "TestNet", sections[0].SSID)
	m.AssertExpectations(t)
}

func TestConnector_GetActiveSTA(t *testing.T) {
	m := &MockConnector{}
	activeSection := &STASection{
		Name:     "upstream_test",
		SSID:     "TestNet",
		Device:   "radio0",
		Disabled: false,
	}
	m.On("GetActiveSTA").Return(activeSection, nil)

	result, err := m.GetActiveSTA()
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TestNet", result.SSID)
	assert.False(t, result.Disabled)
}

func TestConnector_GetActiveSTA_NoneActive(t *testing.T) {
	m := &MockConnector{}
	m.On("GetActiveSTA").Return(nil, nil)

	result, err := m.GetActiveSTA()
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestConnector_FindOrCreateSTAForSSID_ReuseExisting(t *testing.T) {
	m := &MockConnector{}
	m.On("FindOrCreateSTAForSSID", "TestNet", "pass123", "psk2", "radio0").Return("upstream_testnet", nil)

	iface, err := m.FindOrCreateSTAForSSID("TestNet", "pass123", "psk2", "radio0")
	assert.NoError(t, err)
	assert.Equal(t, "upstream_testnet", iface)
	m.AssertExpectations(t)
}

func TestConnector_FindOrCreateSTAForSSID_CreateNew(t *testing.T) {
	m := &MockConnector{}
	m.On("FindOrCreateSTAForSSID", "NewNet", "pass456", "psk2", "radio1").Return("upstream_newnet", nil)

	iface, err := m.FindOrCreateSTAForSSID("NewNet", "pass456", "psk2", "radio1")
	assert.NoError(t, err)
	assert.Equal(t, "upstream_newnet", iface)
	m.AssertExpectations(t)
}

func TestConnector_RemoveDisabledSTA_Success(t *testing.T) {
	m := &MockConnector{}
	m.On("RemoveDisabledSTA", "TestNet").Return(nil)

	err := m.RemoveDisabledSTA("TestNet")
	assert.NoError(t, err)
	m.AssertExpectations(t)
}

func TestConnector_RemoveDisabledSTA_ActiveRefused(t *testing.T) {
	m := &MockConnector{}
	m.On("RemoveDisabledSTA", "ActiveNet").Return(assert.AnError)

	err := m.RemoveDisabledSTA("ActiveNet")
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestConnector_RemoveDisabledSTA_NotFound(t *testing.T) {
	m := &MockConnector{}
	m.On("RemoveDisabledSTA", "UnknownNet").Return(assert.AnError)

	err := m.RemoveDisabledSTA("UnknownNet")
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestConnector_SwitchUpstream_Success(t *testing.T) {
	m := &MockConnector{}
	m.On("SwitchUpstream", "old_iface", "new_iface", "TestNet").Return(nil)

	err := m.SwitchUpstream("old_iface", "new_iface", "TestNet")
	assert.NoError(t, err)
	m.AssertExpectations(t)
}

func TestConnector_SwitchUpstream_TimeoutFallback(t *testing.T) {
	m := &MockConnector{}
	m.On("SwitchUpstream", "old_iface", "bad_iface", "BadNet").Return(assert.AnError)

	err := m.SwitchUpstream("old_iface", "bad_iface", "BadNet")
	assert.Error(t, err)
	m.AssertExpectations(t)
}

func TestConnector_EnsureWWANSetup(t *testing.T) {
	m := &MockConnector{}
	m.On("EnsureWWANSetup").Return(nil)

	err := m.EnsureWWANSetup()
	assert.NoError(t, err)
	m.AssertExpectations(t)
}

func TestConnector_EnsureRadiosEnabled(t *testing.T) {
	m := &MockConnector{}
	m.On("EnsureRadiosEnabled").Return(nil)

	err := m.EnsureRadiosEnabled()
	assert.NoError(t, err)
	m.AssertExpectations(t)
}

func TestGetUCIEncryptionType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"WPA/WPA2", "psk2"},
		{"WPA2", "psk2"},
		{"WPA", "psk"},
		{"WEP", "wep"},
		{"unknown", "none"},
		{"", "none"},
		{"SAE", "none"},
	}
	for _, tt := range tests {
		result := getUCIEncryptionType(tt.input)
		assert.Equal(t, tt.expected, result, "getUCIEncryptionType(%q)", tt.input)
	}
}

func TestGenerateRandomSuffix(t *testing.T) {
	c := &Connector{}

	suffix, err := c.generateRandomSuffix(4)
	assert.NoError(t, err)
	assert.Len(t, suffix, 4)

	for _, ch := range suffix {
		assert.True(t, ch >= '0' && ch <= '9', "expected digit, got %c", ch)
	}

	suffix2, err := c.generateRandomSuffix(4)
	assert.NoError(t, err)
	assert.Len(t, suffix2, 4)

	suffix8, err := c.generateRandomSuffix(8)
	assert.NoError(t, err)
	assert.Len(t, suffix8, 8)
}

func TestConnector_GetSTANetdev(t *testing.T) {
	m := &MockConnector{}
	m.On("GetSTANetdev", "upstream_testnet").Return("phy0-sta0", nil)

	netdev, err := m.GetSTANetdev("upstream_testnet")
	assert.NoError(t, err)
	assert.Equal(t, "phy0-sta0", netdev)
	m.AssertExpectations(t)
}

func TestConnector_GetSTANetdev_NotFound(t *testing.T) {
	m := &MockConnector{}
	m.On("GetSTANetdev", "nonexistent").Return("", assert.AnError)

	netdev, err := m.GetSTANetdev("nonexistent")
	assert.Error(t, err)
	assert.Equal(t, "", netdev)
	m.AssertExpectations(t)
}

func TestSanitizeSSIDForUCI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"MyNetwork", "upstream_mynetwork"},
		{"My-Network 123", "upstream_my_network_123"},
		{"UPPERCASE", "upstream_uppercase"},
		{"Special!@#Chars", "upstream_special___chars"},
	}
	for _, tt := range tests {
		result := sanitizeSSIDForUCI(tt.input)
		assert.Equal(t, tt.expected, result)
	}
}
