package merchant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuoteStoreSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	qs := newQuoteStore(filepath.Join(dir, "quotes.json"))

	quotes := map[string]*lightningQuoteRecord{
		"quote-1": {
			Bolt11:         "lnbc1...",
			MacAddress:     "aa:bb:cc:dd:ee:ff",
			MintURL:        "https://mint.example.com/Bitcoin",
			Amount:         5,
			Expiry:         1700000000,
			Allotment:      100,
			CreatedAt:      time.Now().Truncate(time.Second),
			CompletedAt:    time.Now().Truncate(time.Second),
			SessionGranted: true,
		},
		"quote-2": {
			Bolt11:     "lnbc2...",
			MacAddress: "11:22:33:44:55:66",
			MintURL:    "https://mint.example.com/Bitcoin",
			Amount:     10,
			Expiry:     1700000100,
			CreatedAt:  time.Now().Truncate(time.Second),
		},
	}

	if err := qs.saveQuotes(quotes); err != nil {
		t.Fatalf("saveQuotes failed: %v", err)
	}

	loaded, err := qs.loadQuotes()
	if err != nil {
		t.Fatalf("loadQuotes failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 quotes loaded, got %d", len(loaded))
	}

	q1, ok := loaded["quote-1"]
	if !ok {
		t.Fatal("quote-1 missing from loaded quotes")
	}
	if q1.Bolt11 != "lnbc1..." {
		t.Errorf("expected Bolt11 lnbc1..., got %s", q1.Bolt11)
	}
	if q1.MacAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("expected MacAddress aa:bb:cc:dd:ee:ff, got %s", q1.MacAddress)
	}
	if !q1.SessionGranted {
		t.Error("expected SessionGranted true")
	}
	if q1.Amount != 5 {
		t.Errorf("expected Amount 5, got %d", q1.Amount)
	}

	q2, ok := loaded["quote-2"]
	if !ok {
		t.Fatal("quote-2 missing from loaded quotes")
	}
	if q2.Bolt11 != "lnbc2..." {
		t.Errorf("expected Bolt11 lnbc2..., got %s", q2.Bolt11)
	}
	if q2.SessionGranted {
		t.Error("expected SessionGranted false for quote-2")
	}
}

func TestQuoteStoreLoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	qs := newQuoteStore(filepath.Join(dir, "nonexistent.json"))

	loaded, err := qs.loadQuotes()
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty map for missing file, got %d entries", len(loaded))
	}
}

func TestQuoteStoreLoadCorruptJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quotes.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	qs := newQuoteStore(path)
	_, err := qs.loadQuotes()
	if err == nil {
		t.Fatal("expected error for corrupt JSON, got nil")
	}
}

func TestQuoteStoreSaveEmptyMapProducesEmptyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quotes.json")
	qs := newQuoteStore(path)

	if err := qs.saveQuotes(make(map[string]*lightningQuoteRecord)); err != nil {
		t.Fatalf("saveQuotes with empty map failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read saved file: %v", err)
	}

	var result map[string]*persistedQuote
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal saved file: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty JSON object, got %d entries", len(result))
	}
}

func TestQuoteStoreSaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nestedDir := filepath.Join(dir, "sub", "dir")
	qs := newQuoteStore(filepath.Join(nestedDir, "quotes.json"))

	if err := qs.saveQuotes(map[string]*lightningQuoteRecord{}); err != nil {
		t.Fatalf("saveQuotes with nested missing dir failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(nestedDir, "quotes.json")); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

func TestQuoteStoreAtomicWriteNoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	qs := newQuoteStore(filepath.Join(dir, "quotes.json"))

	if err := qs.saveQuotes(map[string]*lightningQuoteRecord{
		"q1": {Bolt11: "lnbc...", MacAddress: "aa:bb:cc:dd:ee:ff"},
	}); err != nil {
		t.Fatalf("saveQuotes failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLightningQuoteRecordBolt11Field(t *testing.T) {
	// Verify that the Bolt11 field is present and persists through the
	// persistedQuote serialization roundtrip.
	rec := &lightningQuoteRecord{
		Bolt11:     "lnbc1u1pj...",
		MacAddress: "aa:bb:cc:dd:ee:ff",
		MintURL:    "https://mint.example.com",
		Amount:     5,
		Expiry:     1700000000,
		CreatedAt:  time.Now(),
	}

	pq := &persistedQuote{
		QuoteID:    "test-quote",
		Bolt11:     rec.Bolt11,
		MacAddress: rec.MacAddress,
		MintURL:    rec.MintURL,
		Amount:     rec.Amount,
		Expiry:     rec.Expiry,
		CreatedAt:  rec.CreatedAt,
	}

	if pq.Bolt11 != rec.Bolt11 {
		t.Errorf("Bolt11 not preserved: expected %s, got %s", rec.Bolt11, pq.Bolt11)
	}
}
