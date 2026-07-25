package merchant

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistLightningQuotes_ConcurrentMutation(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"q1": {Bolt11: "lnbc1test", MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: "https://mint.example.com", Amount: 5},
		},
		quoteStore: newQuoteStore(filepath.Join(t.TempDir(), "quotes.json")),
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.persistLightningQuotes()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.lightningQuoteMu.Lock()
			if rec, ok := m.lightningQuotes["q1"]; ok {
				rec.SessionGranted = !rec.SessionGranted
				rec.Allotment = uint64(i)
				rec.CompletedAt = time.Now()
			}
			m.lightningQuoteMu.Unlock()
		}
	}()

	wg.Wait()
}

func TestPersistLightningQuotes_ConcurrentHighLoad(t *testing.T) {
	quotes := make(map[string]*lightningQuoteRecord, 10)
	for i := 0; i < 10; i++ {
		quotes[fmt.Sprintf("q-%d", i)] = &lightningQuoteRecord{
			Bolt11:     "lnbc1test",
			MacAddress: "aa:bb:cc:dd:ee:ff",
			MintURL:    "https://mint.example.com",
			Amount:     uint64(i + 1),
		}
	}
	m := &Merchant{
		lightningQuotes: quotes,
		quoteStore:      newQuoteStore(filepath.Join(t.TempDir(), "quotes.json")),
	}

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			m.persistLightningQuotes()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			m.lightningQuoteMu.Lock()
			for _, rec := range m.lightningQuotes {
				rec.SessionGranted = !rec.SessionGranted
				rec.Allotment = uint64(i)
				rec.CompletedAt = time.Now()
			}
			m.lightningQuoteMu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			m.lightningQuoteMu.Lock()
			m.lightningQuotes[fmt.Sprintf("q-new-%d", i)] = &lightningQuoteRecord{
				Bolt11: "lnbc1new", MacAddress: "11:22:33:44:55:66",
				MintURL: "https://mint.example.com", Amount: 1,
			}
			m.lightningQuoteMu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			m.persistLightningQuotes()
		}
	}()

	wg.Wait()
}

func TestSaveQuotes_ConcurrentSaves(t *testing.T) {
	qs := newQuoteStore(filepath.Join(t.TempDir(), "quotes.json"))
	quotes := map[string]*lightningQuoteRecord{
		"q1": {Bolt11: "lnbc1", MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: "https://mint.example.com", Amount: 5},
		"q2": {Bolt11: "lnbc2", MacAddress: "11:22:33:44:55:66", MintURL: "https://mint.example.com", Amount: 10},
	}

	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := qs.saveQuotes(quotes); err != nil {
					t.Errorf("saveQuotes failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	loaded, err := qs.loadQuotes()
	if err != nil {
		t.Fatalf("loadQuotes failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 quotes after concurrent saves, got %d", len(loaded))
	}
}

func TestPersistLightningQuotes_NilStoreIsNoop(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"q1": {Bolt11: "lnbc1", Amount: 5},
		},
	}
	m.persistLightningQuotes()
}

func TestPersistLightningQuotes_EmptyMapSucceeds(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{},
		quoteStore:      newQuoteStore(filepath.Join(t.TempDir(), "quotes.json")),
	}
	m.persistLightningQuotes()

	loaded, err := m.quoteStore.loadQuotes()
	if err != nil {
		t.Fatalf("loadQuotes failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 quotes, got %d", len(loaded))
	}
}
