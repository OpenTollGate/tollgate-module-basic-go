package merchant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// persistedQuote is the serializable subset of lightningQuoteRecord written to
// disk so quote state survives process restarts. Transient fields
// (Processing, CachedState, etc.) are intentionally excluded.
type persistedQuote struct {
	QuoteID        string    `json:"quote_id"`
	Bolt11         string    `json:"bolt11"`
	MacAddress     string    `json:"mac_address"`
	MintURL        string    `json:"mint_url"`
	Amount         uint64    `json:"amount"`
	Expiry         uint64    `json:"expiry"`
	Allotment      uint64    `json:"allotment"`
	CreatedAt      time.Time `json:"created_at"`
	CompletedAt    time.Time `json:"completed_at"`
	SessionGranted bool      `json:"session_granted"`
}

// quoteStore persists lightning quote records to a JSON file using atomic
// writes (temp file + rename) so a crash mid-write never leaves a truncated
// quotes file.
type quoteStore struct {
	filePath string
	mu       sync.Mutex
}

// newQuoteStore returns a quoteStore rooted at filePath. The file is not
// touched until saveQuotes or loadQuotes is called.
func newQuoteStore(filePath string) *quoteStore {
	return &quoteStore{filePath: filePath}
}

// saveQuotes serializes all quotes to JSON and atomically writes them to disk.
// A nil or empty map produces an empty JSON object ("{}").
func (qs *quoteStore) saveQuotes(quotes map[string]*lightningQuoteRecord) error {
	persisted := make(map[string]*persistedQuote, len(quotes))
	for quoteID, rec := range quotes {
		if rec == nil {
			continue
		}
		persisted[quoteID] = &persistedQuote{
			QuoteID:        quoteID,
			Bolt11:         rec.Bolt11,
			MacAddress:     rec.MacAddress,
			MintURL:        rec.MintURL,
			Amount:         rec.Amount,
			Expiry:         rec.Expiry,
			Allotment:      rec.Allotment,
			CreatedAt:      rec.CreatedAt,
			CompletedAt:    rec.CompletedAt,
			SessionGranted: rec.SessionGranted,
		}
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal quotes: %w", err)
	}

	qs.mu.Lock()
	defer qs.mu.Unlock()

	if dir := filepath.Dir(qs.filePath); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create quotes dir %s: %w", dir, err)
		}
	}

	// Atomic write: write to a temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(filepath.Dir(qs.filePath), ".quotes-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp quotes file: %w", err)
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
		return fmt.Errorf("write temp quotes file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp quotes file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp quotes file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp quotes file: %w", err)
	}
	if err := os.Rename(tmpName, qs.filePath); err != nil {
		return fmt.Errorf("rename temp quotes file: %w", err)
	}
	cleanup = false
	return nil
}

// loadQuotes reads and deserializes the quotes file. A missing file returns an
// empty map with a nil error so callers can treat a fresh start identically to
// a wipe. Any other read/parse error is returned.
func (qs *quoteStore) loadQuotes() (map[string]*persistedQuote, error) {
	data, err := os.ReadFile(qs.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*persistedQuote), nil
		}
		return nil, fmt.Errorf("read quotes file: %w", err)
	}

	quotes := make(map[string]*persistedQuote)
	if err := json.Unmarshal(data, &quotes); err != nil {
		return nil, fmt.Errorf("unmarshal quotes: %w", err)
	}
	return quotes, nil
}
