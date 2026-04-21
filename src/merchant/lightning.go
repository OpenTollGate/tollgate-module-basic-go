package merchant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

const lightningQuoteRetention = 24 * time.Hour

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
	MacAddress     string
	MintURL        string
	Amount         uint64
	Allotment      uint64
	SessionGranted bool
	Processing     bool
	UpdatedAt      int64
}

func (m *Merchant) lightningQuoteStorePath() string {
	if m.configManager == nil || m.configManager.ConfigFilePath == "" {
		return ""
	}

	return filepath.Join(filepath.Dir(m.configManager.ConfigFilePath), "lightning_quotes.json")
}

func (m *Merchant) saveLightningQuotes() error {
	m.lightningQuoteMu.Lock()
	defer m.lightningQuoteMu.Unlock()

	return m.saveLightningQuotesLocked()
}

func (m *Merchant) saveLightningQuotesLocked() error {
	path := m.lightningQuoteStorePath()
	if path == "" {
		return nil
	}

	m.pruneLightningQuotesLocked(time.Now())

	data, err := json.MarshalIndent(m.lightningQuotes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lightning quotes: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create lightning quote dir: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write lightning quotes temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace lightning quotes file: %w", err)
	}

	return nil
}

func (m *Merchant) loadLightningQuotes() error {
	path := m.lightningQuoteStorePath()
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read lightning quotes: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	quotes := make(map[string]*lightningQuoteRecord)
	if err := json.Unmarshal(data, &quotes); err != nil {
		return fmt.Errorf("unmarshal lightning quotes: %w", err)
	}

	m.lightningQuoteMu.Lock()
	m.lightningQuotes = quotes
	m.pruneLightningQuotesLocked(time.Now())
	if err := m.saveLightningQuotesLocked(); err != nil {
		m.lightningQuoteMu.Unlock()
		return err
	}
	m.lightningQuoteMu.Unlock()

	return nil
}

func (m *Merchant) pruneLightningQuotesLocked(now time.Time) {
	cutoff := now.Add(-lightningQuoteRetention).Unix()
	for quoteID, record := range m.lightningQuotes {
		if record == nil {
			delete(m.lightningQuotes, quoteID)
			continue
		}

		updatedAt := record.UpdatedAt
		if updatedAt == 0 {
			updatedAt = now.Unix()
			record.UpdatedAt = updatedAt
		}

		if updatedAt < cutoff {
			delete(m.lightningQuotes, quoteID)
		}
	}
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
		UpdatedAt:  time.Now().Unix(),
	}
	err = m.saveLightningQuotesLocked()
	m.lightningQuoteMu.Unlock()
	if err != nil {
		return nil, err
	}

	return &LightningInvoice{
		QuoteID: quote.Quote,
		Invoice: quote.Request,
		MintURL: mintURL,
		Amount:  amount,
		Expiry:  quote.Expiry,
		State:   quote.State.String(),
	}, nil
}

func (m *Merchant) GetLightningInvoiceStatus(quoteID string) (*LightningQuoteStatus, error) {
	record, err := m.getLightningQuoteRecord(quoteID)
	if err != nil {
		return nil, err
	}

	quote, err := m.tollwallet.GetMintQuoteState(quoteID)
	if err != nil {
		return nil, err
	}

	switch quote.State {
	case nut04.Paid:
		if err := m.ensureLightningAccessGranted(quoteID, quote.State); err != nil {
			return nil, err
		}
	case nut04.Issued:
		if err := m.ensureLightningAccessGranted(quoteID, quote.State); err != nil {
			return nil, err
		}
	}

	record, err = m.getLightningQuoteRecord(quoteID)
	if err != nil {
		return nil, err
	}

	state := quote.State.String()
	if record.SessionGranted {
		state = nut04.Issued.String()
	}

	return &LightningQuoteStatus{
		QuoteID:       quoteID,
		MintURL:       record.MintURL,
		Amount:        record.Amount,
		State:         state,
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
		return nil, fmt.Errorf("lightning quote not found: %s", quoteID)
	}

	copy := *record
	return &copy, nil
}

func (m *Merchant) ensureLightningAccessGranted(quoteID string, state nut04.State) error {
	m.lightningQuoteMu.Lock()
	record, ok := m.lightningQuotes[quoteID]
	if !ok {
		m.lightningQuoteMu.Unlock()
		return fmt.Errorf("lightning quote not found: %s", quoteID)
	}
	if record.SessionGranted || record.Processing {
		m.lightningQuoteMu.Unlock()
		return nil
	}
	record.Processing = true
	record.UpdatedAt = time.Now().Unix()
	if err := m.saveLightningQuotesLocked(); err != nil {
		record.Processing = false
		m.lightningQuoteMu.Unlock()
		return err
	}
	recordCopy := *record
	m.lightningQuoteMu.Unlock()

	amountToGrant := recordCopy.Amount
	if state == nut04.Paid {
		mintedAmount, err := m.tollwallet.MintQuoteTokens(quoteID)
		if err != nil {
			m.lightningQuoteMu.Lock()
			if record, ok := m.lightningQuotes[quoteID]; ok {
				record.Processing = false
				record.UpdatedAt = time.Now().Unix()
				_ = m.saveLightningQuotesLocked()
			}
			m.lightningQuoteMu.Unlock()
			return err
		}
		amountToGrant = mintedAmount
	}

	session, allotment, err := m.grantAccessForAmount(recordCopy.MacAddress, amountToGrant, recordCopy.MintURL)
	if err != nil {
		m.lightningQuoteMu.Lock()
		if record, ok := m.lightningQuotes[quoteID]; ok {
			record.Processing = false
			record.UpdatedAt = time.Now().Unix()
			_ = m.saveLightningQuotesLocked()
		}
		m.lightningQuoteMu.Unlock()
		return err
	}

	_ = session

	m.lightningQuoteMu.Lock()
	if record, ok := m.lightningQuotes[quoteID]; ok {
		record.Processing = false
		record.SessionGranted = true
		record.Allotment = allotment
		record.UpdatedAt = time.Now().Unix()
		if err := m.saveLightningQuotesLocked(); err != nil {
			m.lightningQuoteMu.Unlock()
			return err
		}
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
