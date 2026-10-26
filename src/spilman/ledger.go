package spilman

// Durable per-channel balance ledger (payee side).
//
// This is the SpilmanHost shape MONAD uses at monad-relay/src/payments.rs,
// reduced to the two operations the hot path actually needs:
//
//   - AmountDue reads the stored balance (a local SQLite read, no network);
//   - RecordPayment writes (balance, signature) durably before returning.
//
// # The advance guard: strictly-advancing, not compare-and-swap
//
// Gap analysis §6: the Go integration kit's shipped host uses a monotonic
// maximum (integration-kits/go/stores.go:382-383, `WHERE balance < ?`), which
// INTEGRATION.md:86-88 explicitly forbids. What this package does instead is a
// SINGLE-STATEMENT strict-advance guard with authentication in front of it:
//
//	INSERT ... ON CONFLICT(channel_id) DO UPDATE SET ... WHERE stored.balance < excluded.balance
//
// Be precise about what that predicate is and is not. It is NOT a
// compare-and-swap against a caller-supplied "expected previous" balance: the
// caller never supplies one, and `expected` appears nowhere in the SQL. It is a
// strict-advance condition — the write lands only if the offered balance is
// strictly GREATER than the stored one. Consequences, stated plainly:
//
//   - a replay (equal), a rollback (lower) and a same-balance/different-signature
//     retry are refused with ErrBalanceNotAdvanced, with no write and no
//     payment-count bump;
//   - exactly one of two racing writers can win, because the guard and the write
//     are the same statement and no other writer can interleave inside it;
//   - BUT a balance that SKIPS AHEAD (offered > stored by more than one step) is
//     accepted. That is deliberate for this rail: a balance signed by the payer
//     is self-authenticating for the amount it names, and a higher balance is
//     never worse for the seller than the stored one. It does mean this is a
//     monotonic guard, not a CAS, and any comment, test name or PR text that
//     calls it a CAS against the exact previous balance is overstating it.
//
// Authenticity is NOT the guard's job and must never be inferred from it: the
// guard only enforces advance. See step 2 in RecordPayment for the check that
// stops a forged balance, and note that step 2 is ordered BEFORE this write.
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

// LedgerOutcomeAccepted means the balance strictly advanced past the stored value
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
//
// The funding record (ParamsJSON, FundingProofsJSON, ChannelSecretHex,
// KeysetInfoJSON) is not bookkeeping: it is the only thing a payment signature
// can be checked against. A channel registered without it cannot authenticate
// anything, so RecordPayment refuses it rather than crediting an unverifiable
// balance.
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
	// ParamsJSON, FundingProofsJSON, ChannelSecretHex and KeysetInfoJSON are the
	// channel's funding record, captured at registration. Upstream's validator
	// (SpilmanBridge::validate_payment) rebuilds the signed commitment from
	// exactly these four values, so they are persisted verbatim.
	ParamsJSON        string
	FundingProofsJSON string
	ChannelSecretHex  string
	KeysetInfoJSON    string
	// UnilateralData is the payee's exit data, captured at close time. It is the
	// payee's only way to claim an uncooperative channel, so it is persisted.
	UnilateralData []byte
}

// FundingRecord is the subset of a channel registration a payment signature is
// checked against. It is returned as a value so a caller cannot mutate the
// record the ledger will verify the next payment against.
type FundingRecord struct {
	ChannelID         string
	ParamsJSON        string
	FundingProofsJSON string
	ChannelSecretHex  string
	KeysetInfoJSON    string
}

// Complete reports whether the funding record carries everything the upstream
// validator needs. A partial record can only ever fail verification, so callers
// refuse it up front with a distinct error instead of a confusing signature
// failure.
func (f FundingRecord) Complete() bool {
	return f.ParamsJSON != "" && f.FundingProofsJSON != "" && f.ChannelSecretHex != "" && f.KeysetInfoJSON != ""
}

// PaymentValidator authenticates a signed balance update against the channel's
// persisted funding record.
//
// The only production implementation is the upstream cdk-spilman validator
// (SpilmanBridge::validate_payment), reached through the build-tag seam in
// rail_cgo.go / rail_stub.go. The stub REFUSES: the default CGO_ENABLED=0 build
// carries no verifier, and a payee that cannot authenticate a balance must not
// credit it.
type PaymentValidator interface {
	// Name identifies the validator, or "not-compiled" for the stub.
	Name() string
	// ValidatePayment returns nil only when the signature is genuine for that
	// channel, that balance, and that funding record. Every other outcome is an
	// error, and every error is fatal to the payment.
	ValidatePayment(ctx context.Context, funding FundingRecord, balance uint64, signature string) error
}

// ChannelLedger is the durable per-channel ledger. Safe for concurrent use.
type ChannelLedger struct {
	db        *sql.DB
	validator PaymentValidator
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
    params_json        TEXT NOT NULL DEFAULT '',
    funding_proofs_json TEXT NOT NULL DEFAULT '',
    channel_secret_hex  TEXT NOT NULL DEFAULT '',
    keyset_info_json    TEXT NOT NULL DEFAULT '',
    unilateral_data BLOB
) STRICT;
`

// ledgerFundingColumns are added to a pre-existing spilman_channel table so a
// ledger created before the funding record existed keeps opening. A channel row
// that predates them has an empty funding record and is therefore unverifiable:
// RecordPayment refuses it (fail closed) until it is re-registered.
var ledgerFundingColumns = []string{
	"params_json",
	"funding_proofs_json",
	"channel_secret_hex",
	"keyset_info_json",
}

// ledgerDSNPragmas are the durability settings. They are part of the contract,
// not tuning: an uncommitted balance must never be served against.
const ledgerDSNPragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)"

// OpenLedger opens (or creates) the durable ledger at path, authenticating
// payments with this build's validator (upstream cdk-spilman under -tags
// spilman, a refusal in the default build).
//
// FAIL CLOSED is the caller's job: an unreadable ledger must never be read as
// "no balance" — that would let a payer re-spend a channel.
func OpenLedger(path string) (*ChannelLedger, error) {
	return OpenLedgerWithValidator(path, newProtocolValidator())
}

// OpenLedgerWithValidator opens (or creates) the durable ledger at path with an
// explicit validator. A nil validator is refused: a ledger that cannot
// authenticate a payment must not be constructible.
func OpenLedgerWithValidator(path string, validator PaymentValidator) (*ChannelLedger, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("%w: create ledger dir %s: %v", ErrLedger, dir, err)
		}
	}
	return openLedgerDSN("file:"+path+"?"+ledgerDSNPragmas, validator)
}

// OpenLedgerInMemory opens a ledger that does not survive the handle. Tests
// only: nothing here is durable, so it must never be used on a real payee.
func OpenLedgerInMemory() (*ChannelLedger, error) {
	return OpenLedgerInMemoryWithValidator(newProtocolValidator())
}

// OpenLedgerInMemoryWithValidator opens an in-memory ledger that authenticates
// payments with validator. Tests only.
func OpenLedgerInMemoryWithValidator(validator PaymentValidator) (*ChannelLedger, error) {
	if validator == nil {
		return nil, fmt.Errorf("%w: nil payment validator", ErrLedger)
	}
	name := fmt.Sprintf("spilman-mem-%d", memoryLedgerSeq.Add(1))
	dsn := "file:" + name + "?mode=memory&cache=shared&" + ledgerDSNPragmas
	return openLedgerDSN(dsn, validator)
}

func openLedgerDSN(dsn string, validator PaymentValidator) (*ChannelLedger, error) {
	if validator == nil {
		// No validator means no way to tell a payment from a forgery. Refuse to
		// hand back a ledger that would credit anything it is given.
		return nil, fmt.Errorf("%w: nil payment validator", ErrLedger)
	}
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
	if err := migrateLedger(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &ChannelLedger{db: db, validator: validator}, nil
}

// migrateLedger adds the funding-record columns to a spilman_channel table that
// predates them. It is additive only: it never rewrites or drops a row, so an
// existing balance history survives the upgrade untouched.
func migrateLedger(db *sql.DB) error {
	present := make(map[string]bool, len(ledgerFundingColumns))
	rows, err := db.Query(`SELECT name FROM pragma_table_info('spilman_channel')`)
	if err != nil {
		return fmt.Errorf("%w: inspect channel schema: %v", ErrLedger, err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: inspect channel schema: %v", ErrLedger, err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%w: inspect channel schema: %v", ErrLedger, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("%w: inspect channel schema: %v", ErrLedger, err)
	}

	for _, column := range ledgerFundingColumns {
		if present[column] {
			continue
		}
		// The column name comes from the fixed list above, never from input.
		if _, err := db.Exec(`ALTER TABLE spilman_channel ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("%w: add column %s: %v", ErrLedger, column, err)
		}
	}
	return nil
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

// RecordPayment authenticates a payment and, only if it is genuine, durably
// records the new balance behind a strict-advance guard, not a CAS against an
// expected previous value.
//
// Order is AUTHENTICATE -> PERSIST, and the order is the point:
//
//  1. the channel's funding record is loaded; an absent record is refused
//     (ErrChannelUnknown — fail closed);
//  2. the signature is checked with the upstream validator against that exact
//     funding record, and ANY error is fatal (ErrPaymentNotAuthentic). A
//     refused payment writes nothing;
//  3. only then does the single-statement advance guard run.
//
// Step 2 is what stops a FORGED balance: the guard alone only enforces
// monotonicity, which is not authenticity — without this check, a higher balance
// was accepted from anyone, for any channel, with any signature.
//
// Step 3's guard additionally rejects a balance that does not strictly advance
// the stored one: replay (equal), stale/rollback (lower) and the same-balance
// different-signature double-credit case. A rejected payment is not newly
// accepted funds, so the payment count does not advance either.
func (l *ChannelLedger) RecordPayment(ctx context.Context, channelID string, balance uint64, signature string) (LedgerOutcome, error) {
	if l == nil || l.db == nil {
		return "", fmt.Errorf("%w: nil ledger", ErrLedger)
	}
	if l.validator == nil {
		// Defensive: openLedgerDSN refuses a nil validator, so reaching here
		// means the struct was built by hand. Still fail closed.
		return "", fmt.Errorf("%w: ledger has no payment validator", ErrLedger)
	}

	// 1. The funding record is the only thing a signature can be checked
	//    against. Without it the balance is unverifiable, so refuse.
	funding, err := l.FundingRecord(ctx, channelID)
	if err != nil {
		return "", err
	}
	if funding == nil {
		return "", fmt.Errorf("%w: %q", ErrChannelUnknown, channelID)
	}
	if !funding.Complete() {
		return "", fmt.Errorf("%w: %q has no funding record to verify against", ErrFundingRecordMissing, channelID)
	}

	// 2. AUTHENTICATE. Every error, including a transport or crypto error inside
	//    the validator, means the payment is not proven genuine.
	if err := l.validator.ValidatePayment(ctx, *funding, balance, signature); err != nil {
		// Both the refusal sentinel and the validator's own cause are wrapped, so
		// a caller can tell "not authentic" from "this build cannot verify"
		// without parsing the message.
		return "", fmt.Errorf("%w: channel %q balance %d: %w", ErrPaymentNotAuthentic, channelID, balance, err)
	}

	return l.recordAuthenticatedPayment(channelID, balance, signature)
}

// recordAuthenticatedPayment is the durable advance-guard half of RecordPayment. It MUST
// NOT be reachable from a caller that has not authenticated the payment, which
// is why it is unexported and why its only production caller is the
// authenticate-then-persist sequence above.
//
// It exists as a separate function so the package's own tests can exercise the
// guard arithmetic (advance / replay / rollback / racing writers) without minting a
// signature for every case. That is the Go equivalent of the Rust rail's
// non-default `test-support` feature: `#[cfg(test)]` items are invisible to a
// separate tests/ crate there, while a same-package Go test can call an
// unexported function, so no build tag or exported test hook is needed here.
func (l *ChannelLedger) recordAuthenticatedPayment(channelID string, balance uint64, signature string) (LedgerOutcome, error) {
	if balance == 0 {
		// A zero-balance opening signature is legitimate on the wire, but it must
		// not be recorded as if it were progress.
		stored, _, err := l.Balance(channelID)
		if err != nil {
			return "", err
		}
		return "", &BalanceNotAdvancedError{ChannelID: channelID, Stored: stored, Offered: 0}
	}

	// 3. PERSIST. One statement, so the guard and the write cannot be
	//    interleaved by another writer: the advance condition lives in the SQL, not
	//    in Go.
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
		// The advance guard refused the write. Do not pretend it landed: report the
		// balance we actually hold so the caller can reconcile.
		stored, _, err := l.Balance(channelID)
		if err != nil {
			return "", err
		}
		return "", &BalanceNotAdvancedError{ChannelID: channelID, Stored: stored, Offered: balance}
	}
	return LedgerOutcomeAccepted, nil
}

// FundingRecord returns the channel's persisted funding record, or nil when the
// channel is not registered.
func (l *ChannelLedger) FundingRecord(ctx context.Context, channelID string) (*FundingRecord, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("%w: nil ledger", ErrLedger)
	}
	row := l.db.QueryRowContext(ctx,
		`SELECT channel_id, params_json, funding_proofs_json, channel_secret_hex, keyset_info_json
		   FROM spilman_channel WHERE channel_id = ?`, channelID)

	var rec FundingRecord
	if err := row.Scan(&rec.ChannelID, &rec.ParamsJSON, &rec.FundingProofsJSON,
		&rec.ChannelSecretHex, &rec.KeysetInfoJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read funding record for %q: %v", ErrLedger, channelID, err)
	}
	return &rec, nil
}

// SetFundingRecord durably attaches a funding record to an already-registered
// channel. It is the recovery path for a channel registered before the record
// was persisted (see migrateLedger) and for a caller that registers the channel
// and the funding data in two steps.
//
// It refuses to OVERWRITE a different funding record for the same channel id:
// the record is what every later signature is verified against, so silently
// swapping it would let a registrant re-anchor a channel to funding proofs it
// does not actually hold. Re-registering the identical record is idempotent.
func (l *ChannelLedger) SetFundingRecord(ctx context.Context, rec FundingRecord) error {
	if !rec.Complete() {
		return fmt.Errorf("%w: %q funding record is incomplete", ErrFundingRecordMissing, rec.ChannelID)
	}
	if l == nil || l.db == nil {
		return fmt.Errorf("%w: nil ledger", ErrLedger)
	}

	existing, err := l.FundingRecord(ctx, rec.ChannelID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("%w: %q", ErrChannelUnknown, rec.ChannelID)
	}
	if existing.Complete() {
		if sameFundingRecord(*existing, rec) {
			return nil
		}
		return fmt.Errorf("%w: %q already has a different funding record", ErrChannelConflict, rec.ChannelID)
	}

	res, err := l.db.ExecContext(ctx,
		`UPDATE spilman_channel
		    SET params_json = ?, funding_proofs_json = ?, channel_secret_hex = ?, keyset_info_json = ?
		  WHERE channel_id = ?
		    AND (params_json = '' OR funding_proofs_json = ''
		         OR channel_secret_hex = '' OR keyset_info_json = '')`,
		rec.ParamsJSON, rec.FundingProofsJSON, rec.ChannelSecretHex, rec.KeysetInfoJSON, rec.ChannelID)
	if err != nil {
		return fmt.Errorf("%w: store funding record for %q: %v", ErrLedger, rec.ChannelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: store funding record for %q: %v", ErrLedger, rec.ChannelID, err)
	}
	if affected != 1 {
		// Someone wrote a record between our read and our write. Re-read and
		// compare rather than reporting a write that did not happen.
		existing, err := l.FundingRecord(ctx, rec.ChannelID)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: %q", ErrChannelUnknown, rec.ChannelID)
		}
		if sameFundingRecord(*existing, rec) {
			return nil
		}
		return fmt.Errorf("%w: %q already has a different funding record", ErrChannelConflict, rec.ChannelID)
	}
	return nil
}

// sameFundingRecord compares two funding records field by field.
func sameFundingRecord(a, b FundingRecord) bool {
	return a.ParamsJSON == b.ParamsJSON &&
		a.FundingProofsJSON == b.FundingProofsJSON &&
		a.ChannelSecretHex == b.ChannelSecretHex &&
		a.KeysetInfoJSON == b.KeysetInfoJSON
}

// RegisterChannel durably registers a channel and its funding record.
//
// A duplicate ID is refused. Registered with the SAME funding record it is
// refused too — re-registration is a caller bug, not a resync. Registered with
// a DIFFERENT funding record for the same channel id it is refused with
// ErrChannelExists: the funding record anchors every later signature, so
// accepting a second, different one would let the registrant re-point the
// channel at funding proofs the payee never agreed to serve against.
func (l *ChannelLedger) RegisterChannel(ctx context.Context, rec ChannelRecord) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("%w: nil ledger", ErrLedger)
	}
	res, err := l.db.ExecContext(ctx,
		`INSERT INTO spilman_channel
		     (channel_id, unit, state, capacity, expiry, opened_at,
		      receiver_pubkey, mint_url, keyset_id, funding_tx_id,
		      params_json, funding_proofs_json, channel_secret_hex, keyset_info_json,
		      unilateral_data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT(channel_id) DO NOTHING`,
		rec.ChannelID, rec.Unit, string(rec.State), rec.Capacity, rec.Expiry, rec.OpenedAt,
		rec.ReceiverPubkey, rec.MintURL, rec.KeysetID, rec.FundingTxID,
		rec.ParamsJSON, rec.FundingProofsJSON, rec.ChannelSecretHex, rec.KeysetInfoJSON)
	if err != nil {
		return fmt.Errorf("%w: register channel %q: %v", ErrLedger, rec.ChannelID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: register channel %q: %v", ErrLedger, rec.ChannelID, err)
	}
	if affected == 0 {
		// Distinguish "already registered" from "already registered with a
		// conflicting funding record" only in the message: both are refusals and
		// both leave the existing row untouched.
		existing, err := l.FundingRecord(ctx, rec.ChannelID)
		if err != nil {
			return err
		}
		if existing != nil && existing.Complete() && !sameFundingRecord(*existing, fundingRecordOf(rec)) {
			return fmt.Errorf("%w: %q is registered with a different funding record", ErrChannelConflict, rec.ChannelID)
		}
		return fmt.Errorf("%w: %q", ErrChannelExists, rec.ChannelID)
	}
	return nil
}

// fundingRecordOf projects the funding-record fields out of a registration.
func fundingRecordOf(rec ChannelRecord) FundingRecord {
	return FundingRecord{
		ChannelID:         rec.ChannelID,
		ParamsJSON:        rec.ParamsJSON,
		FundingProofsJSON: rec.FundingProofsJSON,
		ChannelSecretHex:  rec.ChannelSecretHex,
		KeysetInfoJSON:    rec.KeysetInfoJSON,
	}
}

// Channel returns the durable channel registration, or nil when absent.
func (l *ChannelLedger) Channel(ctx context.Context, channelID string) (*ChannelRecord, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("%w: nil ledger", ErrLedger)
	}
	row := l.db.QueryRowContext(ctx,
		`SELECT channel_id, unit, state, capacity, expiry, opened_at,
		        receiver_pubkey, mint_url, keyset_id, funding_tx_id,
		        params_json, funding_proofs_json, channel_secret_hex, keyset_info_json,
		        unilateral_data
		   FROM spilman_channel WHERE channel_id = ?`, channelID)

	var rec ChannelRecord
	var state string
	var unilateral []byte
	if err := row.Scan(&rec.ChannelID, &rec.Unit, &state, &rec.Capacity, &rec.Expiry, &rec.OpenedAt,
		&rec.ReceiverPubkey, &rec.MintURL, &rec.KeysetID, &rec.FundingTxID,
		&rec.ParamsJSON, &rec.FundingProofsJSON, &rec.ChannelSecretHex, &rec.KeysetInfoJSON,
		&unilateral); err != nil {
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
