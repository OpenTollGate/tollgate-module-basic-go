// Merchant-side Lightning quote tracking lives here; the src/lightning package
// is only used for outgoing LNURL payout helpers.
package merchant

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

var ErrQuoteNotFound = errors.New("lightning quote not found")

const (
	lightningQuoteStateCacheTTL     = 2 * time.Second
	lightningQuoteMonitorInterval   = 2 * time.Second
	lightningQuoteCleanupInterval   = 1 * time.Minute
	lightningQuoteExpiryGracePeriod = 5 * time.Minute
	lightningQuoteMaxAge            = 30 * time.Minute
	lightningQuoteSettledRetention  = 10 * time.Minute
)

type LightningInvoice struct {
	QuoteID string
	Invoice string
	MintURL string
	Amount  uint64
	Expiry  uint64
	State   string
}

type LightningQuoteStatus struct {
	QuoteID       string
	MintURL       string
	Amount        uint64
	State         string
	AccessGranted bool
	Allotment     uint64
	Metric        string
}

type lightningQuoteRecord struct {
	Bolt11         string
	MacAddress     string
	MintURL        string
	Amount         uint64
	Expiry         uint64
	Allotment      uint64
	CreatedAt      time.Time
	CompletedAt    time.Time
	SessionGranted bool
	Processing     bool
	CachedState    nut04.State
	CachedStateAt  time.Time
	HasCachedState bool
}

func (m *Merchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*LightningInvoice, error) {
	if !utils.ValidateMACAddress(macAddress) {
		return nil, fmt.Errorf("invalid MAC address: %s", macAddress)
	}
	if amount == 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	if _, err := m.calculateAllotment(amount, mintURL); err != nil {
		return nil, err
	}

	m.cleanupStaleLightningQuotes(time.Now())

	quote, err := m.tollwallet.RequestMintQuote(amount, mintURL)
	if err != nil {
		return nil, err
	}

	m.lightningQuoteMu.Lock()
	m.lightningQuotes[quote.Quote] = &lightningQuoteRecord{
		Bolt11:     quote.Request,
		MacAddress: macAddress,
		MintURL:    mintURL,
		Amount:     amount,
		Expiry:     quote.Expiry,
		CreatedAt:  time.Now(),
	}
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()

	go m.monitorLightningQuote(quote.Quote)

	return &LightningInvoice{
		QuoteID: quote.Quote,
		Invoice: quote.Request,
		MintURL: mintURL,
		Amount:  amount,
		Expiry:  quote.Expiry,
		State:   quote.State.String(),
	}, nil
}

func (m *Merchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*LightningQuoteStatus, error) {
	record, err := m.getLightningQuoteRecordForMAC(quoteID, macAddress)
	if err != nil {
		return nil, err
	}

	state, err := m.getLightningQuoteState(quoteID)
	if err != nil {
		return nil, err
	}

	switch state {
	case nut04.Paid, nut04.Issued:
		if err := m.ensureLightningAccessGranted(quoteID, state); err != nil {
			return nil, err
		}
	}

	record, err = m.getLightningQuoteRecordForMAC(quoteID, macAddress)
	if err != nil {
		return nil, err
	}

	statusState := state.String()
	if record.SessionGranted {
		statusState = nut04.Issued.String()
	}

	return &LightningQuoteStatus{
		QuoteID:       quoteID,
		MintURL:       record.MintURL,
		Amount:        record.Amount,
		State:         statusState,
		AccessGranted: record.SessionGranted,
		Allotment:     record.Allotment,
		Metric:        m.config.Metric,
	}, nil
}

func (m *Merchant) getLightningQuoteRecord(quoteID string) (*lightningQuoteRecord, error) {
	m.lightningQuoteMu.RLock()
	defer m.lightningQuoteMu.RUnlock()

	record, ok := m.lightningQuotes[quoteID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}

	copy := *record
	return &copy, nil
}

func (m *Merchant) getLightningQuoteRecordForMAC(quoteID, macAddress string) (*lightningQuoteRecord, error) {
	record, err := m.getLightningQuoteRecord(quoteID)
	if err != nil {
		return nil, err
	}
	if record.MacAddress != macAddress {
		return nil, fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}

	return record, nil
}

func (m *Merchant) getLightningQuoteState(quoteID string) (nut04.State, error) {
	m.lightningQuoteMu.RLock()
	record, ok := m.lightningQuotes[quoteID]
	if ok && record.HasCachedState && time.Since(record.CachedStateAt) < lightningQuoteStateCacheTTL {
		state := record.CachedState
		m.lightningQuoteMu.RUnlock()
		return state, nil
	}
	m.lightningQuoteMu.RUnlock()

	quote, err := m.tollwallet.GetMintQuoteState(quoteID)
	if err != nil {
		return 0, err
	}

	m.lightningQuoteMu.Lock()
	if record, ok := m.lightningQuotes[quoteID]; ok {
		record.CachedState = quote.State
		record.CachedStateAt = time.Now()
		record.HasCachedState = true
	}
	m.lightningQuoteMu.Unlock()

	return quote.State, nil
}

func (m *Merchant) startLightningQuoteJanitor() {
	ticker := time.NewTicker(lightningQuoteCleanupInterval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			m.cleanupStaleLightningQuotes(time.Now())
		}
	}()
}

func (m *Merchant) monitorLightningQuote(quoteID string) {
	ticker := time.NewTicker(lightningQuoteMonitorInterval)
	defer ticker.Stop()

	for {
		record, err := m.getLightningQuoteRecord(quoteID)
		if err != nil {
			log.Printf("monitorLightningQuote: stopping for %s — record not found: %v", quoteID, err)
			return
		}

		now := time.Now()
		if m.shouldDeleteLightningQuote(record, now) {
			m.deleteLightningQuote(quoteID)
			log.Printf("monitorLightningQuote: stopping for %s — quote expired/settled", quoteID)
			return
		}

		state, err := m.getLightningQuoteState(quoteID)
		if err != nil {
			log.Printf("monitorLightningQuote: mint state check failed for %s: %v", quoteID, err)
		} else {
			switch state {
			case nut04.Paid, nut04.Issued:
				if err := m.ensureLightningAccessGranted(quoteID, state); err == nil {
					log.Printf("monitorLightningQuote: stopping for %s — access granted", quoteID)
					return
				} else {
					log.Printf("monitorLightningQuote: ensureLightningAccessGranted failed for %s: %v", quoteID, err)
				}
			}
		}

		<-ticker.C
	}
}

func (m *Merchant) cleanupStaleLightningQuotes(now time.Time) {
	m.lightningQuoteMu.Lock()
	deleted := false
	for quoteID, record := range m.lightningQuotes {
		if m.shouldDeleteLightningQuote(record, now) {
			delete(m.lightningQuotes, quoteID)
			deleted = true
		}
	}
	m.lightningQuoteMu.Unlock()
	if deleted {
		m.persistLightningQuotes()
	}
}

func (m *Merchant) shouldDeleteLightningQuote(record *lightningQuoteRecord, now time.Time) bool {
	if record == nil || record.Processing {
		return false
	}

	if record.SessionGranted && !record.CompletedAt.IsZero() && now.Sub(record.CompletedAt) >= lightningQuoteSettledRetention {
		return true
	}

	if expiryTime, ok := lightningQuoteExpiryTime(record); ok && now.After(expiryTime.Add(lightningQuoteExpiryGracePeriod)) {
		return true
	}

	if !record.CreatedAt.IsZero() && now.Sub(record.CreatedAt) >= lightningQuoteMaxAge {
		return true
	}

	return false
}

func lightningQuoteExpiryTime(record *lightningQuoteRecord) (time.Time, bool) {
	if record == nil || record.Expiry == 0 {
		return time.Time{}, false
	}

	if record.Expiry > 1_000_000_000 {
		return time.Unix(int64(record.Expiry), 0), true
	}

	if record.CreatedAt.IsZero() {
		return time.Time{}, false
	}

	return record.CreatedAt.Add(time.Duration(record.Expiry) * time.Second), true
}

func (m *Merchant) deleteLightningQuote(quoteID string) {
	m.lightningQuoteMu.Lock()
	delete(m.lightningQuotes, quoteID)
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()
}

// persistLightningQuotes writes a snapshot of every lightning quote to disk so
// the in-flight set survives a restart. It is a no-op when no quoteStore is
// configured (e.g. unit tests that construct &Merchant{} directly). Write
// errors are logged but never propagated: losing the persistence side-effect
// must not break payment processing.
func (m *Merchant) persistLightningQuotes() {
	if m.quoteStore == nil {
		return
	}

	m.lightningQuoteMu.RLock()
	snapshot := make(map[string]*lightningQuoteRecord, len(m.lightningQuotes))
	for id, rec := range m.lightningQuotes {
		snapshot[id] = rec
	}
	m.lightningQuoteMu.RUnlock()

	if err := m.quoteStore.saveQuotes(snapshot); err != nil {
		log.Printf("ERROR: failed to persist lightning quotes: %v", err)
	}
}

// loadLightningQuotesFromDisk restores persisted quotes at startup. Expired or
// fully-settled quotes are dropped; unsettled (unpaid or recently-paid) quotes
// are reloaded into the in-memory map and their monitor goroutines relaunched
// so access is granted if the invoice was paid while the process was down.
func (m *Merchant) loadLightningQuotesFromDisk() {
	if m.quoteStore == nil {
		return
	}

	persisted, err := m.quoteStore.loadQuotes()
	if err != nil {
		log.Printf("ERROR: failed to load persisted lightning quotes: %v", err)
		return
	}
	if len(persisted) == 0 {
		return
	}

	now := time.Now()
	relaunched := 0
	for quoteID, pq := range persisted {
		rec := &lightningQuoteRecord{
			Bolt11:         pq.Bolt11,
			MacAddress:     pq.MacAddress,
			MintURL:        pq.MintURL,
			Amount:         pq.Amount,
			Expiry:         pq.Expiry,
			Allotment:      pq.Allotment,
			CreatedAt:      pq.CreatedAt,
			CompletedAt:    pq.CompletedAt,
			SessionGranted: pq.SessionGranted,
		}

		// Drop quotes that are expired, too old, or past the settled
		// retention window — the janitor would remove them anyway.
		if m.shouldDeleteLightningQuote(rec, now) {
			continue
		}

		m.lightningQuoteMu.Lock()
		m.lightningQuotes[quoteID] = rec
		m.lightningQuoteMu.Unlock()

		// Only quotes that still need monitoring are relaunched. A quote
		// whose session was already granted is kept (so status lookups
		// succeed) but does not need a polling goroutine.
		if !rec.SessionGranted {
			go m.monitorLightningQuote(quoteID)
			relaunched++
		}
	}

	if relaunched > 0 {
		log.Printf("Restored %d lightning quote(s) from disk, %d monitor(s) relaunched", len(persisted), relaunched)
	}

	// Persist again so the on-disk file reflects any quotes dropped above.
	m.persistLightningQuotes()
}

func (m *Merchant) ensureLightningAccessGranted(quoteID string, state nut04.State) error {
	m.lightningQuoteMu.Lock()
	record, ok := m.lightningQuotes[quoteID]
	if !ok {
		m.lightningQuoteMu.Unlock()
		return fmt.Errorf("%w: %s", ErrQuoteNotFound, quoteID)
	}
	if record.SessionGranted || record.Processing {
		m.lightningQuoteMu.Unlock()
		return nil
	}
	record.Processing = true
	recordCopy := *record
	m.lightningQuoteMu.Unlock()

	amountToGrant := recordCopy.Amount
	if state == nut04.Paid {
		mintedAmount, err := m.tollwallet.MintQuoteTokens(quoteID)
		if err != nil {
			m.lightningQuoteMu.Lock()
			if record, ok := m.lightningQuotes[quoteID]; ok {
				record.Processing = false
			}
			m.lightningQuoteMu.Unlock()
			return err
		}
		amountToGrant = mintedAmount
	}

	_, allotment, err := m.grantAccessForAmount(recordCopy.MacAddress, amountToGrant, recordCopy.MintURL)
	if err != nil {
		m.lightningQuoteMu.Lock()
		if record, ok := m.lightningQuotes[quoteID]; ok {
			record.Processing = false
		}
		m.lightningQuoteMu.Unlock()
		return err
	}

	m.lightningQuoteMu.Lock()
	if record, ok := m.lightningQuotes[quoteID]; ok {
		record.Processing = false
		record.SessionGranted = true
		record.Allotment = allotment
		record.CompletedAt = time.Now()
	}
	m.lightningQuoteMu.Unlock()
	m.persistLightningQuotes()

	return nil
}

func (m *Merchant) grantAccessForAmount(macAddress string, amountSats uint64, mintURL string) (*CustomerSession, uint64, error) {
	allotment, err := m.calculateAllotment(amountSats, mintURL)
	if err != nil {
		return nil, 0, err
	}

	session, err := m.grantSessionAccess(macAddress, allotment)
	if err != nil {
		return nil, 0, err
	}

	return session, allotment, nil
}

func (m *Merchant) grantSessionAccess(macAddress string, allotment uint64) (*CustomerSession, error) {
	previousSession, hadSession := m.snapshotSession(macAddress)

	session, err := m.AddAllotment(macAddress, m.config.Metric, allotment)
	if err != nil {
		return nil, err
	}

	if err := openGateForSession(macAddress, session); err != nil {
		m.recordPendingAuth(macAddress, allotment)
		m.restoreSession(macAddress, previousSession, hadSession)
		return nil, err
	}

	m.clearPendingAuth(macAddress)
	return session, nil
}

func openGateForSession(macAddress string, session *CustomerSession) error {
	switch session.Metric {
	case "milliseconds":
		endTimestamp := session.StartTime + int64(session.Allotment/1000)
		if err := valve.OpenGateUntil(macAddress, endTimestamp); err != nil {
			return fmt.Errorf("failed to open gate: %w", err)
		}
	case "bytes":
		// Always call OpenGate — ndsctl auth is idempotent.
		// Previous code skipped this when a data baseline existed, but after
		// a deauth the gate is closed while the baseline persists, causing
		// Cashu payments to return success without actually opening the gate.
		if err := valve.OpenGate(macAddress); err != nil {
			return fmt.Errorf("failed to open gate: %w", err)
		}
	default:
		return fmt.Errorf("unsupported metric: %s", session.Metric)
	}

	return nil
}
