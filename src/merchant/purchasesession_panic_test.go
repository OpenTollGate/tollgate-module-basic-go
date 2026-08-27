package merchant

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/nbd-wtf/go-nostr"
)

// panicToken is a minimal tollwallet.Token — just enough for PurchaseSession
// to log and forward it to Receive.
type panicToken struct{}

func (panicToken) Mint() string               { return "https://panic-mint.example.com" }
func (panicToken) Amount() uint64             { return 1 }
func (panicToken) Serialize() (string, error) { return "cashuAstub", nil }
func (panicToken) Close()                     {}

// panicReceiveWallet stubs DecodeToken and Receive; every other WalletPort
// method panics via the embedded nil interface, so untested wallet
// interactions cannot pass silently.
type panicReceiveWallet struct {
	tollwallet.WalletPort
}

func (w *panicReceiveWallet) DecodeToken(tokenStr string) (tollwallet.Token, error) {
	return panicToken{}, nil
}

func (w *panicReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	panic("wallet exploded: simulated keyset corruption")
}

// TestPurchaseSessionPanicContainment pins the panic-containment contract of
// the Receive goroutine in PurchaseSession: a panic inside the wallet layer
// must surface to the caller as an explicit "panicked" payment-processing
// error within 5 seconds — NOT crash the process and NOT degrade into the
// 30s "payment-processing-timeout" notice.
func TestPurchaseSessionPanicContainment(t *testing.T) {
	cm, _ := setupTestConfigManager(t)
	m := &Merchant{
		tollwallet:        &panicReceiveWallet{},
		configManager:     cm,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}

	type psResult struct {
		event *nostr.Event
		err   error
	}
	done := make(chan psResult, 1)
	start := time.Now()
	go func() {
		event, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
		done <- psResult{event, err}
	}()

	select {
	case res := <-done:
		elapsed := time.Since(start)
		if elapsed >= 5*time.Second {
			t.Fatalf("PurchaseSession returned after %v — panic degraded to a slow path, want explicit error <5s", elapsed)
		}
		if res.err != nil {
			t.Fatalf("expected notice event with nil error, got error: %v", res.err)
		}
		if res.event == nil {
			t.Fatal("expected non-nil notice event")
		}
		if res.event.Kind != 21023 {
			t.Fatalf("notice kind = %d, want 21023", res.event.Kind)
		}
		var code string
		for _, tag := range res.event.Tags {
			if len(tag) >= 2 && tag[0] == "code" {
				code = tag[1]
				break
			}
		}
		if code != "payment-processing-failed" {
			t.Fatalf("notice code = %q, want %q", code, "payment-processing-failed")
		}
		if !strings.Contains(res.event.Content, "panicked") {
			t.Fatalf("notice content = %q, want it to contain %q", res.event.Content, "panicked")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PurchaseSession did not return within 5s — panic NOT contained (process death or 30s timeout path)")
	}
}
