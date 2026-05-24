package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"unsafe"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/stretchr/testify/assert"
)

func TestGetIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-Ip", "1.2.3.4")
	r.RemoteAddr = "5.6.7.8:1234"

	ip := getIP(r)
	assert.Equal(t, "1.2.3.4", ip)
}

func TestGetIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	r.RemoteAddr = "5.6.7.8:1234"

	ip := getIP(r)
	assert.Equal(t, "10.0.0.1", ip)
}

func TestGetIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:8080"

	ip := getIP(r)
	assert.Equal(t, "192.168.1.1", ip)
}

func TestParseUsageString_Valid(t *testing.T) {
	used, allotment, err := parseUsageString("100/500")
	assert.NoError(t, err)
	assert.Equal(t, uint64(100), used)
	assert.Equal(t, uint64(500), allotment)
}

func TestParseUsageString_ZeroValues(t *testing.T) {
	used, allotment, err := parseUsageString("0/0")
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), used)
	assert.Equal(t, uint64(0), allotment)
}

func TestParseUsageString_InvalidFormat(t *testing.T) {
	_, _, err := parseUsageString("100")
	assert.Error(t, err)
}

func TestParseUsageString_InvalidUsed(t *testing.T) {
	_, _, err := parseUsageString("abc/500")
	assert.Error(t, err)
}

func TestParseUsageString_InvalidAllotment(t *testing.T) {
	_, _, err := parseUsageString("100/abc")
	assert.Error(t, err)
}

func TestResellerModeAdapter_NilConfigManager(t *testing.T) {
	adapter := &resellerModeAdapter{cm: nil}
	assert.False(t, adapter.IsResellerModeActive())
}

func TestResellerModeAdapter_True(t *testing.T) {
	cm := &config_manager.ConfigManager{}
	cfg := config_manager.NewDefaultConfig()
	cfg.ResellerMode = true
	setMainConfigField(cm, cfg)

	adapter := &resellerModeAdapter{cm: cm}
	assert.True(t, adapter.IsResellerModeActive())
}

func TestResellerModeAdapter_False(t *testing.T) {
	cm := &config_manager.ConfigManager{}
	cfg := config_manager.NewDefaultConfig()
	cfg.ResellerMode = false
	setMainConfigField(cm, cfg)

	adapter := &resellerModeAdapter{cm: cm}
	assert.False(t, adapter.IsResellerModeActive())
}

func setMainConfigField(cm *config_manager.ConfigManager, config *config_manager.Config) {
	cmValue := reflect.ValueOf(cm).Elem()
	configField := cmValue.FieldByName("config")
	if !configField.CanSet() {
		configField = reflect.NewAt(configField.Type(), unsafe.Pointer(configField.UnsafeAddr())).Elem()
	}
	configField.Set(reflect.ValueOf(config))
}
