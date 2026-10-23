package spilman

// Durable per-channel balance ledger (payee side).
//
// This is the SpilmanHost shape MONAD uses at monad-relay/src/payments.rs,
// reduced to the two operations the hot path actually needs:
//
//   - AmountDue reads the stored balance (a local SQLite read, no network);
//   - RecordPayment writes (balance, signature) durably before returning.
//
// # Why CAS and not `new > stored`
//
// Gap analysis §6: the Go integration kit's shipped host uses a monotonic
// maximum (integration-kits/go/stores.go:382-383, `WHERE balance < ?`), which
// INTEGRATION.md:86-88 explicitly forbids. A monotonic max double-credits a
// *retry of a different payment that happens to carry the same balance*.
//
// The write here is a compare-and-swap against the exact previous balance in a
// single statement:
//
//	INSERT ... ON CONFLICT(channel_id) DO UPDATE SET ... WHERE stored.balance < excluded.balance
//
// Exactly one of two racing writers can win, and a replay (equal), a rollback
// (lower) or a same-balance/different-signature retry is refused with
// ErrBalanceNotAdvanced and no write and no payment-count bump.
//
// # Durability
//
// `journal_mode = WAL` + `synchronous = FULL`, one connection: a committed
// RecordPayment has reached stable storage before the caller is allowed to serve
// a single byte. Do not relax either for speed.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver: keeps this package CGO-free
)

// LedgerOutcome is the result of a durable ledger write.
type LedgerOutcome string

// LedgerOutcomeAccepted means the balance advanced from the exact previous value
// and is now durable.
const LedgerOutcomeAccepted LedgerOutcome = "accepted"

// StoredPayment is the latest durable (balance, signature) pair — the only thing
// that lets the payee claim the channel at settle time.
type StoredPayment struct {
	ChannelID    string
	Balance      uint64
	Signature    string
	PaymentCount uint64
	UpdatedAt    uint64
}

// ChannelRecord is the durable registration of a channel the payee serves.
type ChannelRecord struct {
	ChannelID      string
	Unit           string
	State          ChannelState
	Capacity       uint64
	Expiry         uint64
	OpenedAt       uint64
	ReceiverPubkey string
	MintURL        string
	KeysetID       string
	FundingTxID    string
	// UnilateralData is the payee's exit data, captured at close time. It is the
	// payee's only way to claim an uncooperative channel, so it is persisted.
	UnilateralData []byte
}

// ChannelLedger is the durable per-channel ledger. Safe for concurrent use.
type ChannelLedger struct {
	db *sql.DB
}

var memoryLedgerSeq atomic.Uint64

const ledgerSchema = `
CREATE TABLE IF NOT EXISTS spilman_channel_balance (
    channel_id    TEXT    PRIMARY KEY,
    balance       INTEGER NOT NULL,
    signature     TEXT    NOT NULL,
    payment_count INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS spilman_channel (
    channel_id      TEXT    PRIMARY KEY,
    unit            TEXT    NOT NULL,
    state           TEXT    NOT NULL,
    capacity        INTEGER NOT NULL,
    expiry          INTEGER NOT NULL,
    opened_at       INTEGER NOT NULL,
    receiver_pubkey TEXT    NOT NULL,
    mint_url        TEXT    NOT NULL,
    keyset_id       TEXT    NOT NULL,
    funding_tx_id   TEXT    NOT NULL,
    unilateral_data BLOB
) STRICT;
`

// ledgerDSNPragmas are the durability settings. They are part of the contract,
// not tuning: an uncommitted balance must never be served against.
const ledgerDSNPragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"

// OpenLedger opens (or creates) the durable ledger at path.
//
// FAIL CLOSED is the caller's job: an unreadable ledger must never be read as
// "no balance" — that would let a payer re-spend a channel.
func OpenLedger(path string) (*ChannelLedger, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("%w: create ledger dir %s: %v", ErrLedger, dir, err)
		}
	}
	return openLedgerDSN("file:" + path + "?" + ledgerDSNPragmas)
}

// OpenLedgerInMemory opens a ledger that does not survive the handle. Tests
// only: nothing here is durable, so it must never be used on a real payee.
func OpenLedgerInMemory() (*ChannelLedger, error) {
	name := fmt.Sprintf("spilman-mem-%d", memoryLedgerSeq.Add(1))
	dsn := "file:" + name + "?mode=memory&cache=shared&" + ledgerDSNPragmas
	return openLedgerDSN(dsn)
}

func openLedgerDSN(dsn string) (*ChannelLedger, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", ErrLedger, err)
	}
	// One connection: keeps the per-connection durability pragmas in force for
	// every statement and makes the ledger's ordering unambiguous.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(ledgerSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: init schema: %v", ErrLedger, err)
	}
	return &ChannelLedger{db: db}, nil
}

// Close releases the ledger.
func (l *ChannelLedger) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	return l.db.Close()
}

// LatestPayment returns the latest durable payment, or nil when the channel has
// none. nil is deliberately distinct from a zero balance: "no record" is not
// "zero balance".
func (l *ChannelLedger) LatestPayment(channelID string) (*StoredPayment, error) {
	row := l.db.QueryRow(
		`SELECT channel_id, balance, signature, payment_count, updated_at
		   FROM spilman_channel_balance WHERE channel_id = ?`, channelID)

	var p StoredPayment
	if err := row.Scan(&p.ChannelID, &p.Balance, &p.Signature, &p.PaymentCount, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read payment for %q: %v", ErrLedger, channelID, err)
	}
	return &p, nil
}

// Balance returns the stored balance and whether a record exists.
func (l *ChannelLedger) Balance(channelID string) (uint64, bool, error) {
	latest, err := l.LatestPayment(channelID)
	if err != nil {
		return 0, false, err
	}
	if latest == nil {
		return 0, false, nil
	}
	return latest.Balance, true, nil
}

// AmountDue returns the stored balance, or 0 when the channel has no record.
//
// The host contract calls this "amount due" and MONAD implements it as a plain
// read of the stored balance; keeping that shape means the payee's credit
// accounting (balance - served) stays a single subtraction in the caller.
func (l *ChannelLedger) AmountDue(channelID string) (uint64, error) {
	balance, _, err := l.Balance(channelID)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// PaymentCount returns the number of accepted payments for a channel.
func (l *ChannelLedger) PaymentCount(channelID string) (uint64, error) {
	latest, err := l.LatestPayment(channelID)
	if err != nil {
		return 0, err
	}
	if latest == nil {
		return 0, nil
	}
	return latest.PaymentCount, nil
}

// RecordPayment durably records a new balance, CAS'd against the exact previous
// value.
//
// Rejects any balance that does not strictly advance the stored one: that covers
// replay (equal), stale/rollback (lower) and the same-balance
// different-signature double-credit case. A rejected payment is not newly
// accepted funds, so the payment count does not advance either.
func (l *ChannelLedger) RecordPayment(channelID string, balance uint64, signature string) (LedgerOutcome, error) {
	if balance == 0 {
		// A zero-balance opening signature is legitimate on the wire, but it must
		// not be recorded as if it were progress.
		stored, _, err := l.Balance(channelID)
		if err != nil {
			return "", err
		}
		return "", &BalanceNotAdvancedError{ChannelID: channelID, Stored: stored, Offered: 0}
	}

	// One statement, so the guard and the write cannot be interleaved by another
	// writer: the CAS condition lives in the SQL, not in Go.
	res, err := l.db.Exec(
		`INSERT INTO spilman_channel_balance
		     (channel_id, balance, signature, payment_count, updated_at)
		 VALUES (?, ?, ?, 1, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		     balance       = excluded.balance,
		     signature     = excluded.signature,
		     payment_count = spilman_channel_balance.payment_count + 1,
		     updated_at    = excluded.updated_at
		 WHERE spilman_channel_balance.balance < excluded.balance`,
		channelID, balance, signature, nowSeconds())
	if err != nil {
		return "", fmt.Errorf("%w: record payment for %q: %v", ErrLedger, channelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("%w: record payment for %q: %v", ErrLedger, channelID, err)
	}
	if affected != 1 {
		// The CAS guard refused the write. Do not pretend it landed: report the
		// balance we actually hold so the caller can reconcile.
		stored, _, err := l.Balance(channelID)
		if err != nil {
			return "", err
		}
		return "", &BalanceNotAdvancedError{ChannelID: channelID, Stored: stored, Offered: balance}
	}
	return LedgerOutcomeAccepted, nil
}

// RegisterChannel durably registers a channel, refusing a duplicate ID.
func (l *ChannelLedger) RegisterChannel(ctx context.Context, rec ChannelRecord) error {
	res, err := l.db.ExecContext(ctx,
		`INSERT INTO spilman_channel
		     (channel_id, unit, state, capacity, expiry, opened_at,
		      receiver_pubkey, mint_url, keyset_id, funding_tx_id, unilateral_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT(channel_id) DO NOTHING`,
		rec.ChannelID, rec.Unit, string(rec.State), rec.Capacity, rec.Expiry, rec.OpenedAt,
		rec.ReceiverPubkey, rec.MintURL, rec.KeysetID, rec.FundingTxID)
	if err != nil {
		return fmt.Errorf("%w: register channel %q: %v", ErrLedger, rec.ChannelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: register channel %q: %v", ErrLedger, rec.ChannelID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrChannelExists, rec.ChannelID)
	}
	return nil
}

// Channel returns the durable channel registration, or nil when absent.
func (l *ChannelLedger) Channel(ctx context.Context, channelID string) (*ChannelRecord, error) {
	row := l.db.QueryRowContext(ctx,
		`SELECT channel_id, unit, state, capacity, expiry, opened_at,
		        receiver_pubkey, mint_url, keyset_id, funding_tx_id, unilateral_data
		   FROM spilman_channel WHERE channel_id = ?`, channelID)

	var rec ChannelRecord
	var state string
	var unilateral []byte
	if err := row.Scan(&rec.ChannelID, &rec.Unit, &state, &rec.Capacity, &rec.Expiry, &rec.OpenedAt,
		&rec.ReceiverPubkey, &rec.MintURL, &rec.KeysetID, &rec.FundingTxID, &unilateral); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read channel %q: %v", ErrLedger, channelID, err)
	}
	rec.State = ChannelState(state)
	rec.UnilateralData = unilateral
	return &rec, nil
}

// SetChannelState durably transitions a channel's state.
func (l *ChannelLedger) SetChannelState(ctx context.Context, channelID string, state ChannelState) error {
	res, err := l.db.ExecContext(ctx,
		`UPDATE spilman_channel SET state = ? WHERE channel_id = ?`, string(state), channelID)
	if err != nil {
		return fmt.Errorf("%w: set state of %q: %v", ErrLedger, channelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: set state of %q: %v", ErrLedger, channelID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrChannelUnknown, channelID)
	}
	return nil
}

// SetUnilateralCloseData durably stores the payee's unilateral-exit data.
func (l *ChannelLedger) SetUnilateralCloseData(ctx context.Context, channelID string, data []byte) error {
	res, err := l.db.ExecContext(ctx,
		`UPDATE spilman_channel SET unilateral_data = ? WHERE channel_id = ?`, data, channelID)
	if err != nil {
		return fmt.Errorf("%w: store unilateral data for %q: %v", ErrLedger, channelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: store unilateral data for %q: %v", ErrLedger, channelID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrChannelUnknown, channelID)
	}
	return nil
}

func nowSeconds() uint64 { return uint64(time.Now().Unix()) }
