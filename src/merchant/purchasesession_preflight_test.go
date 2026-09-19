package merchant

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/nbd-wtf/go-nostr"
)

// preflightToken is a minimal tollwallet.Token — just enough for PurchaseSession
// to log it and forward it to Receive.
type preflightToken struct{}

func (preflightToken) Mint() string               { return "https://preflight-mint.example.com" }
func (preflightToken) Amount() uint64             { return 1 }
func (preflightToken) Serialize() (string, error) { return "cashuAstub", nil }
func (preflightToken) Close()                     {}

// preflightWallet stubs DecodeToken and records whether Receive was reached;
// every other WalletPort method panics via the embedded nil interface.
type preflightWallet struct {
	tollwallet.WalletPort
	receiveCalled *bool
}

func (w *preflightWallet) DecodeToken(tokenStr string) (tollwallet.Token, error) {
	return preflightToken{}, nil
}

func (w *preflightWallet) Receive(tollwallet.Token) (uint64, error) {
	*w.receiveCalled = true
	return 1, nil
}

func newPreflightMerchant(t *testing.T) (*Merchant, *bool) {
	t.Helper()
	receiveCalled := new(bool)
	cm, _ := setupTestConfigManager(t)
	m := &Merchant{
		config:            cm.GetConfig(),
		configManager:     cm,
		tollwallet:        &preflightWallet{receiveCalled: receiveCalled},
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}
	return m, receiveCalled
}

func noticeCode(t *testing.T, event *nostr.Event) string {
	t.Helper()
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "code" {
			return tag[1]
		}
	}
	t.Fatal("notice event has no code tag")
	return ""
}

func stubPreflightProbe(t *testing.T, probe func(mac string) (valve.ClientState, error)) {
	t.Helper()
	origProbe, origDelay := ndsClientCheck, preflightRetryDelay
	t.Cleanup(func() {
		ndsClientCheck = origProbe
		preflightRetryDelay = origDelay
	})
	ndsClientCheck = probe
	preflightRetryDelay = time.Millisecond
}

// TestPurchaseSessionPreflightRefusesUnregisteredClient pins the fund-safety
// contract of issue #403 trigger (a): when NDS has no client session for the
// paying MAC, the payment must be refused BEFORE Receive — the token is never
// consumed, so the customer keeps custody.
func TestPurchaseSessionPreflightRefusesUnregisteredClient(t *testing.T) {
	m, receiveCalled := newPreflightMerchant(t)
	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{}, nil
	})

	event, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("expected notice event with nil error, got error: %v", err)
	}
	if event == nil || event.Kind != 21023 {
		t.Fatalf("expected kind-21023 notice, got %+v", event)
	}
	if code := noticeCode(t, event); code != "client-not-registered" {
		t.Fatalf("notice code = %q, want %q", code, "client-not-registered")
	}
	if !strings.Contains(event.Content, "Reconnect") {
		t.Fatalf("notice content = %q, want an actionable reconnect instruction", event.Content)
	}
	if *receiveCalled {
		t.Fatal("Receive must not be called when the pre-flight refuses the client")
	}
}

// TestPurchaseSessionPreflightFailsOpenOnProbeError verifies a broken probe
// (ndsctl error, bad JSON) never becomes a denial of service: the payment path
// proceeds exactly as before the pre-flight existed.
func TestPurchaseSessionPreflightFailsOpenOnProbeError(t *testing.T) {
	m, receiveCalled := newPreflightMerchant(t)
	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{}, errPreflightProbeStub
	})

	_, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("expected notice-or-session event with nil error, got error: %v", err)
	}
	if !*receiveCalled {
		t.Fatal("probe error must fail open — Receive should still be reached")
	}
}

// TestPurchaseSessionPreflightRetriesUntilRegistered mirrors the valve auth
// retry semantics at payment time: the reseller flow's upstream NDS registers
// the client session asynchronously, so a not-yet-registered MAC must be
// re-probed, not refused on first sight.
func TestPurchaseSessionPreflightRetriesUntilRegistered(t *testing.T) {
	m, receiveCalled := newPreflightMerchant(t)
	probes := 0
	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		probes++
		if probes < 3 {
			return valve.ClientState{}, nil
		}
		return valve.ClientState{Registered: true}, nil
	})

	_, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("expected notice-or-session event with nil error, got error: %v", err)
	}
	if probes != 3 {
		t.Fatalf("expected the probe to be retried until registered (3 probes), got %d", probes)
	}
	if !*receiveCalled {
		t.Fatal("payment must proceed once the client registers")
	}
}

var errPreflightProbeStub = fmt.Errorf("stub: ndsctl probe failure")
