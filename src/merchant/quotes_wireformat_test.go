//go:build testenv

package merchant

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
)

// TestQuotesWireFormat locks the wire format of quotes.json and characterizes
// nut04.State marshaling behavior. After the Wave 4.2 refactor that swaps
// nut04.State for tollwallet.MintQuoteState, this test must PASS UNMODIFIED
// to prove on-disk format preservation.
//
// CRITICAL FINDING: CachedState is NOT persisted to disk. The on-disk struct
// is persistedQuote (quote_store.go:15-26) which explicitly excludes transient
// fields. Therefore the refactor of CachedState's type cannot corrupt
// existing quotes.json files.
func TestQuotesWireFormat(t *testing.T) {
	t.Run("bare_state_marshals_to_integer", func(t *testing.T) {
		// nut04.State is `type State int` with NO custom MarshalJSON.
		// It always marshals as a raw integer.
		cases := []struct {
			name  string
			state nut04.State
			want  string
		}{
			{"Unpaid", nut04.Unpaid, "0"},
			{"Paid", nut04.Paid, "1"},
			{"Issued", nut04.Issued, "2"},
			{"Pending", nut04.Pending, "3"},
			{"Unknown", nut04.Unknown, "4"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := json.Marshal(tc.state)
				if err != nil {
					t.Fatalf("Marshal(%d): %v", tc.state, err)
				}
				if string(got) != tc.want {
					t.Fatalf("Marshal(nut04.State(%d)) = %s, want %s", tc.state, got, tc.want)
				}
			})
		}
	})

	t.Run("cached_state_in_record_marshals_as_integer", func(t *testing.T) {
		// Pin that lightningQuoteRecord.CachedState marshals as integer
		// within the struct (no json tags → PascalCase field name).
		// Note: this struct is NEVER written to disk directly — only
		// persistedQuote is persisted.
		record := lightningQuoteRecord{
			Bolt11:         "lnbc1u1pj...",
			CachedState:    nut04.Paid,
			HasCachedState: true,
		}
		got, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		// CachedState must appear as integer 1 (not string "PAID")
		if !containsStr(string(got), `"CachedState":1`) {
			t.Fatalf("expected CachedState as integer 1 in JSON, got: %s", got)
		}
		// Must NOT contain string representation
		if containsStr(string(got), `"CachedState":"PAID"`) {
			t.Fatalf("CachedState should NOT be string 'PAID', got: %s", got)
		}
	})

	t.Run("persisted_quote_on_disk_bytes", func(t *testing.T) {
		// Lock the EXACT on-disk wire format for persistedQuote (the struct
		// actually written to quotes.json). This is the format that
		// production routers have on disk today.
		pq := persistedQuote{
			QuoteID:        "quote-1",
			Bolt11:         "lnbc1u1pj...",
			MacAddress:     "aa:bb:cc:dd:ee:ff",
			MintURL:        "https://mint.example.com",
			Amount:         5,
			Expiry:         1700000000,
			Allotment:      100,
			CreatedAt:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			CompletedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			SessionGranted: true,
		}
		got, err := json.Marshal(pq)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		// The exact wire format: snake_case keys, no whitespace, all 10 fields
		want := `{"quote_id":"quote-1","bolt11":"lnbc1u1pj...","mac_address":"aa:bb:cc:dd:ee:ff","mint_url":"https://mint.example.com","amount":5,"expiry":1700000000,"allotment":100,"created_at":"2024-01-01T00:00:00Z","completed_at":"2024-01-01T00:00:00Z","session_granted":true}`
		if string(got) != want {
			t.Fatalf("persistedQuote wire format mismatch:\ngot:  %s\nwant: %s", got, want)
		}
		// CRITICAL: no CachedState field in the on-disk format
		if containsStr(string(got), "CachedState") || containsStr(string(got), "cached_state") {
			t.Fatalf("persistedQuote must NOT contain CachedState: %s", got)
		}
	})

	t.Run("full_record_round_trip", func(t *testing.T) {
		// Round-trip persistedQuote through marshal → unmarshal → deep equal.
		original := persistedQuote{
			QuoteID:        "round-trip-test",
			Bolt11:         "lnbc1000n1pj3...",
			MacAddress:     "11:22:33:44:55:66",
			MintURL:        "https://mint.coinos.io/Bitcoin",
			Amount:         10,
			Expiry:         1735689600,
			Allotment:      11010048,
			CreatedAt:      time.Date(2025, 6, 15, 12, 30, 45, 0, time.UTC),
			CompletedAt:    time.Date(2025, 6, 15, 12, 31, 0, 0, time.UTC),
			SessionGranted: true,
		}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var restored persistedQuote
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if restored != original {
			t.Fatalf("round-trip mismatch:\noriginal:  %+v\nrestored: %+v", original, restored)
		}
	})

	t.Run("production_format_fixture", func(t *testing.T) {
		// Parse a representative production quotes.json file with two records.
		// This proves the on-disk schema is what we expect.
		fixture := `{
  "unpaid-quote-abc": {
    "quote_id": "unpaid-quote-abc",
    "bolt11": "lnbc1000n1pj3q...",
    "mac_address": "aa:bb:cc:dd:ee:ff",
    "mint_url": "https://mint.coinos.io/Bitcoin",
    "amount": 10,
    "expiry": 1735689600,
    "allotment": 0,
    "created_at": "2025-01-01T00:00:00Z",
    "completed_at": "0001-01-01T00:00:00Z",
    "session_granted": false
  },
  "settled-quote-xyz": {
    "quote_id": "settled-quote-xyz",
    "bolt11": "lnbc500n1pj3q...",
    "mac_address": "11:22:33:44:55:66",
    "mint_url": "https://mint.coinos.io/Bitcoin",
    "amount": 5,
    "expiry": 1735689600,
    "allotment": 11010048,
    "created_at": "2025-01-01T00:01:00Z",
    "completed_at": "2025-01-01T00:02:00Z",
    "session_granted": true
  }
}`
		var quotes map[string]*persistedQuote
		if err := json.Unmarshal([]byte(fixture), &quotes); err != nil {
			t.Fatalf("failed to parse production fixture: %v", err)
		}
		if len(quotes) != 2 {
			t.Fatalf("expected 2 quotes, got %d", len(quotes))
		}
		unpaid, ok := quotes["unpaid-quote-abc"]
		if !ok {
			t.Fatal("missing unpaid-quote-abc")
		}
		if unpaid.Amount != 10 || unpaid.SessionGranted != false {
			t.Fatalf("unpaid quote: amount=%d, session_granted=%v", unpaid.Amount, unpaid.SessionGranted)
		}
		settled := quotes["settled-quote-xyz"]
		if settled.Allotment != 11010048 || settled.SessionGranted != true {
			t.Fatalf("settled quote: allotment=%d, session_granted=%v", settled.Allotment, settled.SessionGranted)
		}
		// CRITICAL: fixture must NOT have any CachedState/cached_state field
		if containsStr(fixture, "cached_state") || containsStr(fixture, "CachedState") {
			t.Fatal("production fixture should NOT contain cached_state field")
		}
	})

	t.Run("zero_value_state", func(t *testing.T) {
		// Characterize zero-value nut04.State and persistedQuote.
		var zeroState nut04.State
		if zeroState != nut04.Unpaid {
			t.Fatalf("zero nut04.State = %d, want 0 (Unpaid)", zeroState)
		}
		got, _ := json.Marshal(zeroState)
		if string(got) != "0" {
			t.Fatalf("zero State marshals to %s, want '0'", got)
		}
		// Zero-value persistedQuote
		var zeroPQ persistedQuote
		got, _ = json.Marshal(zeroPQ)
		want := `{"quote_id":"","bolt11":"","mac_address":"","mint_url":"","amount":0,"expiry":0,"allotment":0,"created_at":"0001-01-01T00:00:00Z","completed_at":"0001-01-01T00:00:00Z","session_granted":false}`
		if string(got) != want {
			t.Fatalf("zero persistedQuote:\ngot:  %s\nwant: %s", got, want)
		}
	})

	t.Run("time_format_is_rfc3339_utc", func(t *testing.T) {
		// Pin the time.Time serialization format used in persistedQuote.
		// Go's default JSON marshaling for time.Time is RFC3339Nano.
		// Whole seconds produce no fractional part.
		ts := time.Date(2024, 6, 15, 12, 30, 45, 0, time.UTC)
		got, _ := json.Marshal(ts)
		want := `"2024-06-15T12:30:45Z"`
		if string(got) != want {
			t.Fatalf("whole-second time: got %s, want %s", got, want)
		}
		// Sub-second precision includes nanoseconds.
		tsNano := time.Date(2024, 6, 15, 12, 30, 45, 678901234, time.UTC)
		got, _ = json.Marshal(tsNano)
		wantNano := `"2024-06-15T12:30:45.678901234Z"`
		if string(got) != wantNano {
			t.Fatalf("sub-second time: got %s, want %s", got, wantNano)
		}
	})
}

// containsStr is a simple substring helper.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(sub) > 0 && indexOfStr(s, sub) >= 0))
}

func indexOfStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
