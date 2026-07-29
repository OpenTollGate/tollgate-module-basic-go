package merchant

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

func TestProbeWireFormatBytes(t *testing.T) {
	// Probe 1: lightningQuoteRecord with each CachedState value
	for _, c := range []struct {
		name  string
		state nut04.State
	}{
		{"Unpaid", nut04.Unpaid},
		{"Paid", nut04.Paid},
		{"Issued", nut04.Issued},
		{"Pending", nut04.Pending},
		{"Unknown", nut04.Unknown},
	} {
		rec := &lightningQuoteRecord{
			Bolt11:      "lnbc1",
			CachedState: c.state,
		}
		b, _ := json.Marshal(rec)
		t.Logf("lightningQuoteRecord CachedState=%s: %s", c.name, string(b))
	}

	// Probe 2: persistedQuote exact bytes
	pq := &persistedQuote{
		QuoteID:        "quote-1",
		Bolt11:         "lnbc1",
		MacAddress:     "aa:bb:cc:dd:ee:ff",
		MintURL:        "https://mint.example.com",
		Amount:         5,
		Expiry:         1700000000,
		Allotment:      100,
		CreatedAt:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		CompletedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		SessionGranted: true,
	}
	b, _ := json.Marshal(pq)
	t.Logf("persistedQuote marshaled: %s", string(b))

	bi, _ := json.MarshalIndent(pq, "", "  ")
	t.Logf("persistedQuote MarshalIndent:\n%s", string(bi))

	// Probe 3: full quotes map MarshalIndent (actual saveQuotes format)
	m := map[string]*persistedQuote{"quote-1": pq}
	bm, _ := json.MarshalIndent(m, "", "  ")
	t.Logf("map MarshalIndent (saveQuotes format):\n%s", string(bm))

	// Probe 4: zero-value persistedQuote
	zp := &persistedQuote{}
	bz, _ := json.Marshal(zp)
	t.Logf("zero-value persistedQuote: %s", string(bz))

	// Probe 5: zero-value lightningQuoteRecord
	zr := &lightningQuoteRecord{}
	bzr, _ := json.Marshal(zr)
	t.Logf("zero-value lightningQuoteRecord: %s", string(bzr))
}
