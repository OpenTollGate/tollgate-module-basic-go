package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"unsafe"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

func TestGetIP_XRealIP_FromLocalhost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-Ip", "1.2.3.4")
	r.RemoteAddr = "127.0.0.1:1234"

	ip := getIP(r)
	assert.Equal(t, "1.2.3.4", ip)
}

func TestGetIP_XRealIP_IgnoredFromNonLocalhost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-Ip", "1.2.3.4")
	r.RemoteAddr = "5.6.7.8:1234"

	ip := getIP(r)
	assert.Equal(t, "5.6.7.8", ip, "X-Real-Ip should be ignored for non-localhost requests")
}

func TestGetIP_XForwardedFor_FromLocalhost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	r.RemoteAddr = "127.0.0.1:1234"

	ip := getIP(r)
	assert.Equal(t, "10.0.0.1", ip)
}

func TestGetIP_XForwardedFor_IgnoredFromNonLocalhost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	r.RemoteAddr = "5.6.7.8:1234"

	ip := getIP(r)
	assert.Equal(t, "5.6.7.8", ip, "X-Forwarded-For should be ignored for non-localhost requests")
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

func TestMerchantTypesProvider_DelegatesToInner(t *testing.T) {
	inner := merchant.NewMutexMerchantProvider(nil)
	p := &merchantTypesProvider{inner: inner}

	assert.Nil(t, p.GetMerchant())

	mock := &namedMerchant{name: "test"}
	inner.SetMerchant(mock)

	got := p.GetMerchant()
	assert.NotNil(t, got)
}

func TestSwapMerchant_ValidMerchantInterface_Succeeds(t *testing.T) {
	inner := merchant.NewMutexMerchantProvider(nil)
	merchantProvider = &merchantTypesProvider{inner: inner}

	mock := &namedMerchant{name: "swapped"}
	swapMerchant(mock)

	got := merchantProvider.inner.GetMerchant()
	assert.NotNil(t, got)
	if n, ok := got.(*namedMerchant); ok {
		assert.Equal(t, "swapped", n.name)
	} else {
		t.Fatal("expected *namedMerchant")
	}
}

func TestSwapMerchant_PreservesMerchantOnSuccess(t *testing.T) {
	original := &namedMerchant{name: "original"}
	inner := merchant.NewMutexMerchantProvider(original)
	merchantProvider = &merchantTypesProvider{inner: inner}

	replacement := &namedMerchant{name: "replacement"}
	swapMerchant(replacement)

	got := merchantProvider.inner.GetMerchant()
	if n, ok := got.(*namedMerchant); !ok || n.name != "replacement" {
		t.Fatal("swapMerchant should replace with new merchant")
	}
}

type namedMerchant struct {
	name string
}

func (m *namedMerchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "", nil
}
func (m *namedMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", nil
}
func (m *namedMerchant) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, nil
}
func (m *namedMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *namedMerchant) GetBalance() uint64                             { return 0 }
func (m *namedMerchant) GetBalanceByMint(mintURL string) uint64         { return 0 }
func (m *namedMerchant) GetAllMintBalances() map[string]uint64          { return nil }
func (m *namedMerchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	return nil, nil
}
func (m *namedMerchant) GetAdvertisement() string { return "" }
func (m *namedMerchant) StartPayoutRoutine()     {}
func (m *namedMerchant) StartDataUsageMonitoring() {}
func (m *namedMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, nil
}
func (m *namedMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *namedMerchant) AddAllotment(macAddress, metric string, amount uint64) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *namedMerchant) GetUsage(macAddress string) (string, error) { return "", nil }
func (m *namedMerchant) Fund(cashuToken string) (uint64, error)     { return 0, nil }
func (m *namedMerchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*merchant.LightningInvoice, error) {
	return nil, nil
}
func (m *namedMerchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*merchant.LightningQuoteStatus, error) {
	return nil, nil
}
func (m *namedMerchant) SetOnReachableSetChanged(func()) {}
func (m *namedMerchant) Shutdown() error                  { return nil }
