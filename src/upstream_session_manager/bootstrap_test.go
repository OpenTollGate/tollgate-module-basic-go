package upstream_session_manager

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	merchant_types "github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollgate_protocol"
)

// --- test doubles -----------------------------------------------------------

// bootstrapMerchant fakes the merchant surface the bootstrap candidate check
// reads: balances per mint, and the accepted-mint list.
type bootstrapMerchant struct {
	mu       sync.Mutex
	balances map[string]uint64
}

func (m *bootstrapMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", nil
}

func (m *bootstrapMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }

func (m *bootstrapMerchant) GetBalanceByMint(mintURL string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[mintURL]
}

func (m *bootstrapMerchant) Fund(cashuToken string) (uint64, error) { return 0, nil }

func newBootstrapMerchant() *bootstrapMerchant {
	return &bootstrapMerchant{balances: map[string]uint64{}}
}

// fakeForwarder records what the "upstream" was sent and what to answer.
type fakeForwarder struct {
	mu        sync.Mutex
	calls     []struct{ gateway, token string }
	allotment uint64
	err       error
	block     chan struct{} // non-nil: hold the forward open until closed
	release   chan struct{}
	heldOnce  sync.Once
}

func (f *fakeForwarder) forward(gatewayIP, token string) (uint64, error) {
	f.mu.Lock()
	f.calls = append(f.calls, struct{ gateway, token string }{gatewayIP, token})
	n := len(f.calls)
	f.mu.Unlock()
	if f.block != nil && n == 1 {
		<-f.block // hold the first call so a concurrent ForwardFirstProof sees it in flight
	}
	return f.allotment, f.err
}

// coldStartSession builds a session that is a bootstrap candidate:
// no allotment, no wallet balance, and a candidate check that reads config
// through a stub. Sessions constructed this way never touch the network
// (no tracker, no forwarder call) unless a test explicitly calls a forward.
func coldStartSession(t *testing.T, m merchant_types.PaymentMerchant) *UpstreamSession {
	t.Helper()
	return &UpstreamSession{
		GatewayIP:         "192.168.88.1",
		AdvertisementInfo: &tollgate_protocol.AdvertisementInfo{Metric: "bytes", StepSize: 1048576},
		merchantProvider:  merchant_types.NewMutexMerchantProvider(m),
	}
}

// --- candidate detection ----------------------------------------------------

func TestBootstrapCandidateDirectGatewayNeverBootstraps(t *testing.T) {
	// reseller mode off → never a candidate, regardless of wallet state.
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = nil // GetConfig path short-circuits to false

	if s.BootstrapCandidate() {
		t.Fatal("direct gateway (reseller mode off) must never be a bootstrap candidate")
	}
}

func TestBootstrapCandidateColdStart(t *testing.T) {
	// no upstream session + zero wallet balance → candidate.
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)

	if !s.BootstrapCandidate() {
		t.Fatal("cold-start reseller (no session, no funds) should be a bootstrap candidate")
	}
	if !s.EnsureBootstrapForColdStart() {
		t.Fatal("EnsureBootstrapForColdStart should arm bootstrap on a candidate")
	}
}

func TestBootstrapCandidateFundedWalletIsNotColdStart(t *testing.T) {
	// A reseller holding a balance at the upstream's mint can pay the normal
	// way — it must NOT enter bootstrap mode.
	m := newBootstrapMerchant()
	m.balances["https://mint.example"] = 1000
	s := coldStartSession(t, m)
	s.configManager = resellerConfigManager(t, true)
	s.AdvertisementInfo.PricingOptions = []tollgate_protocol.PricingOption{
		{MintURL: "https://mint.example", PricePerStep: 1, MinSteps: 1},
	}

	if s.BootstrapCandidate() {
		t.Fatal("funded reseller must not be a bootstrap candidate")
	}
	if s.EnsureBootstrapForColdStart() {
		t.Fatal("EnsureBootstrapForColdStart must not arm bootstrap on a funded reseller")
	}
}

func TestBootstrapCandidateExistingSessionIsNotColdStart(t *testing.T) {
	// Upstream session already up → warm path, no bootstrap.
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.paymentMu.Lock()
	s.TotalAllotment = 10 * 1048576
	s.paymentMu.Unlock()

	if s.BootstrapCandidate() {
		t.Fatal("session with an established upstream allotment must not be a bootstrap candidate")
	}
}

// --- first-proof forward: no split -------------------------------------------

func TestForwardFirstProofForwardsEntireProofOnce(t *testing.T) {
	const proof = "cashuAeyJ0b2tlbiI6eyJtaW50IjoiaHR0cHM6Ly9taW50LmV4YW1wbGUiLCJwcm9vZnMiOlt7YW1vdW50OjJ9XX19"

	fwd := &fakeForwarder{allotment: 2 * 1048576}
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = fwd.forward

	s.EnsureBootstrapForColdStart()

	allotment, err := s.ForwardFirstProof(proof)
	if err != nil {
		t.Fatalf("ForwardFirstProof failed: %v", err)
	}
	if allotment != 2*1048576 {
		t.Fatalf("allotment = %d, want %d", allotment, 2*1048576)
	}

	// Exactly one forward, carrying the ENTIRE proof verbatim — no split.
	fwd.mu.Lock()
	calls := append([]struct{ gateway, token string }{}, fwd.calls...)
	fwd.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 forward call, got %d", len(calls))
	}
	if calls[0].gateway != "192.168.88.1" {
		t.Errorf("forward sent to %s, want the session's gateway 192.168.88.1", calls[0].gateway)
	}
	if calls[0].token != proof {
		t.Errorf("forwarded token %q differs from the customer proof — the first proof must be forwarded WHOLE", calls[0].token)
	}

	// Session bookkeeping + bootstrap exit.
	s.paymentMu.Lock()
	total := s.TotalAllotment
	payments := s.PaymentCount
	s.paymentMu.Unlock()
	if total != 2*1048576 {
		t.Errorf("session TotalAllotment = %d, want the granted %d", total, 2*1048576)
	}
	if payments != 1 {
		t.Errorf("PaymentCount = %d, want 1", payments)
	}

	st := s.BootstrapStatus()
	if st.Phase != BootstrapComplete {
		t.Errorf("phase = %q, want %q", st.Phase, BootstrapComplete)
	}
	if st.Active {
		t.Error("bootstrap must be exited (active=false) after the upstream session is established")
	}
	if st.Allotment != 2*1048576 {
		t.Errorf("status allotment = %d, want %d", st.Allotment, 2*1048576)
	}
	if st.Reference == "" || strings.Contains(st.Reference, proof) {
		t.Errorf("reference must be a short digest, not the raw proof: %q", st.Reference)
	}
}

func TestForwardFirstProofReplayReturnsRecordedAllotment(t *testing.T) {
	// A proof that already completed must never be forwarded twice: the
	// upstream would reject it as spent and the value would be stranded.
	const proof = "cashuAsecondProof"
	fwd := &fakeForwarder{allotment: 1048576}
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = fwd.forward

	if _, err := s.ForwardFirstProof(proof); err != nil {
		t.Fatalf("first forward failed: %v", err)
	}
	allotment, err := s.ForwardFirstProof(proof)
	if err != nil {
		t.Fatalf("replay should return the recorded result, got error: %v", err)
	}
	if allotment != 1048576 {
		t.Fatalf("replay allotment = %d, want %d", allotment, 1048576)
	}

	fwd.mu.Lock()
	n := len(fwd.calls)
	fwd.mu.Unlock()
	if n != 1 {
		t.Fatalf("the same proof was forwarded %d times; a completed proof must never be re-sent", n)
	}
}

func TestForwardFirstProofInFlightBlocksSecondForward(t *testing.T) {
	// Two customers paying at once: only one forward may be on the wire.
	const proofA, proofB = "cashuAfirstCustomer", "cashuAsecondCustomer"
	fwd := &fakeForwarder{allotment: 1048576}
	fwd.block = make(chan struct{})
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = fwd.forward

	done := make(chan error, 1)
	go func() {
		_, err := s.ForwardFirstProof(proofA)
		done <- err
	}()

	waitForBootstrapPhase(t, s, BootstrapForwarding)

	_, err := s.ForwardFirstProof(proofB)
	if err == nil {
		t.Fatal("a second forward while the first is in flight must be refused")
	}
	if !strings.Contains(err.Error(), "in progress") && !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("unexpected refusal reason: %v", err)
	}

	// Release the first forward; it must complete.
	close(fwd.block)
	if err := <-done; err != nil {
		t.Fatalf("first forward failed: %v", err)
	}

	fwd.mu.Lock()
	tokens := make([]string, 0, len(fwd.calls))
	for _, c := range fwd.calls {
		tokens = append(tokens, c.token)
	}
	fwd.mu.Unlock()
	if len(tokens) != 1 || tokens[0] != proofA {
		t.Fatalf("expected only proofA on the wire, got %v", tokens)
	}
}

func TestForwardFirstProofErrorRecordsReferenceAndDoesNotRetry(t *testing.T) {
	// The POST failed: the proof may already be spent at the mint, so the
	// failure is recorded and never retried automatically.
	const proof = "cashuAfailedForward"
	fwd := &fakeForwarder{err: errBoom}
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = fwd.forward

	if _, err := s.ForwardFirstProof(proof); err == nil {
		t.Fatal("expected the forward failure to surface")
	}

	st := s.BootstrapStatus()
	if st.Phase != BootstrapError {
		t.Errorf("phase = %q, want %q", st.Phase, BootstrapError)
	}
	if st.Error == "" || !strings.Contains(st.Error, "boom") {
		t.Errorf("status error = %q, want it to carry the cause", st.Error)
	}
	if st.Reference == "" {
		t.Error("the failed proof's reference must be retained for reconciliation")
	}

	// A retry of the same proof after an error is refused (the POST may have
	// been applied upstream); it is subject to operator reconciliation, not
	// automatic resend.
	if _, err := s.ForwardFirstProof(proof); err == nil {
		t.Fatal("a failed forward must not be silently retried")
	}
	fwd.mu.Lock()
	n := len(fwd.calls)
	fwd.mu.Unlock()
	if n != 1 {
		t.Fatalf("the failed proof was re-sent %d times; ambiguous outcomes must be reconciled, not retried", n)
	}
}

func TestForwardFirstProofZeroAllotmentIsError(t *testing.T) {
	fwd := &fakeForwarder{allotment: 0}
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = fwd.forward

	if _, err := s.ForwardFirstProof("cashuAzero"); err == nil {
		t.Fatal("a zero allotment from the upstream must be an error")
	}
	if st := s.BootstrapStatus(); st.Phase != BootstrapError {
		t.Errorf("phase = %q, want %q", st.Phase, BootstrapError)
	}
}

func TestForwardFirstProofEmptyTokenRefused(t *testing.T) {
	s := coldStartSession(t, newBootstrapMerchant())
	s.configManager = resellerConfigManager(t, true)
	s.bootstrapForwarder = (&fakeForwarder{allotment: 1}).forward

	if _, err := s.ForwardFirstProof(""); err == nil {
		t.Fatal("an empty token must be refused")
	}
}

// --- status endpoint surface --------------------------------------------------

func TestBootstrapStatusDefaultsToIdle(t *testing.T) {
	s := coldStartSession(t, newBootstrapMerchant())
	st := s.BootstrapStatus()
	if st.Phase != BootstrapIdle {
		t.Errorf("phase = %q, want the idle default %q", st.Phase, BootstrapIdle)
	}
	if st.Active {
		t.Error("a session that never armed bootstrap must not report active")
	}
	if st.GatewayIP != "192.168.88.1" {
		t.Errorf("gateway_ip = %q, want the session gateway", st.GatewayIP)
	}
}

// --- manager-level API --------------------------------------------------------

func TestManagerForwardFirstProofUnknownGateway(t *testing.T) {
	// The manager surfaces per-gateway bootstrap control for the merchant
	// side of #239. An unknown gateway is a clean error, not a panic.
	usm := &UpstreamSessionManager{
		merchantProvider: merchant_types.NewMutexMerchantProvider(newBootstrapMerchant()),
	}
	if _, err := usm.ForwardFirstProof("192.168.99.99", "cashuAtoken"); err == nil {
		t.Fatal("expected an error for an unknown gateway")
	}
}

func TestManagerGetBootstrapStatusSkipsSessionlessGateways(t *testing.T) {
	usm := &UpstreamSessionManager{
		merchantProvider: merchant_types.NewMutexMerchantProvider(newBootstrapMerchant()),
		gateways: map[string]*Gateway{
			"192.168.88.1": {GatewayIP: "192.168.88.1", Session: nil},
		},
	}
	st := usm.GetBootstrapStatus()
	if len(st) != 0 {
		t.Fatalf("gateways without sessions must not appear in the snapshot, got %v", st)
	}
}

// --- helpers -------------------------------------------------------------------

var errBoom = &boomError{}

type boomError struct{}

func (e *boomError) Error() string { return "upstream exploded: boom" }

// resellerConfigManager returns a ConfigManager whose loaded config has
// reseller_mode set to the given value. It drives the real config load path
// against a temp config file so the candidate check exercises production
// code, not a stubbed getter.
func resellerConfigManager(t *testing.T, resellerMode bool) *config_manager.ConfigManager {
	t.Helper()
	dir := t.TempDir()
	cm, err := config_manager.NewConfigManager(
		dir+"/config.json",
		dir+"/install.json",
		dir+"/identities.json",
	)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	if err := config_manager.SetDotPath(cm, "reseller_mode", boolString(resellerMode)); err != nil {
		t.Fatalf("SetDotPath(reseller_mode): %v", err)
	}
	return cm
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func waitForBootstrapPhase(t *testing.T, s *UpstreamSession, want BootstrapPhase) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.BootstrapStatus().Phase == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("phase never reached %q (last: %q)", want, s.BootstrapStatus().Phase)
}
