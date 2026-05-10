package merchant

import "sync"

type MerchantProvider struct {
	mu       sync.RWMutex
	merchant MerchantInterface
}

func NewMerchantProvider(m MerchantInterface) *MerchantProvider {
	return &MerchantProvider{merchant: m}
}

func (p *MerchantProvider) GetMerchant() MerchantInterface {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.merchant
}

func (p *MerchantProvider) SetMerchant(m MerchantInterface) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.merchant = m
}
