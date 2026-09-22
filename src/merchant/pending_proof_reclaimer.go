package merchant

import (
	"log"
	"time"
)

// pendingProofReclaimer is the wallet capability the reclaimer needs. It
// is intentionally narrower than WalletPort: only backends that can
// resolve ambiguous send/melt outcomes (gonuts today) should implement
// it, discovered by type assertion — not by growing the WalletPort
// interface and its sidecar wire protocol.
type pendingProofReclaimer interface {
	PendingBalance() uint64
	ReclaimPendingProofs() (uint64, error)
}

var (
	// reclaimerBootDelay gives mints and upstream connectivity time to
	// come up after boot before the first reclaim attempt; reclaiming
	// against an unreachable mint just logs an error and waits for the
	// next tick anyway.
	reclaimerBootDelay = 20 * time.Second

	// reclaimerInterval is the steady-state sweep period. The tick is
	// cheap when there is nothing pending (one in-memory bucket read),
	// so it does not need to back off.
	reclaimerInterval = time.Minute
)

// StartPendingProofReclaimer sweeps proofs stuck in the pending/reserved
// bucket — the residue of ambiguous send/melt outcomes (interrupted
// token hand-off, lost melt responses). Unspent proofs are re-swapped
// back into spendable balance; proofs the mint reports as spent are left
// pending and surface through the periodic log line, which is the
// operator signal that a payout/melt needs manual reconciliation.
//
// Runs forever like the other merchant background loops; safe to call on
// a wallet whose backend does not implement pendingProofReclaimer (it
// just logs once and returns).
func (m *Merchant) StartPendingProofReclaimer() {
	wallet, ok := m.tollwallet.(pendingProofReclaimer)
	if !ok {
		log.Printf("Pending-proof reclaimer: wallet backend does not support reclaim; skipping (issue #500)")
		return
	}

	bootDelay, interval := reclaimerBootDelay, reclaimerInterval

	go func() {
		time.Sleep(bootDelay)
		m.reclaimPendingProofsOnce(wallet)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			m.reclaimPendingProofsOnce(wallet)
		}
	}()
}

func (m *Merchant) reclaimPendingProofsOnce(wallet pendingProofReclaimer) {
	pending := wallet.PendingBalance()
	if pending == 0 {
		return
	}

	log.Printf("Pending-proof reclaimer: %d sats pending resolution; asking mints for proof states", pending)

	reclaimed, err := wallet.ReclaimPendingProofs()
	if err != nil {
		log.Printf("Pending-proof reclaimer: reclaim attempt failed (will retry next sweep): %v", err)
	}

	if remaining := wallet.PendingBalance(); remaining > 0 {
		log.Printf("Pending-proof reclaimer: %d sats still pending after reclaiming %d sats — if this persists, the proofs are spent at the mint but unaccounted; reconcile the affected payout/melt manually", remaining, reclaimed)
	}
}
