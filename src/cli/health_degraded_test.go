package cli

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// healthDegradedStub embeds the interface (satisfying it without implementing
// its methods) and adds the assertion surface `health` looks for.
type healthDegradedStub struct {
	merchant.MerchantInterface
	degraded bool
	reason   string
}

func (s healthDegradedStub) WalletDegradedInfo() (bool, string) {
	return s.degraded, s.reason
}

// `status` gained wallet_degraded/wallet_reason in #824, but `health` kept
// hardcoding status=ok and message=healthy. That made a merchant that cannot
// sell look healthy to every monitoring loop.
func TestHandleHealthCommand_SurfacesDegradedWallet(t *testing.T) {
	s := &CLIServer{
		startTime:        time.Now(),
		merchantProvider: merchant.NewMutexMerchantProvider(healthDegradedStub{degraded: true, reason: "wallet initialization failed: EOF"}),
	}

	resp := s.processCommand(CLIMessage{Command: "health"})
	health, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", resp.Data)
	}
	if health["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", health["status"])
	}
	if health["wallet_degraded"] != true {
		t.Errorf("wallet_degraded = %v, want true", health["wallet_degraded"])
	}
	if health["wallet_reason"] != "wallet initialization failed: EOF" {
		t.Errorf("wallet_reason = %v", health["wallet_reason"])
	}
	if health["wallet_ok"] != true {
		t.Error("wallet_ok must keep its historical meaning: the merchant object exists")
	}
	if resp.Message != "degraded: wallet initialization failed: EOF" {
		t.Errorf("message = %q", resp.Message)
	}
}

func TestHandleHealthCommand_FullMerchantStaysHealthy(t *testing.T) {
	s := &CLIServer{
		startTime:        time.Now(),
		merchantProvider: merchant.NewMutexMerchantProvider(fullMerchantStub{}),
	}

	resp := s.processCommand(CLIMessage{Command: "health"})
	health, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", resp.Data)
	}
	if health["status"] != "ok" {
		t.Errorf("status = %v, want ok", health["status"])
	}
	if health["wallet_degraded"] != false {
		t.Errorf("wallet_degraded = %v, want false", health["wallet_degraded"])
	}
	if health["wallet_reason"] != "" {
		t.Errorf("wallet_reason = %v, want empty", health["wallet_reason"])
	}
	if resp.Message != "healthy" {
		t.Errorf("message = %q, want healthy", resp.Message)
	}
}

func TestHandleHealthCommand_NilProviderSafe(t *testing.T) {
	s := &CLIServer{startTime: time.Now()}

	resp := s.processCommand(CLIMessage{Command: "health"})
	health, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", resp.Data)
	}
	if health["wallet_ok"] != false {
		t.Errorf("wallet_ok = %v, want false", health["wallet_ok"])
	}
	if health["wallet_degraded"] != false {
		t.Errorf("wallet_degraded = %v, want false", health["wallet_degraded"])
	}
}
