package merchant

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistLightningQuotes_ConcurrentReadWriteRace(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"q1": {
				Bolt11:     "lnbc1test",
				MacAddress: "aa:bb:cc:dd:ee:ff",
				MintURL:    "https://mint.example.com",
				Amount:     5,
			},
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
