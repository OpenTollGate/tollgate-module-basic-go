package merchant

import "sync"

// MerchantProvider is the interface for thread-safe merchant access with
// atomic swap capability. Long-lived consumers (HTTP handlers, CLI, USM)
// should hold a MerchantProvider and call GetMerchant() at operation time
// to always see the current merchant — even after degraded-to-full recovery.
type MerchantProvider interface {
	GetMerchant() MerchantInterface
	SetMerchant(MerchantInterface)
}

// MutexMerchantProvider implements MerchantProvider with an RWMutex.
type MutexMerchantProvider struct {
	mu       sync.RWMutex
	merchant MerchantInterface
}

// Compile-time assertion.
var _ MerchantProvider = (*MutexMerchantProvider)(nil)

func NewMerchantProvider(m MerchantInterface) *MutexMerchantProvider {
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
