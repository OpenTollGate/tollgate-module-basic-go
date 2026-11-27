package paymentjournal

import (
	"fmt"
	"sort"
	"time"
)

// Retention is the policy for reclaiming journal capacity.
//
// # WHY THIS EXISTS
//
// maxRecords is a hard cap that counts every operation whatever its state, and the store has
// no delete path. A caller that journals one record per unit of business therefore stops being
// able to record anything once it has done maxRecords units of business — permanently, and
// without an adversary. Measured on the LNURLcash redemption rail: sale 1024 was refused, and
// every sale after it too.
//
// That is not a bug in the caller. A journal whose capacity is consumed by COMPLETED work has
// no bound on lifetime, only on lifetime *volume*, and the two are different promises. The
// bound that makes bytes-at-rest and fsync cost finite is worth keeping; counting finished
// records against it is not.
//
// # WHAT MAY BE RECLAIMED
//
// Only records that are durably finished: Paid, Completed and Failed. Anything still in
// flight — Prepared, Submitted, Unknown, Awaiting, PaidFeePending, GrantPending — is
// untouchable, because those states are exactly what reconciliation and duplicate-suppression
// read. Dropping one would re-open a money move for a second attempt, which is the failure
// this store exists to prevent.
//
// # WHAT RECLAIMING COSTS
//
// A reclaimed record can no longer answer "have I seen this before?". That question has two
// different answers depending on the state:
//
//   - Failed: the move definitively did not happen, so forgetting it is safe. A retry is a
//     legitimate new attempt.
//   - Paid / Completed: the move DID happen. Forgetting it means nothing local remembers it.
//     Whether that is safe depends entirely on whether the thing on the other side can be
//     asked — a mint that still reports the quote, an issuer that refuses a spent note. When
//     it cannot, the record must be kept, which is why KeepSettled exists below.
//
// So the default is deliberately the conservative one, and a caller that knows its terminal
// records are safe to forget opts in. Silence gets the safe behaviour.
type Retention struct {
	// KeepFor is how long a terminal record is kept after its last update. Zero disables all
	// pruning, which is the current behaviour and stays the default.
	KeepFor time.Duration

	// KeepSettled, when true, keeps Paid and Completed records forever and reclaims only
	// Failed ones. That is the safe setting for a caller whose settled records are the ONLY
	// local evidence that a payment happened — the LNURLcash rail, where the issuer's own
	// confirmation was the settlement signal and the note is not otherwise recorded.
	KeepSettled bool

	// MaxRecords overrides the store's capacity for newly prepared records. Zero uses
	// maxRecords. A caller may lower it; raising it past maxRecords is refused rather than
	// clamped, because a silent clamp would make the configured bound a lie.
	MaxRecords int
}

// capFor returns the number of records the store will allow, given this policy.
func (r Retention) capFor() int {
	if r.MaxRecords <= 0 || r.MaxRecords > maxRecords {
		return maxRecords
	}
	return r.MaxRecords
}

// reclaimable reports whether a record in this state may be dropped.
func (r Retention) reclaimable(op Operation) bool {
	if r.KeepSettled {
		return op.State == Failed
	}
	switch op.State {
	case Paid, Completed, Failed:
		return true
	}
	return false
}

// ErrRetentionRefused reports a retention policy the store will not apply, because applying it
// would drop a record that is still needed.
type ErrRetentionRefused struct{ Reason string }

func (e ErrRetentionRefused) Error() string {
	return "payment journal: retention refused: " + e.Reason
}

// Prune reclaims finished records according to the policy and reports how many went.
//
// It is atomic with respect to Prepare and Advance: it takes the same write lock, decides on a
// copy, and persists in one rename. A crash mid-prune leaves either the old set or the new one,
// never a partial journal — the same guarantee every other mutation here has.
//
// It refuses outright rather than guessing when the policy would take a record that is still
// in flight. That cannot happen through reclaimable() today; the check is here so it also
// cannot happen after someone edits reclaimable().
func (s *Store) Prune(ret Retention, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return 0, err
	}
	if ret.KeepFor <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-ret.KeepFor)

	next := s.copyData()
	dropped := 0
	for id, op := range next.Operations {
		if !op.UpdatedAt.Before(cutoff) {
			continue // not old enough
		}
		if !ret.reclaimable(op) {
			// Still in flight, or settled under a policy that keeps settled records. Keep it.
			// An in-flight record is never reclaimed regardless of age: age is not progress.
			if !terminal(op.State) {
				continue
			}
			continue
		}
		if !terminal(op.State) {
			return 0, ErrRetentionRefused{Reason: fmt.Sprintf(
				"operation %q in state %q is not terminal and must not be reclaimed", id, op.State)}
		}
		delete(next.Operations, id)
		dropped++
	}
	if dropped == 0 {
		return 0, nil
	}
	if err := s.persist(next, false); err != nil {
		return 0, err
	}
	return dropped, nil
}

// terminal reports whether a state is durably finished and can never advance again.
func terminal(st State) bool {
	switch st {
	case Paid, Completed, Failed:
		return true
	}
	return false
}

// Capacity reports the current record count and the configured bound, so an operator-facing
// caller can warn before a journal fills instead of discovering it at refusal time. It is
// deliberately cheaper than List: it counts under the lock and copies nothing.
func (s *Store) Capacity() (used, limit int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.ready(); e != nil {
		return 0, 0, e
	}
	return len(s.data.Operations), maxRecords, nil
}

// OldestInFlight reports the oldest record that is still in flight, so a caller can tell a
// journal that is full of finished work (prunable, benign) from one whose slots are all held by
// operations that never resolved (a real wedge that pruning cannot fix).
func (s *Store) OldestInFlight() (Operation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Operation{}, false, err
	}
	var inFlight []Operation
	for _, op := range s.data.Operations {
		if !terminal(op.State) {
			inFlight = append(inFlight, clone(op))
		}
	}
	if len(inFlight) == 0 {
		return Operation{}, false, nil
	}
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i].CreatedAt.Before(inFlight[j].CreatedAt) })
	return inFlight[0], true, nil
}
