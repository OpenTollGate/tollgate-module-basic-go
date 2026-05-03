package cli

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

type mockMerchant struct {
	balance         uint64
	mintBalances    map[string]uint64
	acceptedMints   []config_manager.MintConfig
	fundAmount      uint64
	fundErr         error
	drainToken      string
	drainAmount     uint64
	drainErr        error
	paymentToken    string
	paymentTokenErr error
}

func (m *mockMerchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return m.paymentToken, m.paymentTokenErr
}

func (m *mockMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return m.paymentToken, m.paymentTokenErr
}

func (m *mockMerchant) DrainMint(mintURL string) (string, uint64, error) {
	return m.drainToken, m.drainAmount, m.drainErr
}

func (m *mockMerchant) GetAcceptedMints() []config_manager.MintConfig {
	return m.acceptedMints
}

func (m *mockMerchant) GetBalance() uint64 {
	return m.balance
}

func (m *mockMerchant) GetBalanceByMint(mintURL string) uint64 {
	if m.mintBalances == nil {
		return 0
	}
	return m.mintBalances[mintURL]
}

func (m *mockMerchant) GetAllMintBalances() map[string]uint64 {
	return m.mintBalances
}

func (m *mockMerchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	return nil, nil
}

func (m *mockMerchant) GetAdvertisement() string {
	return "test-ad"
}

func (m *mockMerchant) StartPayoutRoutine() {}

func (m *mockMerchant) StartDataUsageMonitoring() {}

func (m *mockMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, nil
}

func (m *mockMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return nil, nil
}

func (m *mockMerchant) AddAllotment(macAddress, metric string, amount uint64) (*merchant.CustomerSession, error) {
	return nil, nil
}

func (m *mockMerchant) GetUsage(macAddress string) (string, error) {
	return "", nil
}

func (m *mockMerchant) RestoreSessions() {}

func (m *mockMerchant) Fund(cashuToken string) (uint64, error) {
	return m.fundAmount, m.fundErr
}
