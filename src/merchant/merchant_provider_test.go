package merchant

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

type mockMerchantForProvider struct {
	name string
}

func (m *mockMerchantForProvider) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) GetAcceptedMints() []config_manager.MintConfig {
	return nil
}

func (m *mockMerchantForProvider) GetBalance() uint64 { return 0 }
func (m *mockMerchantForProvider) GetBalanceByMint(mintURL string) uint64 {
	return 0
}

func (m *mockMerchantForProvider) GetAllMintBalances() map[string]uint64 {
	return nil
}

func (m *mockMerchantForProvider) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) GetAdvertisement() string { return "" }
func (m *mockMerchantForProvider) StartPayoutRoutine()     {}
func (m *mockMerchantForProvider) StartDataUsageMonitoring() {}

func (m *mockMerchantForProvider) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) GetSession(macAddress string) (*CustomerSession, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) GetUsage(macAddress string) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}

func (m *mockMerchantForProvider) Fund(cashuToken string) (uint64, error) {
	return 0, fmt.Errorf("mock: %s", m.name)
}

func extractMockName(m MerchantInterface) string {
	if mock, ok := m.(*mockMerchantForProvider); ok {
		return mock.name
	}
	return "not-mock"
}

func TestNewMutexMerchantProvider_InitialValue(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "initial"})

	if extractMockName(p.GetMerchant()) != "initial" {
		t.Fatalf("expected initial, got %v", p.GetMerchant())
	}
}

func TestMutexMerchantProvider_SetMerchant(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "old"})

	p.SetMerchant(&mockMerchantForProvider{name: "new"})

	if extractMockName(p.GetMerchant()) != "new" {
		t.Fatalf("expected new, got %v", p.GetMerchant())
	}
}

func TestMutexMerchantProvider_NilInitial(t *testing.T) {
	p := NewMutexMerchantProvider(nil)

	if p.GetMerchant() != nil {
		t.Fatalf("expected nil, got %v", p.GetMerchant())
	}

	p.SetMerchant(&mockMerchantForProvider{name: "after-nil"})
	if p.GetMerchant() == nil {
		t.Fatal("expected non-nil after SetMerchant")
	}
}

func TestMutexMerchantProvider_ImplementsProvider(t *testing.T) {
	var _ MerchantProvider = NewMutexMerchantProvider(nil)
}

func TestMutexMerchantProvider_ConcurrentGetSet(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "start"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			p.SetMerchant(&mockMerchantForProvider{name: fmt.Sprintf("writer-%d", n)})
		}(i)
		go func() {
			defer wg.Done()
			_ = p.GetMerchant()
		}()
	}
	wg.Wait()

	if p.GetMerchant() == nil {
		t.Fatal("expected non-nil after concurrent access")
	}
}

func TestMutexMerchantProvider_DoubleSwap(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "first"})
	p.SetMerchant(&mockMerchantForProvider{name: "second"})
	p.SetMerchant(&mockMerchantForProvider{name: "third"})

	if extractMockName(p.GetMerchant()) != "third" {
		t.Fatalf("expected third, got %v", p.GetMerchant())
	}
}

func TestMutexMerchantProvider_MultipleReadersSeeSameValue(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "shared"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if extractMockName(p.GetMerchant()) != "shared" {
				t.Errorf("expected shared, got %v", p.GetMerchant())
			}
		}()
	}
	wg.Wait()
}

func TestMutexMerchantProvider_SwapPropagatesToConsumers(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "degraded"})

	before := extractMockName(p.GetMerchant())
	if before != "degraded" {
		t.Fatalf("expected degraded, got %s", before)
	}

	p.SetMerchant(&mockMerchantForProvider{name: "full"})

	after := extractMockName(p.GetMerchant())
	if after != "full" {
		t.Fatalf("expected full, got %s", after)
	}
}

func TestMutexMerchantProvider_SetToNil(t *testing.T) {
	p := NewMutexMerchantProvider(&mockMerchantForProvider{name: "initial"})

	p.SetMerchant(nil)

	if p.GetMerchant() != nil {
		t.Fatal("expected nil after SetMerchant(nil)")
	}
}
