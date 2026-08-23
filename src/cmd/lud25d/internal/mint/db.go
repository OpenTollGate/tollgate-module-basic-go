package mint

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Note status constants.
const (
	NotePending = "pending"
	NotePaid    = "paid"
	NoteSpent   = "spent"
	NoteExpired = "expired"
)

// ErrNoteNotFound is returned when a note lookup fails.
var ErrNoteNotFound = errors.New("note not found")

// Note represents a bearer note row in the SQLite database.
type Note struct {
	K1          string // CSPRNG bearer secret (hex-encoded 32 bytes), NOT Lightning preimage
	PaymentHash string // Lightning invoice payment hash (proof of payment)
	QuoteID     string // Cashu mint NUT-04 quote ID
	AmountSat   int64  // amount in whole satoshis (mirrors the NUT-04 quote)
	Status      string // pending|paid|spent|expired
	CreatedAt   int64  // unix timestamp
	PaidAt      *int64 // unix timestamp, nil if unpaid
	SpentAt     *int64 // unix timestamp, nil if unspent
	ExpiresAt   int64  // unix timestamp
}

// DB wraps a SQLite connection for the notes table.
type DB struct {
	db *sql.DB
}

// schemaSQL is the notes table DDL.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS notes (
  k1 TEXT PRIMARY KEY,
  payment_hash TEXT NOT NULL,
  quote_id TEXT NOT NULL,
  amount_sat INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL,
  paid_at INTEGER,
  spent_at INTEGER,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notes_payment_hash ON notes(payment_hash);
CREATE INDEX IF NOT EXISTS idx_notes_status ON notes(status);
CREATE INDEX IF NOT EXISTS idx_notes_expires_at ON notes(expires_at);
`

// OpenDB opens (or creates) a SQLite database at the given path and
// initialises the notes schema.
func OpenDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// InsertNote stores a new note. Returns an error if a note with the
// same k1 already exists.
func (d *DB) InsertNote(n Note) error {
	_, err := d.db.Exec(
		`INSERT INTO notes (k1, payment_hash, quote_id, amount_sat, status, created_at, paid_at, spent_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.K1, n.PaymentHash, n.QuoteID, n.AmountSat, n.Status,
		n.CreatedAt, nilInt64(n.PaidAt), nilInt64(n.SpentAt), n.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert note: %w", err)
	}
	return nil
}

// GetNote retrieves a note by its k1. Returns ErrNoteNotFound if no
// row matches.
func (d *DB) GetNote(k1 string) (*Note, error) {
	row := d.db.QueryRow(
		`SELECT k1, payment_hash, quote_id, amount_sat, status, created_at, paid_at, spent_at, expires_at
		 FROM notes WHERE k1 = ?`, k1,
	)

	var n Note
	var paidAt, spentAt sql.NullInt64
	err := row.Scan(
		&n.K1, &n.PaymentHash, &n.QuoteID, &n.AmountSat, &n.Status,
		&n.CreatedAt, &paidAt, &spentAt, &n.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get note: %w", err)
	}

	n.PaidAt = int64Ptr(paidAt)
	n.SpentAt = int64Ptr(spentAt)
	return &n, nil
}

// MarkPaid updates a note's status to paid and sets paid_at.
func (d *DB) MarkPaid(k1 string, paidAt int64) error {
	res, err := d.db.Exec(
		`UPDATE notes SET status = ?, paid_at = ? WHERE k1 = ?`,
		NotePaid, paidAt, k1,
	)
	if err != nil {
		return fmt.Errorf("mark paid: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNoteNotFound
	}
	return nil
}

// MarkSpent updates a note's status to spent and sets spent_at.
func (d *DB) MarkSpent(k1 string, spentAt int64) error {
	res, err := d.db.Exec(
		`UPDATE notes SET status = ?, spent_at = ? WHERE k1 = ?`,
		NoteSpent, spentAt, k1,
	)
	if err != nil {
		return fmt.Errorf("mark spent: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNoteNotFound
	}
	return nil
}

// MarkExpired updates a note's status to expired.
func (d *DB) MarkExpired(k1 string) error {
	res, err := d.db.Exec(
		`UPDATE notes SET status = ? WHERE k1 = ? AND status = ?`,
		NoteExpired, k1, NotePending,
	)
	if err != nil {
		return fmt.Errorf("mark expired: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNoteNotFound
	}
	return nil
}

// --- helpers ---

func nilInt64(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	val := v.Int64
	return &val
}

// expiryDurationToUnix converts a time.Duration to a unix timestamp
// representing now + duration.
func expiryDurationToUnix(d time.Duration) int64 {
	return time.Now().Add(d).Unix()
}
