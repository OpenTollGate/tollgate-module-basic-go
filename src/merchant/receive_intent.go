// The receive-intent journal — the merchant half of #502's
// business-transaction record, shaped on the Cashu Fault Lab lifecycle
// model (persist-before-effect; ambiguity is a state; definitive failure
// requires stable evidence; never retry blindly).
//
// One durable record per money-moving Receive attempt, keyed by the
// customer-quotable receive reference, written BEFORE the Receive call:
//
//	begin:    journal(reference, mac, mint, amount, token)  ← before money
//	resolve:  granted   — a session was granted (the success path)
//	          owed      — value arrived, gate could not open (the owed
//	                      -grant store takes over; see owed_grant.go)
//	          abandoned — stable evidence the mint never took the note
//	                      (its refusal, or NUT-07 all-unspent after the
//	                      call had ended) — the customer keeps the note
//	pending:  everything else. Pending is the honest state for a crash,
//	          a dropped response, an unanswered checkstate.
//
// Reconciliation turns pending into a decision at the three points where
// the answer can be known:
//
//	late recorder  — the Receive answered after our deadline (its own
//	                 result is the evidence: success ⇒ owed, definitive
//	                 refusal ⇒ abandoned, ambiguous ⇒ NUT-07 ask);
//	restart        — every pending intent on disk is checkstated once
//	                 against its mint (the call is over: the process died);
//	resubmission   — a customer retry arriving for a pending intent (the
//	                 #639 in-flight guard has already excluded a live
//	                 call) reconciles synchronously before any answer.
//
// The journal stores the serialized token because checkstate needs the
// proof secrets after a restart. Tokens are bearer instruments: the store
// is 0600 root-only like the drain journal, and the secrets are never
// logged.
package merchant

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	intentStatePending   = "pending"
	intentStateGranted   = "granted"
	intentStateOwed      = "owed"
	intentStateAbandoned = "abandoned"
)

// persistedReceiveIntent is the serializable form. TokenSerialized is
// bearer material: the store file is 0600 and nothing here logs it.
type persistedReceiveIntent struct {
	Reference       string    `json:"reference"`
	MacAddress      string    `json:"mac_address"`
	MintURL         string    `json:"mint_url"`
	AmountSats      uint64    `json:"amount_sats"`
	TokenSerialized string    `json:"token_serialized"`
	Metric          string    `json:"metric"`
	CreatedAt       time.Time `json:"created_at"`
	ResolvedAt      time.Time `json:"resolved_at"`
	State           string    `json:"state"`
	ResolutionNote  string    `json:"resolution_note"`
}

type receiveIntentRecord struct {
	persistedReceiveIntent
}

// receiveIntentStore persists intents with the same atomic-write
// discipline as the quote and owed-grant stores.
type receiveIntentStore struct {
	filePath string
	mu       sync.Mutex
}

func newReceiveIntentStore(filePath string) *receiveIntentStore {
	return &receiveIntentStore{filePath: filePath}
}

func (s *receiveIntentStore) saveIntents(intents map[string]*receiveIntentRecord) error {
	persisted := make(map[string]*persistedReceiveIntent, len(intents))
	for ref, rec := range intents {
		if rec == nil {
			continue
		}
		persisted[ref] = &rec.persistedReceiveIntent
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receive intents: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir := filepath.Dir(s.filePath); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create receive-intent dir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.filePath), ".receive-intents-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp receive-intents file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp receive-intents file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp receive-intents file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp receive-intents file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp receive-intents file: %w", err)
	}
	if err := os.Rename(tmpName, s.filePath); err != nil {
		return fmt.Errorf("rename temp receive-intents file: %w", err)
	}
	cleanup = false
	return nil
}

func (s *receiveIntentStore) loadIntents() (map[string]*persistedReceiveIntent, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*persistedReceiveIntent), nil
		}
		return nil, fmt.Errorf("read receive-intents file: %w", err)
	}
	intents := make(map[string]*persistedReceiveIntent)
	if err := json.Unmarshal(data, &intents); err != nil {
		return nil, fmt.Errorf("unmarshal receive intents: %w", err)
	}
	return intents, nil
}

// beginReceiveIntent durably records one money-moving attempt BEFORE the
// Receive call. It returns (existing, resolved) when the reference is
// already known: resolved=true means a prior attempt reached a terminal
// state (granted/owed/abandoned — the caller answers the resubmission
// without moving money); resolved=false means pending — the caller
// reconciles it (the prior call cannot still be on the wire: the #639
// in-flight guard ran first).
func (m *Merchant) beginReceiveIntent(reference, macAddress, mintURL string, amountSats uint64, tokenSerialized, metric string) (existing *receiveIntentRecord, resolved bool) {
	if reference == "" {
		return nil, false
	}
	rec := &receiveIntentRecord{persistedReceiveIntent{
		Reference:       reference,
		MacAddress:      NormalizeMACAddress(macAddress),
		MintURL:         mintURL,
		AmountSats:      amountSats,
		TokenSerialized: tokenSerialized,
		Metric:          metric,
		CreatedAt:       time.Now(),
		State:           intentStatePending,
	}}

	m.receiveIntentMu.Lock()
	if m.receiveIntents == nil {
		m.receiveIntents = make(map[string]*receiveIntentRecord)
	}
	if prior, ok := m.receiveIntents[reference]; ok {
		if prior.State == intentStateAbandoned {
			// An abandoned attempt took no value: the same note may be
			// resubmitted, and this reference now describes the NEW attempt.
			prior.State = intentStatePending
			prior.CreatedAt = time.Now()
			prior.ResolvedAt = time.Time{}
			prior.ResolutionNote = ""
			m.receiveIntentMu.Unlock()
			m.persistReceiveIntents()
			return nil, false
		}
		m.receiveIntentMu.Unlock()
		return prior, prior.State != intentStatePending
	}
	m.receiveIntents[reference] = rec
	m.receiveIntentMu.Unlock()

	if m.receiveIntentStore == nil {
		log.Printf("WARNING: receive intent for reference %s has no durable store — it survives only until restart", reference)
	} else {
		m.persistReceiveIntents()
	}
	return nil, false
}

// resolveReceiveIntent moves an intent to a terminal state, durably.
func (m *Merchant) resolveReceiveIntent(reference, state, note string) {
	if reference == "" {
		return
	}
	m.receiveIntentMu.Lock()
	rec, ok := m.receiveIntents[reference]
	if !ok || rec.State != intentStatePending {
		m.receiveIntentMu.Unlock()
		return
	}
	rec.State = state
	rec.ResolvedAt = time.Now()
	rec.ResolutionNote = note
	m.receiveIntentMu.Unlock()
	m.persistReceiveIntents()
}

// persistReceiveIntents snapshots the journal. No-op without a store;
// errors logged (the in-memory record still governs this process).
func (m *Merchant) persistReceiveIntents() {
	if m.receiveIntentStore == nil {
		return
	}
	m.receiveIntentMu.Lock()
	snapshot := make(map[string]*receiveIntentRecord, len(m.receiveIntents))
	for ref, rec := range m.receiveIntents {
		cp := *rec
		snapshot[ref] = &cp
	}
	m.receiveIntentMu.Unlock()
	if err := m.receiveIntentStore.saveIntents(snapshot); err != nil {
		log.Printf("ERROR: failed to persist receive intents: %v", err)
	}
}

// reconcileReceiveIntent turns one pending intent into a decision using
// NUT-07 as the evidence. It runs only at points where the original call
// is provably over (restart, late recorder completion, resubmission with
// the in-flight guard clear) — the v0.13.0 client is single-shot, so an
// unspent answer at those points is final.
func (m *Merchant) reconcileReceiveIntent(reference string) {
	// Copy the fields needed for the evidence work under the lock: the
	// record is shared with the resolver goroutines, and the checkstate
	// call happens outside any lock.
	m.receiveIntentMu.Lock()
	rec, ok := m.receiveIntents[reference]
	if !ok || rec.State != intentStatePending || rec.TokenSerialized == "" {
		m.receiveIntentMu.Unlock()
		return
	}
	tokenSerialized := rec.TokenSerialized
	mintURL := rec.MintURL
	amountSats := rec.AmountSats
	macAddress := rec.MacAddress
	m.receiveIntentMu.Unlock()

	// Sequencing before evidence (#793 review, crash-lane Appendix B): the
	// wallet's boot pending-swap replay runs concurrently with this
	// reconcile, and for an intent whose proofs that replay is about to
	// re-POST, a checkstate issued NOW can answer unspent — abandoning on
	// it would hand the customer a dead note while the operator's replay
	// recovers the value moments later. An unspent answer is only stable
	// evidence once no replay can spend the proofs under inspection, so the
	// reconcile first observes the replay pass when the wallet exposes it
	// (gonuts does; cdk resumes at open and the sidecar owns its intents,
	// so neither needs the gate). Boot itself never waits here — only this
	// already-async reconcile does, and the pass is bounded by
	// construction.
	if awaiter, ok := m.tollwallet.(interface {
		BootSwapReplayDone() <-chan struct{}
	}); ok {
		<-awaiter.BootSwapReplayDone()
	}

	spent, err := m.checkTokenSpent(tokenSerialized)
	switch {
	case err != nil:
		// Ambiguous evidence resolves nothing — the intent stays pending
		// for the next trigger (restart, resubmission, operator tooling).
		log.Printf("ReceiveIntent: reconciliation for reference %s could not decide (checkstate: %v) — staying pending", reference, err)
	case spent:
		// The mint took the note: the value is recoverable by the operator
		// (NUT-09 restore) and the customer is owed service. The owed-grant
		// machinery owns convergence from here.
		allotment, allotErr := m.calculateAllotment(amountSats, mintURL)
		if allotErr != nil {
			log.Printf("ReceiveIntent: reference %s proved SPENT but pricing against mint %s failed (%v) — staying pending for the next trigger", reference, mintURL, allotErr)
			return
		}
		m.resolveReceiveIntent(reference, intentStateOwed, "NUT-07: proofs spent; owed entitlement recorded")
		m.recordOwedGrant(reference, macAddress, mintURL, amountSats, allotment)
	default:
		// Stable evidence the swap never happened: the single-shot client's
		// request ended and the mint never spent the proofs. The customer
		// keeps a spendable note and may resubmit it fresh.
		m.resolveReceiveIntent(reference, intentStateAbandoned, "NUT-07: all proofs unspent after the call had ended")
	}
}

// checkTokenSpent decodes the journaled token and asks the mint. Kept as
// a seam so tests can drive the evidence without a mint.
func (m *Merchant) checkTokenSpent(tokenSerialized string) (bool, error) {
	token, err := m.tollwallet.DecodeToken(tokenSerialized)
	if err != nil {
		return false, fmt.Errorf("decode journaled token: %w", err)
	}
	defer token.Close()
	return m.tollwallet.CheckTokenSpent(token)
}

// loadReceiveIntentsFromDisk restores the journal at startup and
// reconciles every pending intent whose answer is now knowable (the
// process died with the call unfinished — no request of ours is still on
// the wire).
func (m *Merchant) loadReceiveIntentsFromDisk() {
	if m.receiveIntentStore == nil {
		return
	}
	persisted, err := m.receiveIntentStore.loadIntents()
	if err != nil {
		log.Printf("ERROR: failed to load persisted receive intents: %v", err)
		return
	}
	if len(persisted) == 0 {
		return
	}

	pending := 0
	for ref, pi := range persisted {
		rec := &receiveIntentRecord{persistedReceiveIntent: *pi}
		m.receiveIntentMu.Lock()
		if m.receiveIntents == nil {
			m.receiveIntents = make(map[string]*receiveIntentRecord)
		}
		if _, exists := m.receiveIntents[ref]; !exists {
			m.receiveIntents[ref] = rec
		}
		m.receiveIntentMu.Unlock()
		if rec.State == intentStatePending {
			pending++
		}
	}

	if pending > 0 {
		log.Printf("Restored %d pending receive intent(s) — reconciling against their mints (NUT-07)", pending)
		go func() {
			for ref, rec := range persisted {
				if rec.State != intentStatePending {
					continue
				}
				m.reconcileReceiveIntent(ref)
			}
		}()
	}
}
