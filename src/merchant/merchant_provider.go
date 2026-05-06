package merchant

import "sync"

type MerchantProvider interface {
	GetMerchant() MerchantInterface
	SetMerchant(m MerchantInterface)
}

type MutexMerchantProvider struct {
	mu       sync.RWMutex
	merchant MerchantInterface
}

func NewMutexMerchantProvider(m MerchantInterface) *MutexMerchantProvider {
	return &MutexMerchantProvider{merchant: m}
}

func (p *MutexMerchantProvider) GetMerchant() MerchantInterface {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.merchant
}

func (p *MutexMerchantProvider) SetMerchant(m MerchantInterface) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.merchant = m
}
