package upstream_session_manager

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

type namedMerchant struct {
	name string
}

func (m *namedMerchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *namedMerchant) GetBalance() uint64                             { return 0 }
func (m *namedMerchant) GetBalanceByMint(mintURL string) uint64         { return 0 }
func (m *namedMerchant) GetAllMintBalances() map[string]uint64          { return nil }
func (m *namedMerchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) GetAdvertisement() string { return "" }
func (m *namedMerchant) StartPayoutRoutine()     {}
func (m *namedMerchant) StartDataUsageMonitoring() {}
func (m *namedMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) AddAllotment(macAddress, metric string, amount uint64) (*merchant.CustomerSession, error) {
	return nil, fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) GetUsage(macAddress string) (string, error) {
	return "", fmt.Errorf("mock: %s", m.name)
}
func (m *namedMerchant) Fund(cashuToken string) (uint64, error) {
	return 0, fmt.Errorf("mock: %s", m.name)
}

func providerMerchantName(p merchant.MerchantProvider) string {
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
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	if providerMerchantName(p) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(p))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(p) != "full" {
		t.Fatalf("expected full, got %s", providerMerchantName(p))
	}
}

func TestMockMerchantProvider_NilMerchant(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(nil)

	if providerMerchantName(p) != "nil" {
		t.Fatalf("expected nil, got %s", providerMerchantName(p))
	}
}

func TestMockMerchantProvider_ConcurrentSwapAndRead(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "initial"})

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
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "test"})
	usm := &UpstreamSessionManager{
		merchant: p,
	}

	if providerMerchantName(usm.merchant) != "test" {
		t.Fatalf("expected test, got %s", providerMerchantName(usm.merchant))
	}
}

func TestUpstreamSessionManager_SwapPropagates(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})
	usm := &UpstreamSessionManager{
		merchant: p,
	}

	if providerMerchantName(usm.merchant) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(usm.merchant))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(usm.merchant) != "full" {
		t.Fatalf("expected full after swap, got %s", providerMerchantName(usm.merchant))
	}
}

func TestUpstreamSession_MerchantProviderPropagates(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	session := &UpstreamSession{
		GatewayIP: "192.168.1.1",
		merchant:  p,
	}

	if providerMerchantName(session.merchant) != "degraded" {
		t.Fatalf("expected degraded, got %s", providerMerchantName(session.merchant))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(session.merchant) != "full" {
		t.Fatalf("expected full after swap, got %s", providerMerchantName(session.merchant))
	}
}

func TestUpstreamSession_MultipleSessionsShareProvider(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	s1 := &UpstreamSession{GatewayIP: "192.168.1.1", merchant: p}
	s2 := &UpstreamSession{GatewayIP: "192.168.1.2", merchant: p}

	if providerMerchantName(s1.merchant) != "degraded" {
		t.Fatalf("s1: expected degraded, got %s", providerMerchantName(s1.merchant))
	}
	if providerMerchantName(s2.merchant) != "degraded" {
		t.Fatalf("s2: expected degraded, got %s", providerMerchantName(s2.merchant))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if providerMerchantName(s1.merchant) != "full" {
		t.Fatalf("s1: expected full, got %s", providerMerchantName(s1.merchant))
	}
	if providerMerchantName(s2.merchant) != "full" {
		t.Fatalf("s2: expected full, got %s", providerMerchantName(s2.merchant))
	}
}
