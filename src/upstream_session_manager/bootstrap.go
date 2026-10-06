package upstream_session_manager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// This file implements the cold-start bootstrap of #239 ("Reseller Bootstrap:
// Zero-Float Mesh Formation", phase 2a).
//
// A freshly configured reseller has no internet, no upstream session and no
// ecash balance. When its first customer pays, the reseller cannot swap the
// proof at the mint (that needs the internet it does not have yet), so the
// wallet-funded payment in HandleRenewal can never succeed. The first proof is
// therefore forwarded *whole* to the upstream TollGate over the local link:
// no swap, no split, no margin on the first transaction. The upstream validates
// the proof at the mint, authorizes the reseller's STA, and the reseller now
// has an upstream data session (and, by giving the customer its normal local
// allotment out of that session, keeps the remainder as prepaid float).
//
// BootstrapPhase models that state machine; UpstreamSession.ForwardFirstProof
// drives it. The warm path (a session already exists; later payments swap and
// split normally) is unchanged.
type BootstrapPhase string

const (
	// BootstrapIdle: bootstrap is armed but no proof has been forwarded yet.
	BootstrapIdle BootstrapPhase = "idle"
	// BootstrapForwarding: the customer's entire first proof is on the wire to
	// the upstream TollGate. The point of no return: once this POST is sent the
	// proof may already be spent at the mint even if the answer is lost.
	BootstrapForwarding BootstrapPhase = "forwarding"
	// BootstrapEstablishing: the upstream accepted the proof and returned an
	// allotment; the reseller is bringing the data session up locally.
	BootstrapEstablishing BootstrapPhase = "establishing"
	// BootstrapComplete: the upstream session is up and bootstrap is exited.
	// Subsequent payments take the normal wallet-funded, split path.
	BootstrapComplete BootstrapPhase = "complete"
	// BootstrapError: the forward failed or the proof was rejected. The proof
	// reference is retained for reconciliation and there is deliberately no
	// automatic retry — an ambiguous network result must be reconciled, not
	// blindly resent (the same rule the rest of the wallet follows).
	BootstrapError BootstrapPhase = "error"
)

// BootstrapStatus is an immutable snapshot of one session's cold-start
// bootstrap state. It is safe to serialise to an HTTP handler; phase 2b/3 of
// #239 surface it as `bootstrap_phase`.
type BootstrapStatus struct {
	Active    bool           `json:"active"`
	Phase     BootstrapPhase `json:"phase"`
	GatewayIP string         `json:"gateway_ip"`
	Allotment uint64         `json:"allotment"`
	// Reference is a short, non-reversible digest of the forwarded proof: what
	// an operator quotes to find the transaction. The raw token is never kept
	// here, so no secret can leak through a status endpoint or a log line.
	Reference string    `json:"reference,omitempty"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// proofForwarder hands an ENTIRE Cashu token to the upstream TollGate and
// returns the allotment the upstream granted. Production uses
// postTokenToUpstream — the same HTTP payment wire the warm path uses, so the
// upstream needs no new endpoint. Tests inject a fake.
//
// Contract: no split. The exact token handed in is the token that reaches the
// upstream; nothing is swapped, minted or deducted locally first.
type proofForwarder func(gatewayIP, token string) (allotment uint64, err error)

// bootstrapState is the mutex-guarded state of one session's bootstrap machine.
// It is intentionally small and never holds the raw proof.
type bootstrapState struct {
	mu        sync.Mutex
	active    bool
	phase     BootstrapPhase
	allotment uint64
	reference string
	lastErr   string
	updatedAt time.Time
}

// proofReference returns a short, non-reversible handle for a forwarded proof.
func proofReference(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// resellerModeEnabled reports whether this node is configured as a reseller
// (it buys access from an upstream TollGate). A direct gateway is never a
// bootstrap candidate.
func (s *UpstreamSession) resellerModeEnabled() bool {
	if s.configManager == nil {
		return false
	}
	cfg := s.configManager.GetConfig()
	return cfg != nil && cfg.ResellerMode
}

// hasEstablishedUpstreamSession reports whether an upstream data session is
// already up for this gateway.
func (s *UpstreamSession) hasEstablishedUpstreamSession() bool {
	s.paymentMu.Lock()
	defer s.paymentMu.Unlock()
	return s.TotalAllotment > 0
}

// hasFundsForUpstream reports whether the wallet holds a non-zero balance at
// any mint the upstream accepts. With no such balance a cold start cannot be
// funded from the wallet, which is exactly when the proof has to be forwarded
// whole instead.
func (s *UpstreamSession) hasFundsForUpstream() bool {
	if s.merchantProvider == nil {
		return false
	}
	m := s.merchantProvider.GetMerchant()
	if m == nil {
		return false
	}
	if s.AdvertisementInfo != nil {
		for _, opt := range s.AdvertisementInfo.PricingOptions {
			if opt.MintURL == "" {
				continue
			}
			if m.GetBalanceByMint(opt.MintURL) > 0 {
				return true
			}
		}
		if len(s.AdvertisementInfo.PricingOptions) > 0 {
			return false
		}
	}
	// Older advertisement shapes carry no pricing options; fall back to any
	// accepted mint — if we hold a balance anywhere we are not at zero float.
	for _, mint := range m.GetAcceptedMints() {
		if m.GetBalanceByMint(mint.URL) > 0 {
			return true
		}
	}
	return false
}

// BootstrapCandidate reports whether this session should bootstrap: reseller
// mode is active, no upstream data session is up yet, and the wallet holds no
// balance at any mint the upstream accepts. A direct gateway (reseller mode
// off) is never a candidate, so this is inert on existing deployments.
func (s *UpstreamSession) BootstrapCandidate() bool {
	if !s.resellerModeEnabled() {
		return false
	}
	if s.hasEstablishedUpstreamSession() {
		return false
	}
	return !s.hasFundsForUpstream()
}

// EnsureBootstrapForColdStart arms bootstrap mode when this session is a
// cold-start candidate. It is idempotent: while bootstrap is already active
// (or a forward is in flight) it is a quiet no-op, so the usage tracker's
// repeated 0/0 polls do not spam the log. Returns true while bootstrap mode is
// active.
func (s *UpstreamSession) EnsureBootstrapForColdStart() bool {
	if !s.BootstrapCandidate() {
		return false
	}
	s.bootstrap.mu.Lock()
	defer s.bootstrap.mu.Unlock()
	if s.bootstrap.active {
		return true
	}
	s.bootstrap.active = true
	s.bootstrap.phase = BootstrapIdle
	s.bootstrap.updatedAt = time.Now()
	logger.WithField("gateway", s.GatewayIP).
		Info("🔥 Cold start: bootstrap mode armed — the first customer proof will be forwarded whole to the upstream (#239)")
	return true
}

// ForwardFirstProof forwards the ENTIRE customer proof to this session's
// upstream TollGate — no local swap, no split — and drives the bootstrap state
// machine. It returns the allotment the upstream granted once the upstream
// session is established, at which point bootstrap mode is exited and later
// payments take the normal wallet-funded path.
//
// Money-path notes (AGENTS.md, "distributed state-machine change"):
//   - Point of no return: the POST to the upstream. Once sent, the proof may be
//     spent even if the answer is lost, so a failure is recorded as
//     BootstrapError with the proof reference and is NOT retried automatically.
//     Recovery is operator-driven reconciliation, matching the rule that an
//     ambiguous network result must be reconciled rather than blindly retried.
//   - Duplicate execution: an in-flight forward is refused; a replay of a proof
//     that already completed returns the recorded allotment instead of
//     forwarding it a second time.
//   - The customer's own allotment is granted by the merchant from the upstream
//     session this call establishes; that step is out of scope here.
func (s *UpstreamSession) ForwardFirstProof(token string) (uint64, error) {
	if token == "" {
		return 0, fmt.Errorf("bootstrap forward: empty token")
	}
	ref := proofReference(token)

	s.bootstrap.mu.Lock()
	if s.bootstrap.active && (s.bootstrap.phase == BootstrapForwarding || s.bootstrap.phase == BootstrapEstablishing) {
		s.bootstrap.mu.Unlock()
		return 0, fmt.Errorf("bootstrap forward already in progress for gateway %s", s.GatewayIP)
	}
	if s.bootstrap.phase == BootstrapComplete && s.bootstrap.reference == ref {
		allotment := s.bootstrap.allotment
		s.bootstrap.mu.Unlock()
		logger.WithField("gateway", s.GatewayIP).
			Info("bootstrap: replay of an already-forwarded proof — returning the recorded allotment, not forwarding again")
		return allotment, nil
	}
	// A proof that already went on the wire (even if the answer was lost —
	// BootstrapError) is never re-sent: the upstream may have applied it, and a
	// resubmission would be refused as double-spent, stranding the customer's
	// value with no session. Recovery is operator reconciliation keyed on the
	// retained reference.
	if s.bootstrap.reference == ref && s.bootstrap.phase == BootstrapError {
		s.bootstrap.mu.Unlock()
		logger.WithField("gateway", s.GatewayIP).
			Warn("bootstrap: refusing to re-forward a proof whose previous forward failed — reconcile against the upstream instead of re-sending")
		return 0, fmt.Errorf("proof %s was already forwarded (outcome: %s) and will not be re-sent", ref, s.bootstrap.lastErr)
	}
	s.bootstrap.active = true
	s.bootstrap.phase = BootstrapForwarding
	s.bootstrap.reference = ref
	s.bootstrap.lastErr = ""
	s.bootstrap.updatedAt = time.Now()
	s.bootstrap.mu.Unlock()

	forwarder := s.bootstrapForwarder
	if forwarder == nil {
		forwarder = postTokenToUpstream
	}

	logger.WithFields(logrus.Fields{
		"gateway":   s.GatewayIP,
		"reference": ref,
	}).Info("💥 Bootstrap: forwarding the ENTIRE first proof to upstream (no swap, no split)")

	allotment, err := forwarder(s.GatewayIP, token)
	if err != nil {
		s.bootstrap.mu.Lock()
		s.bootstrap.phase = BootstrapError
		s.bootstrap.lastErr = err.Error()
		s.bootstrap.updatedAt = time.Now()
		s.bootstrap.mu.Unlock()
		logger.WithFields(logrus.Fields{
			"gateway":   s.GatewayIP,
			"reference": ref,
			"error":     err,
		}).Error("bootstrap: forwarding the first proof failed — reference retained for reconciliation; no automatic retry")
		return 0, fmt.Errorf("bootstrap forward failed: %w", err)
	}
	if allotment == 0 {
		s.bootstrap.mu.Lock()
		s.bootstrap.phase = BootstrapError
		s.bootstrap.lastErr = "upstream granted a zero allotment"
		s.bootstrap.updatedAt = time.Now()
		s.bootstrap.mu.Unlock()
		return 0, fmt.Errorf("bootstrap forward: upstream granted a zero allotment for reference %s", ref)
	}

	s.bootstrap.mu.Lock()
	s.bootstrap.phase = BootstrapEstablishing
	s.bootstrap.allotment = allotment
	s.bootstrap.updatedAt = time.Now()
	s.bootstrap.mu.Unlock()

	// The upstream leg succeeded. Record the session locally and exit
	// bootstrap. No split happened, so this transaction earns no margin: it
	// buys the float later customers are served from.
	s.paymentMu.Lock()
	s.TotalAllotment = allotment
	s.LastPaymentAt = time.Now()
	s.PaymentCount++
	s.paymentMu.Unlock()

	go triggerNdsSession(s.GatewayIP)

	s.bootstrap.mu.Lock()
	s.bootstrap.phase = BootstrapComplete
	s.bootstrap.active = false
	s.bootstrap.updatedAt = time.Now()
	s.bootstrap.mu.Unlock()

	logger.WithFields(logrus.Fields{
		"gateway":       s.GatewayIP,
		"new_allotment": allotment,
		"reference":     ref,
	}).Info("✅ Bootstrap complete: upstream session established, bootstrap mode exited")

	return allotment, nil
}

// BootstrapStatus returns a snapshot of this session's bootstrap state.
func (s *UpstreamSession) BootstrapStatus() BootstrapStatus {
	s.bootstrap.mu.Lock()
	defer s.bootstrap.mu.Unlock()
	phase := s.bootstrap.phase
	if phase == "" {
		phase = BootstrapIdle
	}
	return BootstrapStatus{
		Active:    s.bootstrap.active,
		Phase:     phase,
		GatewayIP: s.GatewayIP,
		Allotment: s.bootstrap.allotment,
		Reference: s.bootstrap.reference,
		Error:     s.bootstrap.lastErr,
		UpdatedAt: s.bootstrap.updatedAt,
	}
}
