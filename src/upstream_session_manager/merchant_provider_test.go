package upstream_session_manager

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	merchant_types "github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types"
)

type namedMerchant struct {
	name string
}

func (m *namedMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *namedMerchant) GetBalanceByMint(mintURL string) uint64         { return 0 }
func (m *namedMerchant) Fund(cashuToken string) (uint64, error) {
	return 0, fmt.Errorf("mock: %s", m.name)
}

func providerMerchantName(p merchant_types.MerchantProvider) string {
	m := p.GetMerchant()
	if m == nil {
		return "nil"
	}
	if n, ok := m.(*namedMerchant); ok {
		return n.name
	}
	return "unknown"
}

func TestMockMerchantProvider_BasicSwap(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	if providerMerchantName(p) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(p))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(p) != "full" {
		t.Fatalf("expected full, got %s", providerMerchantName(p))
	}
}

func TestMockMerchantProvider_NilMerchant(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(nil)

	if providerMerchantName(p) != "nil" {
		t.Fatalf("expected nil, got %s", providerMerchantName(p))
	}
}

func TestMockMerchantProvider_ConcurrentSwapAndRead(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "initial"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			p.SetMerchant(&namedMerchant{name: fmt.Sprintf("merchant-%d", n)})
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

func TestUpstreamSessionManager_StoresMerchantProvider(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "test"})
	usm := &UpstreamSessionManager{
		merchantProvider: p,
	}

	if providerMerchantName(usm.merchantProvider) != "test" {
		t.Fatalf("expected test, got %s", providerMerchantName(usm.merchantProvider))
	}
}

func TestUpstreamSessionManager_SwapPropagates(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})
	usm := &UpstreamSessionManager{
		merchantProvider: p,
	}

	if providerMerchantName(usm.merchantProvider) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(usm.merchantProvider))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(usm.merchantProvider) != "full" {
		t.Fatalf("expected full after swap, got %s", providerMerchantName(usm.merchantProvider))
	}
}

func TestUpstreamSession_MerchantProviderPropagates(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	session := &UpstreamSession{
		GatewayIP:        "192.168.1.1",
		merchantProvider: p,
	}

	if providerMerchantName(session.merchantProvider) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(session.merchantProvider))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(session.merchantProvider) != "full" {
		t.Fatalf("expected full after swap, got %s", providerMerchantName(session.merchantProvider))
	}
}

func TestUpstreamSession_MultipleSessionsShareProvider(t *testing.T) {
	p := merchant_types.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	s1 := &UpstreamSession{GatewayIP: "192.168.1.1", merchantProvider: p}
	s2 := &UpstreamSession{GatewayIP: "192.168.1.2", merchantProvider: p}

	if providerMerchantName(s1.merchantProvider) != "degraded" {
		t.Fatalf("s1: expected degraded, got %s", providerMerchantName(s1.merchantProvider))
	}
	if providerMerchantName(s2.merchantProvider) != "degraded" {
		t.Fatalf("s2: expected degraded, got %s", providerMerchantName(s2.merchantProvider))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(s1.merchantProvider) != "full" {
		t.Fatalf("s1: expected full, got %s", providerMerchantName(s1.merchantProvider))
	}
	if providerMerchantName(s2.merchantProvider) != "full" {
		t.Fatalf("s2: expected full, got %s", providerMerchantName(s2.merchantProvider))
	}
}
