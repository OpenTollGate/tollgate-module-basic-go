package lnurlcash

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fpFor builds a well-formed 64-hex journal key. The journal only accepts real
// fingerprints — a truncated or non-hex key is a corrupt record, not a note — so
// the fixtures use the same shape production writes.
func fpFor(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The redemption journal is the thing that makes "one note, one grant" true
// across a crash. It is a local transaction primitive: it never talks to the
// network, and its atomicity is file-system atomicity (temp + fsync + rename +
// directory fsync), not a transaction with the issuer or the mint.
func TestIntentStoreSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intents.json")
	store, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	rec, err := store.Begin(fpFor("note-1"), Intent{
		MacAddress: "aa:bb:cc:dd:ee:ff",
		MintURL:    testMint,
		AmountSats: 1500,
		IssuerHost: "issuer.example",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if rec.State != IntentPending || rec.Version != 1 {
		t.Fatalf("fresh intent = %+v, want pending v1", rec)
	}
	if _, err := store.MarkSubmitted(fpFor("note-1"), "quote-1"); err != nil {
		t.Fatalf("MarkSubmitted: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, ok, err := reopened.Get(fpFor("note-1"))
	if err != nil || !ok {
		t.Fatalf("Get after reopen = %v, %v", ok, err)
	}
	if got.State != IntentSubmitted {
		t.Fatalf("state after reopen = %q, want %q — a submitted redemption must survive a restart",
			got.State, IntentSubmitted)
	}
	if got.QuoteID != "quote-1" {
		t.Fatalf("quote after reopen = %q", got.QuoteID)
	}
	if got.AmountSats != 1500 || got.MintURL != testMint {
		t.Fatalf("record lost fields across reopen: %+v", got)
	}
}

// The journal file holds a note fingerprint and the client's MAC; it is written
// 0600 and is never world-readable.
func TestIntentStoreFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intents.json")
	store, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()
	if _, err := store.Begin(fpFor("note-1"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Fatalf("journal mode = %o, want 0600", perm)
	}
}

// A corrupt or foreign journal must refuse to open rather than silently start
// empty: an empty journal is a fresh licence to grant for every note already
// presented.
func TestIntentStoreRefusesUnreadableState(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed json", content: "{not json"},
		{name: "wrong shape", content: `[]`},
		{name: "unknown field", content: `{"records":{"a":{"state":"pending","surprise":1}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "intents.json")
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := OpenIntentStore(path); err == nil {
				t.Fatal("opened a journal it could not understand")
			}
		})
	}
}

// Exactly one writer may hold the journal. A second Open refuses instead of
// interleaving snapshots with the first.
func TestIntentStoreRefusesASecondWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intents.json")
	first, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer first.Close()

	if _, err := OpenIntentStore(path); err == nil {
		t.Fatal("a second writer opened the journal")
	}
}

// Every transition is a compare-and-set on Version, so a stale writer cannot
// rewind a resolution (which would re-open a note for a second grant).
func TestIntentStoreTransitionsAreVersioned(t *testing.T) {
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()

	rec, err := store.Begin(fpFor("note-1"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	submitted, err := store.MarkSubmitted(fpFor("note-1"), "quote-1")
	if err != nil {
		t.Fatalf("MarkSubmitted: %v", err)
	}
	if submitted.Version != rec.Version+1 {
		t.Fatalf("version after submit = %d, want %d", submitted.Version, rec.Version+1)
	}

	// A stale version must be refused.
	if _, err := store.Advance(fpFor("note-1"), rec.Version, IntentAbandoned, ""); err == nil {
		t.Fatal("accepted a stale-version transition")
	}
	// And a resolved record cannot be rewound to pending.
	if _, err := store.Advance(fpFor("note-1"), submitted.Version, IntentPending, ""); err == nil {
		t.Fatal("rewound a submitted record to pending")
	}
	// The honest terminal transition is accepted.
	if _, err := store.Advance(fpFor("note-1"), submitted.Version, IntentAmbiguous, "no answer"); err != nil {
		t.Fatalf("Advance to ambiguous: %v", err)
	}
}

// Begin is idempotent for one fingerprint and refuses to rebind it to another
// client, mint, or amount: the record is the note's identity.
func TestIntentStoreBeginRefusesRebinding(t *testing.T) {
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()

	base := Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1000, IssuerHost: "issuer.example"}
	if _, err := store.Begin(fpFor("note-1"), base); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := store.Begin(fpFor("note-1"), base); err != nil {
		t.Fatalf("idempotent Begin was refused: %v", err)
	}

	other := base
	other.MacAddress = "11:22:33:44:55:66"
	if _, err := store.Begin(fpFor("note-1"), other); err == nil {
		t.Fatal("rebound one note's record to a different client")
	}
	other = base
	other.AmountSats = 99_999
	if _, err := store.Begin(fpFor("note-1"), other); err == nil {
		t.Fatal("rebound one note's record to a different amount")
	}
}

// Retention bounds the journal on a router with a small, flash-backed /var.
//
// The rule is NOT "drop old terminal records". A `redeemed` record is the only thing that
// makes a replay refused locally; deleting it means the gate's protection reduces to the
// issuer refusing a spent note, which is exactly the dependency this feature exists to
// avoid. So:
//
//   - `redeemed` is COMPACTED to a tombstone: state and time survive, the per-payment
//     detail (quote, invoice, MAC) is dropped.
//   - `ambiguous` likewise — "do not present this again" is the entire remedy.
//   - `abandoned` is deleted: nothing was promised, so a fresh attempt is legitimate.
//   - a pending or submitted record is NEVER touched; dropping one re-opens its note.
func TestIntentStoreRetentionCompactsGuardsAndDropsOnlyRefusals(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()
	store.now = func() time.Time { return now }

	// A redeemed note: must survive as a tombstone.
	if _, err := store.Begin(fpFor("old-redeemed"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	rec, err := store.MarkSubmitted(fpFor("old-redeemed"), "q-1")
	if err != nil {
		t.Fatalf("MarkSubmitted: %v", err)
	}
	if _, err := store.Advance(fpFor("old-redeemed"), rec.Version, IntentRedeemed, "paid"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	// An abandoned note: may be deleted, nothing was promised.
	if _, err := store.Begin(fpFor("old-abandoned"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ab, _, err := store.Get(fpFor("old-abandoned"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := store.Advance(fpFor("old-abandoned"), ab.Version, IntentAbandoned, "refused"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	// A pending note: never touched.
	if _, err := store.Begin(fpFor("still-pending"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	now = now.Add(365 * 24 * time.Hour)
	if _, err := store.Prune(24 * time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// The redeemed record survives as a guard, with its detail gone.
	kept, ok, err := store.Get(fpFor("old-redeemed"))
	if err != nil || !ok {
		t.Fatalf("dropped a redeemed record (ok=%v err=%v): a replay now depends on the issuer refusing it", ok, err)
	}
	if kept.State != IntentRedeemed {
		t.Fatalf("tombstone state = %q, want %q: the replay guard depends on it", kept.State, IntentRedeemed)
	}
	if !kept.Compacted {
		t.Fatal("the redeemed record was not marked compacted")
	}
	if kept.QuoteID != "" || kept.Invoice != "" || kept.MacAddress != "" {
		t.Fatalf("tombstone still carries per-payment detail: %+v", kept)
	}

	// The abandoned record is gone; the pending one is untouched.
	if _, ok, _ := store.Get(fpFor("old-abandoned")); ok {
		t.Fatal("kept an abandoned record: nothing was promised, so it need not be remembered")
	}
	if _, ok, _ := store.Get(fpFor("still-pending")); !ok {
		t.Fatal("dropped a pending record — that re-opens its note for a second grant")
	}

	// A second prune must not keep re-writing already-compacted tombstones.
	if n, err := store.Prune(24 * time.Hour); err != nil || n != 0 {
		t.Fatalf("second Prune changed %d records (err=%v), want 0: the tombstone is idempotent", n, err)
	}
}

// A negative or absent retention must not be read as "drop everything".
func TestPruneWithNoRetentionDropsNothing(t *testing.T) {
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()
	if _, err := store.Begin(fpFor("note-x"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "i"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, retention := range []time.Duration{0, -time.Hour} {
		if dropped, err := store.Prune(retention); err != nil || dropped != 0 {
			t.Fatalf("Prune(%v) = %d, %v; want 0, nil", retention, dropped, err)
		}
	}
}

// The journal's own file must never contain the note itself: it holds a
// fingerprint, and a fingerprint is not a bearer instrument.
func TestIntentStoreFileHoldsNoNoteMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intents.json")
	store, err := OpenIntentStore(path)
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	defer store.Close()
	if _, err := store.Begin(fpFor("note-1"), Intent{MacAddress: "aa:bb:cc:dd:ee:ff", MintURL: testMint, AmountSats: 1, IssuerHost: "issuer.example"}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if strings.Contains(string(raw), "k1secret") || strings.Contains(string(raw), "lnurl1") {
		t.Fatalf("journal holds note material: %s", raw)
	}
}
