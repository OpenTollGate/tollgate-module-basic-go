package merchant_types

import (
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/stretchr/testify/assert"
)

type mockPaymentMerchant struct {
	balance uint64
}

func (m *mockPaymentMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "mock-token", nil
}

func (m *mockPaymentMerchant) GetAcceptedMints() []config_manager.MintConfig {
	return nil
}

func (m *mockPaymentMerchant) GetBalanceByMint(mintURL string) uint64 {
	return m.balance
}

func (m *mockPaymentMerchant) Fund(cashuToken string) (uint64, error) {
	return 0, nil
}

func TestPaymentMerchant_Interface(t *testing.T) {
	var _ PaymentMerchant = &mockPaymentMerchant{}
}

func TestMerchantProvider_Interface(t *testing.T) {
	var _ MerchantProvider = NewMutexMerchantProvider(&mockPaymentMerchant{})
}

func TestMutexMerchantProvider_InitialValue(t *testing.T) {
	m := &mockPaymentMerchant{balance: 42}
	p := NewMutexMerchantProvider(m)
	assert.Equal(t, uint64(42), p.GetMerchant().GetBalanceByMint("any"))
}

func TestMutexMerchantProvider_SetAndGet(t *testing.T) {
	p := NewMutexMerchantProvider(&mockPaymentMerchant{balance: 10})
	p.SetMerchant(&mockPaymentMerchant{balance: 99})
	assert.Equal(t, uint64(99), p.GetMerchant().GetBalanceByMint("any"))
}

func TestMutexMerchantProvider_ConcurrentAccess(t *testing.T) {
	p := NewMutexMerchantProvider(&mockPaymentMerchant{balance: 0})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(val uint64) {
			defer wg.Done()
			p.SetMerchant(&mockPaymentMerchant{balance: val})
		}(uint64(i))
		go func() {
			defer wg.Done()
			_ = p.GetMerchant().GetBalanceByMint("any")
		}()
	}
	wg.Wait()
}

func TestMutexMerchantProvider_NilMerchant(t *testing.T) {
	p := NewMutexMerchantProvider(nil)
	assert.Nil(t, p.GetMerchant())
}

func TestMutexMerchantProvider_SetToNil(t *testing.T) {
	p := NewMutexMerchantProvider(&mockPaymentMerchant{balance: 1})
	p.SetMerchant(nil)
	assert.Nil(t, p.GetMerchant())
}
