package merchant_types

import (
	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

type CustomerSession struct {
	MacAddress string
	StartTime  int64
	Metric     string
	Allotment  uint64
}

type LightningInvoice struct {
	QuoteID string
	Invoice string
	MintURL string
	Amount  uint64
	Expiry  uint64
	State   string
}

type LightningQuoteStatus struct {
	QuoteID       string
	MintURL       string
	Amount        uint64
	State         string
	AccessGranted bool
	Allotment     uint64
	Metric        string
}

type PaymentMerchant interface {
	CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)
	GetAcceptedMints() []config_manager.MintConfig
	GetBalanceByMint(mintURL string) uint64
	Fund(cashuToken string) (uint64, error)
}

type MerchantProvider interface {
	GetMerchant() PaymentMerchant
}

type MutexMerchantProvider struct {
	mu       sync.RWMutex
	merchant PaymentMerchant
}

func NewMutexMerchantProvider(m PaymentMerchant) *MutexMerchantProvider {
	return &MutexMerchantProvider{merchant: m}
}

func (p *MutexMerchantProvider) GetMerchant() PaymentMerchant {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.merchant
}

func (p *MutexMerchantProvider) SetMerchant(m PaymentMerchant) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.merchant = m
}
