package lnurlcash

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The redemption-intent journal.
//
// This is the merchant package's receive-intent journal applied to a different
// money move, deliberately: same states-in-a-file shape, same atomic
// temp + fsync + rename + directory-fsync write, same single-writer flock. It is
// NOT paymentjournal — that package is a much larger primitive (budgets,
// reservations, outgoing fee accounting) and depending on it here would import
// machinery this feature does not use and cannot feed.
//
// The record exists for exactly one reason: the callback to the issuer is the
// point of no return (the issuer burns the note), so the intent to send it must
// be durable BEFORE it is sent, and a second presentation of the same note must be
// answerable from disk without touching the network.
//
//	begin:      intent written, nothing sent yet             -> pending
//	submitted:  we asked our mint for a quote; callback next -> submitted
//	sent:       the callback went out, outcome not yet known -> submitted
//	redeemed:   the MINT confirmed the quote → terminal, grantable
//	abandoned:  the issuer refused → terminal, the note was NOT burned by us
//	ambiguous:  no classifiable answer → terminal, NEVER retried
//
// On restart a `pending` record can proceed (nothing was sent) and a `submitted`
// record can only be reconciled against the mint's own quote state — never
// re-sent.
const (
	IntentPending   = "pending"
	IntentSubmitted = "submitted"
	IntentRedeemed  = "redeemed"
	IntentAbandoned = "abandoned"
	IntentAmbiguous = "ambiguous"
)

var (
	// ErrIntentNotFound is returned when no record exists for a fingerprint.
	ErrIntentNotFound = errors.New("lnurlcash: no redemption intent for that note")
	// ErrIntentConflict is a refused compare-and-set or a refused rebinding.
	ErrIntentConflict = errors.New("lnurlcash: redemption intent conflict")
	// ErrIntentStoreLocked means another process holds the journal.
	ErrIntentStoreLocked = errors.New("lnurlcash: redemption intent journal is held by another writer")
	// ErrIntentStoreCorrupt means the file could not be trusted, so it was not
	// used. Refusing is the only safe answer: an empty journal is a licence to
	// grant for every note already presented.
	ErrIntentStoreCorrupt = errors.New("lnurlcash: redemption intent journal is unreadable")
)

const (
	intentFormat     = 1
	maxIntentRecords = 4096
)

// Intent is one redemption's durable record. It holds a fingerprint (never the
// note) and the client's MAC, and no secret of any kind.
type Intent struct {
	MacAddress   string    `json:"mac_address"`
	MintURL      string    `json:"mint_url"`
	AmountSats   uint64    `json:"amount_sats"`
	IssuerHost   string    `json:"issuer_host"`
	QuoteID      string    `json:"quote_id,omitempty"`
	Invoice      string    `json:"invoice,omitempty"`
	State        string    `json:"state"`
	Version      uint64    `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Resolution   string    `json:"resolution,omitempty"`
	AttemptCount int       `json:"attempt_count"`
	// Compacted marks a tombstone: the note's state is remembered so a replay is still
	// refused, but the bulky per-payment detail (quote, invoice, MAC, amounts) has been
	// dropped by Prune. It is never set on a pending or submitted record.
	Compacted bool `json:"compacted,omitempty"`
}

type intentFile struct {
	Format  int               `json:"format"`
	Records map[string]Intent `json:"records"`
}

// IntentStore is the single-writer journal.
type IntentStore struct {
	mu     sync.Mutex
	path   string
	lock   *os.File
	data   intentFile
	now    func() time.Time
	closed bool
}

// OpenIntentStore opens (creating if absent) the journal at path and takes the
// exclusive writer lock. The file is 0600; the lock is a sibling file so a
// reader that ignores the lock cannot mistake a half-written snapshot for state.
func OpenIntentStore(path string) (*IntentStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrIntentStoreCorrupt
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("lnurlcash: create intent journal dir: %w", err)
		}
	}

	s := &IntentStore{
		path: abs,
		data: intentFile{Format: intentFormat, Records: map[string]Intent{}},
		now:  func() time.Time { return time.Now().UTC() },
	}

	lockPath := abs + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("lnurlcash: open intent journal lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrIntentStoreLocked
	}
	s.lock = f

	if err := s.load(); err != nil {
		s.lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *IntentStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // a fresh journal is a legitimate state, not corruption
		}
		return ErrIntentStoreCorrupt
	}
	if len(raw) == 0 {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Unknown fields are refused: a journal written by a newer version may mean
	// something this build does not understand, and silently dropping it is how
	// a "resolved" record would read as "pending" again.
	decoder.DisallowUnknownFields()
	var file intentFile
	if err := decoder.Decode(&file); err != nil {
		return ErrIntentStoreCorrupt
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrIntentStoreCorrupt
	}
	if file.Format != intentFormat || file.Records == nil {
		return ErrIntentStoreCorrupt
	}
	if len(file.Records) > maxIntentRecords {
		return ErrIntentStoreCorrupt
	}
	for fp, rec := range file.Records {
		if !validFingerprint(fp) || !validIntent(rec) || rec.UpdatedAt.Before(rec.CreatedAt) {
			return ErrIntentStoreCorrupt
		}
	}
	s.data = file
	return nil
}

func validFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	for _, r := range fp {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func validIntent(rec Intent) bool {
	if rec.MacAddress == "" || rec.MintURL == "" || rec.IssuerHost == "" || rec.AmountSats == 0 || rec.Version == 0 {
		return false
	}
	switch rec.State {
	case IntentPending, IntentSubmitted, IntentRedeemed, IntentAbandoned, IntentAmbiguous:
		return true
	}
	return false
}

// Close releases the writer lock. The journal is left on disk.
func (s *IntentStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.lock == nil {
		return nil
	}
	return s.lock.Close()
}

// Get returns the record for a note fingerprint.
func (s *IntentStore) Get(fp string) (Intent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Intent{}, false, ErrIntentConflict
	}
	rec, ok := s.data.Records[fp]
	return rec, ok, nil
}

// List returns every record, ordered by fingerprint, for reconciliation and for
// operator tooling.
func (s *IntentStore) List() ([]Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrIntentConflict
	}
	out := make([]Intent, 0, len(s.data.Records))
	for _, rec := range s.data.Records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Begin records the intent to redeem one note, durably, BEFORE anything is sent.
//
// Idempotent for the same fingerprint and the same terms. A different MAC, mint,
// amount, or issuer for an existing fingerprint is refused: the record IS the
// note's identity, and letting a caller rewrite it would let one note be
// resolved twice under two different prices.
func (s *IntentStore) Begin(fp string, in Intent) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginLocked(fp, in)
}

func (s *IntentStore) beginLocked(fp string, in Intent) (Intent, error) {
	if s.closed {
		return Intent{}, ErrIntentConflict
	}
	if !validFingerprint(fp) {
		return Intent{}, ErrIntentConflict
	}
	if existing, ok := s.data.Records[fp]; ok {
		if existing.MacAddress != in.MacAddress || existing.MintURL != in.MintURL ||
			existing.AmountSats != in.AmountSats || existing.IssuerHost != in.IssuerHost {
			return Intent{}, ErrIntentConflict
		}
		return existing, nil
	}
	if len(s.data.Records) >= maxIntentRecords {
		return Intent{}, ErrIntentConflict
	}

	rec := Intent{
		MacAddress: in.MacAddress,
		MintURL:    in.MintURL,
		AmountSats: in.AmountSats,
		IssuerHost: in.IssuerHost,
		State:      IntentPending,
		Version:    1,
		CreatedAt:  s.now(),
		UpdatedAt:  s.now(),
	}
	if rec.UpdatedAt.Before(rec.CreatedAt) {
		rec.UpdatedAt = rec.CreatedAt
	}
	if !validIntent(rec) {
		return Intent{}, ErrIntentConflict
	}

	next := s.copyFile()
	next.Records[fp] = rec
	if err := s.persist(next); err != nil {
		return Intent{}, err
	}
	return rec, nil
}

// MarkSubmitted records that a quote was obtained and the callback is about to
// leave. From here the redemption can never be re-sent; only reconciled.
func (s *IntentStore) MarkSubmitted(fp, quoteID string) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data.Records[fp]
	if !ok {
		return Intent{}, ErrIntentNotFound
	}
	if rec.State != IntentPending {
		// Already submitted (a retry of the same step) or already terminal: the
		// caller reconciles, it does not transition.
		return rec, nil
	}
	rec.State = IntentSubmitted
	rec.QuoteID = quoteID
	rec.AttemptCount++
	rec.Version++
	rec.UpdatedAt = s.advanceTime(rec)
	next := s.copyFile()
	next.Records[fp] = rec
	if err := s.persist(next); err != nil {
		return Intent{}, err
	}
	return rec, nil
}

// Advance is the CAS transition into a terminal state. Pending and submitted are
// not reachable through it: a resolution can only move forward, because moving one
// backwards re-opens a note for a second grant.
func (s *IntentStore) Advance(fp string, version uint64, state, note string) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != IntentRedeemed && state != IntentAbandoned && state != IntentAmbiguous {
		return Intent{}, ErrIntentConflict
	}
	rec, ok := s.data.Records[fp]
	if !ok {
		return Intent{}, ErrIntentNotFound
	}
	if rec.Version != version {
		return Intent{}, ErrIntentConflict
	}
	if rec.State == IntentRedeemed || rec.State == IntentAbandoned || rec.State == IntentAmbiguous {
		return Intent{}, ErrIntentConflict
	}
	rec.State = state
	rec.Resolution = note
	rec.Version++
	rec.UpdatedAt = s.advanceTime(rec)
	next := s.copyFile()
	next.Records[fp] = rec
	if err := s.persist(next); err != nil {
		return Intent{}, err
	}
	return rec, nil
}

// SetInvoice stores the invoice our mint issued for this redemption, before it is
// handed to the issuer. It is bookkeeping, not a state change, so it does not
// bump Version; it is written once and refused if it disagrees.
func (s *IntentStore) SetInvoice(fp, invoice string) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data.Records[fp]
	if !ok {
		return Intent{}, ErrIntentNotFound
	}
	// A `pending` record may REPLACE its invoice. Pending means the callback was never sent,
	// so the issuer never saw the old invoice and nothing was promised on it — the record is
	// the crash window between SetInvoice and MarkSubmitted. Refusing the replacement is what
	// wedged the note: every retry asked the mint for a new quote and collided here, so the
	// note was abandoned forever while still unspent at the issuer.
	//
	// Once submitted, the invoice is binding: replacing it would be a second payment attempt
	// against a note already in flight.
	if rec.State == IntentPending {
		rec.Invoice = invoice
		rec.UpdatedAt = s.advanceTime(rec)
		next := s.copyFile()
		next.Records[fp] = rec
		if err := s.persist(next); err != nil {
			return Intent{}, err
		}
		return rec, nil
	}
	if rec.Invoice == invoice {
		return rec, nil
	}
	if rec.Invoice != "" {
		return Intent{}, ErrIntentConflict
	}
	rec.Invoice = invoice
	rec.UpdatedAt = s.advanceTime(rec)
	next := s.copyFile()
	next.Records[fp] = rec
	if err := s.persist(next); err != nil {
		return Intent{}, err
	}
	return rec, nil
}

// Prune compacts old terminal records. It never drops a pending or submitted record:
// dropping one of those is exactly the state that re-opens a note for a second grant.
//
// A terminal record is COMPACTED, not deleted, when it carries a guarantee:
//
//   - `redeemed` must be remembered, because forgetting it means a replay depends
//     entirely on the issuer refusing a spent note — and the whole point of this
//     feature is not to depend on the issuer's word. The tombstone keeps the state and
//     the time and drops the bulky, revealing detail (quote, invoice, MAC).
//   - `ambiguous` must be remembered for the same reason, more so: "do not present this
//     again" is the only thing standing between an unclear outcome and a second attempt.
//   - `abandoned` MAY be deleted. The note was refused before any callback, so nothing
//     was promised and a later presentation is a legitimate fresh attempt.
//
// A non-positive retention prunes nothing. The access a redeemed record produced lives in
// the session, not in the record, so compaction cannot take access away from a customer
// who already paid.
func (s *IntentStore) Prune(retention time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if retention <= 0 {
		return 0, nil
	}
	cutoff := s.now().Add(-retention)
	changed := 0
	next := s.copyFile()
	for fp, rec := range next.Records {
		switch rec.State {
		case IntentRedeemed, IntentAmbiguous:
			if rec.UpdatedAt.After(cutoff) || rec.Compacted {
				continue
			}
			// Keep the record VALID, drop only the bulky and revealing parts. validIntent
			// requires MacAddress, MintURL, IssuerHost, AmountSats and Version: dropping any
			// of them makes the record invalid, and the store rejects the WHOLE journal when
			// one record is invalid — so a leaner tombstone would make the file unreadable on
			// the next boot and take every replay guard with it.
			//
			// What compaction actually removes: the BOLT11 invoice (long), the quote id, and
			// the per-attempt narration. What it keeps is what the guard needs.
			next.Records[fp] = Intent{
				MacAddress: rec.MacAddress,
				MintURL:    rec.MintURL,
				AmountSats: rec.AmountSats,
				IssuerHost: rec.IssuerHost,
				State:      rec.State,
				Version:    rec.Version,
				CreatedAt:  rec.CreatedAt,
				UpdatedAt:  rec.UpdatedAt,
				Compacted:  true,
			}
			changed++
		case IntentAbandoned:
			if rec.UpdatedAt.After(cutoff) {
				continue
			}
			delete(next.Records, fp)
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	if err := s.persist(next); err != nil {
		return 0, err
	}
	return changed, nil
}

func (s *IntentStore) advanceTime(rec Intent) time.Time {
	now := s.now()
	if now.Before(rec.CreatedAt) {
		now = rec.CreatedAt
	}
	if now.Before(rec.UpdatedAt) {
		now = rec.UpdatedAt
	}
	return now
}

func (s *IntentStore) copyFile() intentFile {
	out := intentFile{Format: intentFormat, Records: make(map[string]Intent, len(s.data.Records))}
	for fp, rec := range s.data.Records {
		out.Records[fp] = rec
	}
	return out
}

// persist writes the whole journal atomically: temp file in the same directory,
// fsync the file, rename over the target, fsync the directory. A crash at any
// point leaves either the old snapshot or the new one, never a torn one — which is
// what makes "durable before the callback" a real claim rather than an intention.
func (s *IntentStore) persist(next intentFile) error {
	if s.closed {
		return ErrIntentConflict
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("lnurlcash: marshal intent journal: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".lnurlcash-intents-*.tmp")
	if err != nil {
		return fmt.Errorf("lnurlcash: create intent journal temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("lnurlcash: write intent journal temp: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("lnurlcash: chmod intent journal temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("lnurlcash: sync intent journal temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("lnurlcash: close intent journal temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("lnurlcash: rename intent journal: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("lnurlcash: sync intent journal dir: %w", err)
	}
	cleanup = false

	s.data = next
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// String keeps a record's log rendering free of anything but identifiers.
func (rec Intent) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lnurlcash intent %s/%s %d sats via %s", rec.State, rec.QuoteID, rec.AmountSats, rec.MintURL)
	return b.String()
}
