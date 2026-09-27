package merchant

import (
	"fmt"
	"reflect"
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

// SwapFeeSats reports no fee so PurchaseSession skips the fee pre-check #409
// adds ahead of the NDS pre-flight; without this stub the nil-embedded
// WalletPort panics once both changes share a tree (verified on a merged
// test branch).
func (w *preflightWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }

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

// stubPreflightProbe drives the payment pre-flight of one merchant. It cannot
// be a package-level override: the usage monitor of an earlier test's merchant
// is still running in this test binary and reads the default probe on its own
// goroutine, so writing a package-level seam here is a data race the suite's
// `-race` gate reports as a failure of whichever test happens to be running.
func stubPreflightProbe(t *testing.T, m *Merchant, probe func(mac string) (valve.ClientState, error)) {
	t.Helper()
	m.setClientProbe(probe, time.Millisecond)
	t.Cleanup(func() { m.setClientProbe(nil, 0) })
}

// TestPurchaseSessionPreflightRefusesUnregisteredClient pins the fund-safety
// contract of issue #403 trigger (a): when NDS has no client session for the
// paying MAC, the payment must be refused BEFORE Receive — the token is never
// consumed, so the customer keeps custody.
func TestPurchaseSessionPreflightRefusesUnregisteredClient(t *testing.T) {
	m, receiveCalled := newPreflightMerchant(t)
	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
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
	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
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
	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
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

// TestClientProbeSeamIsPerMerchantAndDefaultsToTheNdsProbe pins the seam that
// replaced the package-level pre-flight probe. Two properties matter, both of
// them reasons the package-level version was a data race: a merchant with no
// seam probes with the router's own probe at the production retry delay, and
// stubbing one merchant leaves every other merchant on that default — so the
// pre-flight tests never write state a usage-monitor goroutine of another
// merchant can read.
func TestClientProbeSeamIsPerMerchantAndDefaultsToTheNdsProbe(t *testing.T) {
	stubbed, _ := newPreflightMerchant(t)
	untouched, _ := newPreflightMerchant(t)

	defaultProbe := reflect.ValueOf(valve.CheckClientState).Pointer()

	probe, delay := untouched.clientProbeSeam()
	if got := reflect.ValueOf(probe).Pointer(); got != defaultProbe {
		t.Fatalf("a merchant with no pre-flight seam must probe with valve.CheckClientState (%v), got %v", defaultProbe, got)
	}
	if delay != preflightRetryDelayDefault {
		t.Fatalf("default pre-flight retry delay = %v, want %v", delay, preflightRetryDelayDefault)
	}

	stubPreflightProbe(t, stubbed, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	stubbedProbe, stubbedDelay := stubbed.clientProbeSeam()
	if reflect.ValueOf(stubbedProbe).Pointer() == defaultProbe {
		t.Fatal("the stubbed merchant still probes with the package default")
	}
	if stubbedDelay != time.Millisecond {
		t.Fatalf("stubbed pre-flight retry delay = %v, want %v", stubbedDelay, time.Millisecond)
	}

	otherProbe, otherDelay := untouched.clientProbeSeam()
	if reflect.ValueOf(otherProbe).Pointer() != defaultProbe || otherDelay != preflightRetryDelayDefault {
		t.Fatal("stubbing one merchant's pre-flight changed another merchant's seam: the seam is not per merchant")
	}
}
