package merchant

import (
	"fmt"
	"log"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/nbd-wtf/go-nostr"
)

const (
	pendingAuthMaxAttempts = 5
	pendingAuthWindow      = 10 * time.Minute
)

type pendingAuthRecord struct {
	MacAddress string
	Allotment  uint64
	CreatedAt  time.Time
	Attempts   []time.Time
}

func (m *Merchant) recordPendingAuth(mac string, allotment uint64) {
	m.pendingAuthMu.Lock()
	defer m.pendingAuthMu.Unlock()
	if _, exists := m.pendingAuths[mac]; exists {
		return
	}
	m.pendingAuths[mac] = &pendingAuthRecord{
		MacAddress: mac,
		Allotment:  allotment,
		CreatedAt:  time.Now(),
	}
}

func (m *Merchant) clearPendingAuth(mac string) {
	m.pendingAuthMu.Lock()
	delete(m.pendingAuths, mac)
	m.pendingAuthMu.Unlock()
}

func (m *Merchant) sweepPendingAuths() {
	m.pendingAuthMu.Lock()
	defer m.pendingAuthMu.Unlock()
	for mac, rec := range m.pendingAuths {
		if time.Since(rec.CreatedAt) > pendingAuthWindow*2 {
			delete(m.pendingAuths, mac)
		}
	}
}

func (m *Merchant) RetryAuth(macAddress string) (*nostr.Event, error) {
	if !utils.ValidateMACAddress(macAddress) {
		return m.CreateNoticeEvent("error", "invalid-mac-address",
			fmt.Sprintf("Invalid MAC address: %s", macAddress), macAddress)
	}

	m.pendingAuthMu.Lock()
	rec, exists := m.pendingAuths[macAddress]
	if !exists {
		m.pendingAuthMu.Unlock()
		return m.CreateNoticeEvent("error", "no-pending-session",
			"No paid session awaiting authentication", macAddress)
	}
	if time.Since(rec.CreatedAt) > pendingAuthWindow {
		delete(m.pendingAuths, macAddress)
		m.pendingAuthMu.Unlock()
		return m.CreateNoticeEvent("error", "session-window-expired",
			"Retry window expired; a new payment is required", macAddress)
	}
	now := time.Now()
	pruned := rec.Attempts[:0]
	for _, t := range rec.Attempts {
		if now.Sub(t) <= pendingAuthWindow {
			pruned = append(pruned, t)
		}
	}
	if len(pruned) >= pendingAuthMaxAttempts {
		m.pendingAuthMu.Unlock()
		return m.CreateNoticeEvent("error", "retry-limit-reached",
			fmt.Sprintf("Retry limit (%d) reached for this session", pendingAuthMaxAttempts),
			macAddress)
	}
	pruned = append(pruned, now)
	rec.Attempts = pruned
	attemptCount := len(rec.Attempts)
	allotment := rec.Allotment
	delete(m.pendingAuths, macAddress)
	m.pendingAuthMu.Unlock()

	session, err := m.grantSessionAccess(macAddress, allotment)
	if err != nil {
		m.pendingAuthMu.Lock()
		if existing, ok := m.pendingAuths[macAddress]; ok {
			existing.Attempts = append(existing.Attempts, now)
		} else {
			m.pendingAuths[macAddress] = &pendingAuthRecord{
				MacAddress: macAddress, Allotment: allotment,
				CreatedAt: time.Now(), Attempts: []time.Time{now},
			}
		}
		m.pendingAuthMu.Unlock()

		log.Printf("retry-auth: gate-open failed (attempt %d/%d) for %s: %v",
			attemptCount, pendingAuthMaxAttempts, macAddress, err)
		return m.CreateNoticeEvent("error", "gate-open-failed",
			fmt.Sprintf("Gate still unavailable after retry: %v", err), macAddress)
	}

	return m.createSessionEvent(session, macAddress)
}
