package wireless_gateway_manager

import (
	"errors"
)

var errSupersededOperation = errors.New("upstream operation superseded")
var errOperationBusy = errors.New("upstream operation busy")

// ManualWriter is the exclusive radio-mutation lease held by one manual
// operation (a CLI connect, disconnect or remove). Ownership is explicit
// and handler-scoped: the handler acquires the lease once at entry and
// ends it on return. Leases are never nested, and nothing infers
// ownership from the calling goroutine — an ordinary sync.Mutex is the
// whole contract.
//
// While a lease is held:
//
//   - every automatic manager path (switch, candidate discovery, radio
//     re-enable, stale-STA cleanup) declines nonblocking instead of
//     queueing, so the manager loop keeps ticking and never writes the
//     radio concurrently with the manual writer;
//   - a second manual writer blocks in BeginManualWriter until the current
//     writer ends: two manual handlers serialize;
//   - the manager's connectivity checks are paused through a nested pause
//     scope for the lease's lifetime, and in-flight observation
//     generations are invalidated, so stale verdicts cannot publish.
//
// The lease does not fence external root writers; it coordinates the
// manager's own goroutines and the CLI handlers against each other.
type ManualWriter struct {
	um *UpstreamManager
}

// BeginManualWriter acquires the exclusive radio writer lease. It blocks
// while another manual writer owns the radio. The returned lease's End is
// idempotent, so callers can defer it unconditionally.
func (um *UpstreamManager) BeginManualWriter() *ManualWriter {
	um.writerMu.Lock()
	// A manual action invalidates every observation gathered before it.
	um.connectionGeneration.Add(1)
	um.pauseMu.Lock()
	um.manualOperations++
	um.pauseMu.Unlock()
	return &ManualWriter{um: um}
}

// End releases the writer lease together with its pause scope. It is
// idempotent and nil-safe.
func (w *ManualWriter) End() {
	if w == nil || w.um == nil {
		return
	}
	um := w.um
	w.um = nil
	um.pauseMu.Lock()
	if um.manualOperations > 0 {
		um.manualOperations--
	}
	um.pauseMu.Unlock()
	um.writerMu.Unlock()
}

// The existing manager is the only in-process network owner. Automatic
// work never waits for the writer lease: a busy lease means a manual flow
// owns the radio right now, and the automatic path declines (retrying
// next cycle) instead of stalling the loop behind a manual join parked in
// its DHCP wait.
func (um *UpstreamManager) switchAutomatically(generation uint64, active string, candidate *Candidate) (uint64, error) {
	if !um.writerMu.TryLock() {
		return 0, errOperationBusy
	}
	defer um.writerMu.Unlock()
	if um.isPaused() || um.connectionGeneration.Load() != generation {
		return 0, errSupersededOperation
	}
	current := um.connectionGeneration.Add(1)
	return current, um.connector.SwitchUpstream(active, candidate.IfaceName, candidate.SSID, candidate.BSSID)
}

// tryOperation runs action only when the writer lease is immediately
// available. A busy lease means another flow owns the radio (typically a
// manual join parked in its DHCP wait), so tick-path housekeeping declines
// and retries later instead of stalling the loop behind it.
func (um *UpstreamManager) tryOperation(action func()) bool {
	if !um.writerMu.TryLock() {
		return false
	}
	defer um.writerMu.Unlock()
	// A manual join may have ended since this tick observed its state,
	// while its post-connect pause window still protects radio churn.
	if um.isPaused() {
		return false
	}
	action()
	return true
}

// maintainAutomatically runs startup housekeeping under the writer lease,
// declining nonblocking when a manual writer already owns the radio (the
// per-tick paths retry that housekeeping later) instead of queueing
// Start behind a parked manual join.
func (um *UpstreamManager) maintainAutomatically(action func()) {
	if !um.writerMu.TryLock() {
		return
	}
	defer um.writerMu.Unlock()
	if !um.isPaused() {
		action()
	}
}

// applyAutomaticResult fences synchronous switch bookkeeping without queueing
// the manager loop behind a manual writer that acquired the lease meanwhile.
func (um *UpstreamManager) applyAutomaticResult(generation uint64, action func()) bool {
	if !um.writerMu.TryLock() {
		return false
	}
	defer um.writerMu.Unlock()
	if um.isPaused() || um.connectionGeneration.Load() != generation {
		return false
	}
	action()
	return true
}

// applyVerification publishes a post-switch verdict under the writer lease
// so it cannot interleave with another writer's generation bump. It blocks
// until the lease is free: verification runs on background goroutines, and
// the generation check below makes a verdict gathered before a manual join
// publish nothing once the lease is handed over. Radio mutations never
// wait here — only this state publication does.
func (um *UpstreamManager) applyVerification(generation uint64, action func()) bool {
	um.writerMu.Lock()
	defer um.writerMu.Unlock()
	if um.connectionGeneration.Load() != generation {
		return false
	}
	action()
	return true
}

// Candidate construction in reseller mode may create a STA section, so even
// discovery must use the same owner after a scan. No extra route writer.
// Like the switch path, discovery never waits for the lease: a busy writer
// means a manual flow owns the radio, and the scan cycle declines.
func (um *UpstreamManager) findCandidatesForGeneration(generation uint64, networks []NetworkInfo, reseller, emergency bool) (*Candidate, error) {
	if !um.writerMu.TryLock() {
		return nil, errOperationBusy
	}
	defer um.writerMu.Unlock()
	if um.isPaused() || um.connectionGeneration.Load() != generation {
		return nil, errSupersededOperation
	}
	return um.findCandidates(networks, reseller, emergency)
}
