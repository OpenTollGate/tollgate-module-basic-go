package upstream_session_manager

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
	"github.com/nbd-wtf/go-nostr"
)

// The misfire class of #768: the TEMPORARY captive-portal trigger (workaround
// TW-1) must fire only after a gateway's advertisement VALIDATED, and at most
// once per gateway per process. On rc1 it fired from inside the prober on any
// HTTP 200 from gateway:2121 — before validation, on every 30 s detector
// tick — so a router poked gateway:80 with a browser User-Agent for upstreams
// it had already rejected as non-TollGates (bench evidence: the QEMU lane's
// slirp gateway carried an unrelated mock service on :2121).

// countingProber replaces the network prober and counts trigger invocations —
// in production the trigger makes a real browser-mimic GET to gateway:80, so
// every call is an outbound side effect against a third party.
type countingProber struct {
	mu            sync.Mutex
	advertisement []byte // bytes returned by ProbeGatewayWithContext
	probeErr      error
	triggerCalls  int
}

func (p *countingProber) ProbeGatewayWithContext(ctx context.Context, interfaceName, gatewayIP string) ([]byte, error) {
	if p.probeErr != nil {
		return nil, p.probeErr
	}
	return p.advertisement, nil
}

func (p *countingProber) CancelProbesForInterface(interfaceName string) {}

func (p *countingProber) TriggerCaptivePortalSession(ctx context.Context, gatewayIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggerCalls++
	return nil
}

func (p *countingProber) triggers() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.triggerCalls
}

// emptyMerchant answers the merchant surface NewUpstreamSession touches on
// the adoption path: no accepted mints, no balances (the cold-start shape —
// pricing selection finds nothing compatible, which is fine: these tests pin
// the trigger's gating, not session establishment).
type emptyMerchant struct{}

func (emptyMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", nil
}

func (emptyMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }

func (emptyMerchant) GetBalanceByMint(mintURL string) uint64 { return 0 }

func (emptyMerchant) Fund(cashuToken string) (uint64, error) { return 0, nil }

type providerOf struct {
	m merchant_types.PaymentMerchant
}

func (p providerOf) GetMerchant() merchant_types.PaymentMerchant { return p.m }

// newGatedManager builds the manager white-box with the counting prober and
// a real in-memory ConfigManager (HandleGatewayConnected proceeds into
// NewUpstreamSession, which reads session-renewal defaults), so the tests
// exercise the real gating without network.
func newGatedManager(t *testing.T, prober TollGateProber) *UpstreamSessionManager {
	t.Helper()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	cm, err := config_manager.NewConfigManager("config.json", "install.json", "identities.json")
	if err != nil {
		t.Fatalf("failed to construct test ConfigManager: %v", err)
	}
	return &UpstreamSessionManager{
		configManager:    cm,
		merchantProvider: providerOf{emptyMerchant{}},
		gateways:         make(map[string]*Gateway),
		tollGateProber:   prober,
	}
}

// nonTollGateProbeBody is what a non-TollGate HTTP service answers on :2121 —
// HTTP 200, JSON, and not a kind-10021 advertisement. Any gateway's stock
// management port can produce this shape.
func nonTollGateProbeBody() []byte {
	return []byte(`{"status":"ok","model":"some-other-router"}`)
}

func signedAdvertisementJSON(t *testing.T) []byte {
	t.Helper()
	event := &nostr.Event{
		Kind:    tollgate_protocol.TollGateAdvertisementKind,
		Content: "",
		Tags: nostr.Tags{
			{"metric", "bytes"},
			{"step_size", "22020096"},
			{"price_per_step", "cashu", "210", "sat", "https://mint.example", "1"},
		},
	}
	if err := event.Sign(nostr.GeneratePrivateKey()); err != nil {
		t.Fatalf("failed to sign advertisement: %v", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal advertisement: %v", err)
	}
	return data
}

func TestHandleGatewayConnected_DoesNotTriggerPortalForNonTollGateProbeHit(t *testing.T) {
	prober := &countingProber{advertisement: nonTollGateProbeBody()}
	usm := newGatedManager(t, prober)

	err := usm.HandleGatewayConnected("eth1", "52:54:00:12:34:57", "192.168.13.221")
	if err == nil {
		t.Fatal("expected an error: the probe body is not a TollGate advertisement")
	}
	if got := prober.triggers(); got != 0 {
		t.Fatalf("trigger fired %d times for a gateway whose advertisement failed validation; must be 0", got)
	}
}

func TestHandleGatewayConnected_TriggersPortalOnceForValidatedTollGate(t *testing.T) {
	prober := &countingProber{advertisement: signedAdvertisementJSON(t)}
	usm := newGatedManager(t, prober)

	for attempt := 1; attempt <= 3; attempt++ {
		_ = usm.HandleGatewayConnected("eth1", "52:54:00:12:34:57", "192.168.13.221")
		if got := prober.triggers(); got != 1 {
			t.Fatalf("after %d HandleGatewayConnected calls: trigger fired %d times; the invocation contract is once per gateway per process (#768)",
				attempt, got)
		}
	}
}

func TestHandleGatewayConnected_TriggerPerGatewayIsIndependent(t *testing.T) {
	prober := &countingProber{advertisement: signedAdvertisementJSON(t)}
	usm := newGatedManager(t, prober)

	_ = usm.HandleGatewayConnected("eth1", "52:54:00:12:34:57", "192.168.13.221")
	_ = usm.HandleGatewayConnected("eth1", "52:54:00:12:34:58", "192.168.13.222")

	if got := prober.triggers(); got != 2 {
		t.Fatalf("two distinct validated gateways must each trigger once, got %d", got)
	}
}
