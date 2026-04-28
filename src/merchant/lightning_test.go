package merchant

import (
	"errors"
	"testing"
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
