// Merchant-side Lightning quote tracking lives here; the src/lightning package
// is only used for outgoing LNURL payout helpers.
package merchant

import (
	"errors"
	"fmt"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

var ErrQuoteNotFound = errors.New("lightning quote not found")

const lightningQuoteStateCacheTTL = 2 * time.Second

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
	MacAddress      string
	MintURL         string
	Amount          uint64
	Allotment       uint64
	SessionGranted  bool
	Processing      bool
	CachedState     nut04.State
	CachedStateAt   time.Time
	HasCachedState  bool
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

	quote, err := m.tollwallet.RequestMintQuote(amount, mintURL)
	if err != nil {
		return nil, err
	}

	m.lightningQuoteMu.Lock()
	m.lightningQuotes[quote.Quote] = &lightningQuoteRecord{
		MacAddress: macAddress,
		MintURL:    mintURL,
		Amount:     amount,
	}
	m.lightningQuoteMu.Unlock()

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
	}
	m.lightningQuoteMu.Unlock()

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
	session, err := m.AddAllotment(macAddress, m.config.Metric, allotment)
	if err != nil {
		return nil, err
	}

	switch session.Metric {
	case "milliseconds":
		endTimestamp := session.StartTime + int64(session.Allotment/1000)
		if err := valve.OpenGateUntil(macAddress, endTimestamp); err != nil {
			return nil, fmt.Errorf("failed to open gate: %w", err)
		}
	case "bytes":
		if !valve.HasDataBaseline(macAddress) {
			if err := valve.OpenGate(macAddress); err != nil {
				return nil, fmt.Errorf("failed to open gate: %w", err)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported metric: %s", session.Metric)
	}

	return session, nil
}
