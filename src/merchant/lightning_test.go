package merchant

import (
	"errors"
	"testing"
	"time"
)

func TestGetLightningQuoteRecordReturnsErrQuoteNotFound(t *testing.T) {
	m := &Merchant{lightningQuotes: make(map[string]*lightningQuoteRecord)}

	_, err := m.getLightningQuoteRecord("missing")
	if !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("expected ErrQuoteNotFound, got %v", err)
	}
}

func TestGetLightningQuoteRecordForMACHidesMismatchedQuotes(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"quote-1": {MacAddress: "aa:bb:cc:dd:ee:ff"},
		},
	}

	_, err := m.getLightningQuoteRecordForMAC("quote-1", "11:22:33:44:55:66")
	if !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("expected ErrQuoteNotFound for mismatched MAC, got %v", err)
	}
}

func TestCleanupStaleLightningQuotesRemovesSettledQuotes(t *testing.T) {
	now := time.Now()
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"settled": {
				SessionGranted: true,
				CompletedAt:    now.Add(-lightningQuoteSettledRetention - time.Second),
			},
			"recent": {
				SessionGranted: true,
				CompletedAt:    now.Add(-time.Minute),
			},
		},
	}

	m.cleanupStaleLightningQuotes(now)

	if _, exists := m.lightningQuotes["settled"]; exists {
		t.Fatal("expected settled quote to be removed after retention window")
	}
	if _, exists := m.lightningQuotes["recent"]; !exists {
		t.Fatal("expected recent settled quote to remain available")
	}
}

func TestCleanupStaleLightningQuotesRemovesExpiredQuotes(t *testing.T) {
	now := time.Now()
	createdAt := now.Add(-10 * time.Minute)
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"expired": {
				CreatedAt: createdAt,
				Expiry:    uint64(createdAt.Add(time.Minute).Unix()),
			},
			"fresh": {
				CreatedAt: now.Add(-time.Minute),
				Expiry:    uint64(now.Add(5 * time.Minute).Unix()),
			},
		},
	}

	m.cleanupStaleLightningQuotes(now)

	if _, exists := m.lightningQuotes["expired"]; exists {
		t.Fatal("expected expired quote to be removed after grace period")
	}
	if _, exists := m.lightningQuotes["fresh"]; !exists {
		t.Fatal("expected unexpired quote to remain available")
	}
}
