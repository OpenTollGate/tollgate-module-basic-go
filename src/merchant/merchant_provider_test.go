package merchant

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

// stubMerchant is a minimal MerchantInterface implementation for tests.
type stubMerchant struct {
	label string
}

func (s *stubMerchant) CreatePaymentToken(string, uint64) (string, error) {
	return "", fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) CreatePaymentTokenWithOverpayment(string, uint64, uint64, uint64) (string, error) {
	return "", fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) DrainMint(string) (string, uint64, error) {
	return "", 0, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) RequestLightningInvoice(string, string, uint64) (*LightningInvoice, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) GetLightningInvoiceStatus(string, string) (*LightningQuoteStatus, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (s *stubMerchant) GetBalance() uint64                            { return 0 }
func (s *stubMerchant) GetBalanceByMint(string) uint64                { return 0 }
func (s *stubMerchant) GetAllMintBalances() map[string]uint64         { return nil }
func (s *stubMerchant) PurchaseSession(string, string) (*nostr.Event, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) GetAdvertisement() string  { return s.label }
func (s *stubMerchant) StartPayoutRoutine()       {}
func (s *stubMerchant) StartDataUsageMonitoring() {}
func (s *stubMerchant) CreateNoticeEvent(string, string, string, string) (*nostr.Event, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) GetSession(string) (*CustomerSession, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) AddAllotment(string, string, uint64) (*CustomerSession, error) {
	return nil, fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) GetUsage(string) (string, error) {
	return "", fmt.Errorf("stub: %s", s.label)
}
func (s *stubMerchant) Fund(string) (uint64, error) {
	return 0, fmt.Errorf("stub: %s", s.label)
}

// TestMutexMerchantProviderSatisfiesInterface verifies the compile-time
// interface assertion in merchant_provider.go is valid.
func TestMutexMerchantProviderSatisfiesInterface(t *testing.T) {
	// This line mirrors the compile-time check already present in production
	// code. If MutexMerchantProvider does not satisfy MerchantProvider, the
	// test will not compile.
	var _ MerchantProvider = (*MutexMerchantProvider)(nil)

	// Also verify runtime behaviour: a concrete instance works through the
	// interface.
	s := &stubMerchant{label: "check"}
	var p MerchantProvider = NewMerchantProvider(s)
	if p.GetMerchant() != s {
		t.Fatal("expected GetMerchant to return the initial merchant")
	}
}

// TestMerchantProviderGetSet verifies basic set-then-get round-trip.
func TestMerchantProviderGetSet(t *testing.T) {
	initial := &stubMerchant{label: "initial"}
	p := NewMerchantProvider(initial)

	if got := p.GetMerchant(); got != initial {
		t.Fatal("GetMerchant should return the initial merchant")
	}

	replacement := &stubMerchant{label: "replacement"}
	p.SetMerchant(replacement)

	if got := p.GetMerchant(); got != replacement {
		t.Fatal("GetMerchant should return the replacement after SetMerchant")
	}
}

// TestMerchantProviderConcurrentAccess hammers GetMerchant and SetMerchant
// from many goroutines to confirm no data races.
func TestMerchantProviderConcurrentAccess(t *testing.T) {
	p := NewMerchantProvider(&stubMerchant{label: "seed"})

	var wg sync.WaitGroup

	// 100 readers
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := p.GetMerchant()
			if m == nil {
				t.Error("GetMerchant returned nil")
			}
		}()
	}

	// 10 writers — each swaps to a unique stub
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.SetMerchant(&stubMerchant{label: fmt.Sprintf("writer-%d", id)})
		}(i)
	}

	wg.Wait()
}

// TestMerchantProviderSwapPropagation verifies that after swapping the
// merchant, every subsequent GetMerchant sees the new instance.
func TestMerchantProviderSwapPropagation(t *testing.T) {
	mockA := &stubMerchant{label: "A"}
	mockB := &stubMerchant{label: "B"}

	p := NewMerchantProvider(mockA)

	// Before swap
	if got := p.GetMerchant(); got != mockA {
		t.Fatal("expected merchant A before swap")
	}

	p.SetMerchant(mockB)

	// After swap — verify several times to catch potential flapping
	for i := 0; i < 10; i++ {
		if got := p.GetMerchant(); got != mockB {
			t.Fatalf("call %d: expected merchant B after swap, got %v", i, got)
		}
	}
}

// TestMintHealthTrackerStopIdempotent verifies that calling Stop() more than
// once does not panic (it uses sync.Once internally).
func TestMintHealthTrackerStopIdempotent(t *testing.T) {
	tracker := NewMintHealthTracker(nil)

	// Calling Stop twice must not panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Stop() panicked on second call: %v", r)
			}
		}()
		tracker.Stop()
		tracker.Stop()
	}()
}

// TestDegradedToFullRecovery simulates the degraded-to-full recovery path:
// start with a degraded-style merchant, swap in a full merchant via the
// provider, and confirm GetMerchant returns the new one.
func TestDegradedToFullRecovery(t *testing.T) {
	degraded := &stubMerchant{label: "degraded"}
	full := &stubMerchant{label: "full"}

	provider := NewMerchantProvider(degraded)

	if got := provider.GetMerchant().GetAdvertisement(); got != "degraded" {
		t.Fatalf("expected degraded merchant first, got %q", got)
	}

	// Simulate recovery: swap to the full merchant.
	provider.SetMerchant(full)

	if got := provider.GetMerchant().GetAdvertisement(); got != "full" {
		t.Fatalf("expected full merchant after recovery swap, got %q", got)
	}
}
