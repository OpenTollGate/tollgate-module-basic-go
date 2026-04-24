package cli

import (
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

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

func getCLIMerchantName(s *CLIServer) string {
	m := s.merchant.GetMerchant()
	if m == nil {
		return "nil"
	}
	if n, ok := m.(*namedMerchant); ok {
		return n.name
	}
	return "unknown"
}

func TestCLIServer_MerchantProvider_SeesInitial(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "initial"})
	s := NewCLIServer(nil, p)

	if getCLIMerchantName(s) != "initial" {
		t.Fatalf("expected initial, got %s", getCLIMerchantName(s))
	}
}

func TestCLIServer_MerchantProvider_SwapPropagates(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})
	s := NewCLIServer(nil, p)

	if getCLIMerchantName(s) != "degraded" {
		t.Fatalf("expected degraded, got %s", getCLIMerchantName(s))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if getCLIMerchantName(s) != "full" {
		t.Fatalf("expected full after swap, got %s", getCLIMerchantName(s))
	}
}

func TestCLIServer_MerchantProvider_NilMerchant(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(nil)
	s := NewCLIServer(nil, p)

	if getCLIMerchantName(s) != "nil" {
		t.Fatalf("expected nil, got %s", getCLIMerchantName(s))
	}
}

func TestCLIServer_MultipleServersShareProvider(t *testing.T) {
	p := merchant.NewMutexMerchantProvider(&namedMerchant{name: "degraded"})

	s1 := NewCLIServer(nil, p)
	s2 := NewCLIServer(nil, p)

	if getCLIMerchantName(s1) != "degraded" {
		t.Fatalf("s1: expected degraded, got %s", getCLIMerchantName(s1))
	}
	if getCLIMerchantName(s2) != "degraded" {
		t.Fatalf("s2: expected degraded, got %s", getCLIMerchantName(s2))
	}

	p.SetMerchant(&namedMerchant{name: "full"})

	if getCLIMerchantName(s1) != "full" {
		t.Fatalf("s1: expected full, got %s", getCLIMerchantName(s1))
	}
	if getCLIMerchantName(s2) != "full" {
		t.Fatalf("s2: expected full, got %s", getCLIMerchantName(s2))
	}
}
