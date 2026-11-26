package merchant

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// The LNURLcash redemption rail, merchant side.
//
// Shape of the transaction, and why it is this shape:
//
//	note → our own bolt11 invoice (a Cashu mint quote) → issuer melt → existing
//	quote monitor → existing grant
//
// The gate is not a Lightning node and cannot receive a melt directly. It CAN ask
// an accepted mint for a mint quote, which hands it a bolt11 invoice for a known
// amount; paying that invoice credits a quote the existing machinery already
// monitors and grants for. So this rail does not invent a payment path — it makes
// the ISSUER pay the invoice the Cashu path already trusts.
//
// That fixes what "confirmed redemption" means here: not the issuer's `OK`, and
// never a certificate. The grant happens where it always happens, when a mint
// quote for our own invoice reaches PAID and the tokens are issued.
//
// What the journal adds, and why it is a separate record from the quote:
//
//   - the note's identity is durable BEFORE the melt leaves the router, so a
//     restart mid-melt reconciles instead of re-presenting a spend that may
//     already have been consumed;
//   - a second presentation of the same note is refused by a durable record, not
//     by an in-memory set a restart erases;
//   - the binding (note → our invoice) is immutable, so a later request cannot
//     re-point a paid note at a different invoice.

// ErrLNURLCashRailClosed is returned when the rail has no durable journal. It is a
// refusal rather than a degraded mode: without the journal the rail cannot tell a
// replay from a new note, and telling those apart is the whole safety property.
var ErrLNURLCashRailClosed = errors.New("lnurlcash rail: no durable journal available")

// ErrLNURLCashNoteAlreadyRedeemed is the answer to a replay. It is deliberately
// the same whether the first attempt succeeded, failed to confirm, or is still in
// flight: an attacker probing the boundary learns nothing, and a customer who
// double-taps gets an answer that tells them to look at their existing session.
var ErrLNURLCashNoteAlreadyRedeemed = errors.New("lnurlcash rail: this note has already been presented")

// ErrLNURLCashRetriesExhausted reports that this note has already failed as many
// times as the rail will allow.
//
// The retry budget exists because every attempt costs a permanent journal record and
// the journal has no pruner: at its cap, Prepare refuses for everyone. Bounding the
// attempts per note keeps one unlucky (or hostile) note from disabling redemption for
// the whole gate. A note that reaches this bound needs an operator, not another try.
var ErrLNURLCashRetriesExhausted = errors.New("lnurlcash rail: this note has exhausted its redemption attempts")

// maxRedeemAttemptsPerNote bounds how many times one note may be retried after a
// definitive, pre-issuer failure. Each attempt consumes one journal record that nothing
// ever reclaims, so this is the difference between a bad note and a gate that can no
// longer record any payment at all.
const maxRedeemAttemptsPerNote = 8

// IssuerCallback is the redemption endpoint the issuer published for a note,
// validated against the note's own domain. It is a struct, not a *url.URL, so a
// caller cannot mutate a URL that has already been checked.
type IssuerCallback struct {
	Host string
	Path string
}

// LNURLCashRail coordinates one redemption. It holds no wallet of its own:
// invoicing and melting are injected, so the rail is testable without a mint, and
// the grant stays in the existing quote monitor.
type LNURLCashRail struct {
	store       *paymentjournal.Store
	invoice     func(macAddress, mintURL string, sats uint64) (quoteID, bolt11 string, err error)
	melt        func(note lnurlcash.Note, callback IssuerCallback, invoice string) error
	maxNoteSats uint64

	mu     sync.Mutex
	byNote map[string]struct{}
}

// NewLNURLCashRail wires the rail. A nil store, invoice or melt leaves it closed
// rather than half-working.
func NewLNURLCashRail(
	store *paymentjournal.Store,
	maxNoteSats uint64,
	invoice func(macAddress, mintURL string, sats uint64) (string, string, error),
	melt func(note lnurlcash.Note, callback IssuerCallback, invoice string) error,
) *LNURLCashRail {
	return &LNURLCashRail{
		store:       store,
		invoice:     invoice,
		melt:        melt,
		maxNoteSats: maxNoteSats,
		byNote:      make(map[string]struct{}),
	}
}

// Open reports whether the rail can serve a redemption.
func (r *LNURLCashRail) Open() bool {
	return r != nil && r.store != nil && r.invoice != nil && r.melt != nil
}

// noteFingerprint is the journal's identifier for a note: sha256 of the spend.
//
// A fingerprint, not the spend — but note what the journal's file is, then: a
// bearer note is spent by revealing its preimage, so anyone who can read this
// file can spend every note it names. The journal's owner-only 0600 file is the
// confidentiality boundary, exactly as its README says, and this rail does not
// add a second copy of the secret anywhere.
func noteFingerprint(spend string) string {
	sum := sha256.Sum256([]byte(spend))
	return hex.EncodeToString(sum[:])
}

// Redeem presents a note for one client, with the value the ISSUER confirmed (the
// caller obtains it from lnurlcash.Client.Info; a note's own claimed amount, and
// any `cs1`, are not inputs here).
//
// It returns once the issuer has accepted the melt and the note is bound to a
// quote. It does not grant: the quote monitor grants when the mint quote settles,
// the same path every Lightning purchase uses.
//
// Failure classes, deliberately distinct:
//
//   - a local refusal (bad note, replay, over ceiling) means nothing left the
//     router and the note is untouched;
//   - an ambiguous issuer failure means the note may be burned. The journal record
//     stays `unknown`, a retry is refused, and the customer is told to wait for
//     reconciliation rather than re-present;
//   - a definitive issuer refusal means the note is worthless here: the record is
//     marked failed and the note may be presented again.
//
// walletKeyForMint derives a journal-safe WalletID from a mint URL.
//
// paymentjournal.validID permits only [A-Za-z0-9-_.:@] — deliberately, since an id
// reaches a filename-shaped key and a JSON object key. A mint URL contains "/" (and
// possibly a query), so it CANNOT be used verbatim: every Prepare would be refused
// and the rail would refuse every payment while looking wired-up. That is exactly
// the shape of bug this rail exists to avoid, so the conversion is explicit and
// tested rather than inline.
//
// The key is the canonical mint URL's sha256, prefixed so a human reading the
// journal can tell where it came from. Canonicalisation goes through
// tollwallet.NormalizeMintURL so two spellings of one mint cannot split a
// customer's history across two wallet keys (#375, #480).
func walletKeyForMint(mintURL string) string {
	canonical := tollwallet.NormalizeMintURL(mintURL)
	sum := sha256.Sum256([]byte(canonical))
	return "mint-" + hex.EncodeToString(sum[:])
}

func (r *LNURLCashRail) Redeem(note lnurlcash.Note, confirmedMSat uint64, macAddress, mintURL string) (quoteID string, err error) {
	return r.RedeemAt(note, confirmedMSat, macAddress, mintURL, "")
}

// RedeemAt is Redeem with the issuer's own callback path.
//
// LUD-03/LUD-25 let the withdraw response name a callback endpoint that differs from the
// informational URL. Using the note's path unconditionally meant that a conforming issuer
// which does so would 404 every melt: the note is then reserved but never redeemed, which
// is a burned-in-limbo failure with no recovery. callbackPath comes from IssuerState,
// which already validated the host, the scheme and that it shares the note's domain — so
// this adds no trust, it stops discarding an answer we paid for.
//
// An empty callbackPath keeps the old behaviour (the note's own path), which is what a
// bare k1 note with no issuer answer gets.
func (r *LNURLCashRail) RedeemAt(note lnurlcash.Note, confirmedMSat uint64, macAddress, mintURL, callbackPath string) (quoteID string, err error) {
	if !r.Open() {
		return "", ErrLNURLCashRailClosed
	}
	if confirmedMSat == 0 {
		return "", fmt.Errorf("lnurlcash rail: the issuer confirmed no value for this note")
	}
	sats := lnurlcash.WithdrawableSats(confirmedMSat)
	if sats == 0 {
		return "", fmt.Errorf("lnurlcash rail: the note is worth less than one satoshi")
	}
	if r.maxNoteSats != 0 && sats > r.maxNoteSats {
		return "", fmt.Errorf("lnurlcash rail: note is %d sats, above the configured ceiling of %d", sats, r.maxNoteSats)
	}

	fingerprint := noteFingerprint(note.Spend())

	// In-memory reservation first: two concurrent requests for one note must not
	// both reach the journal. The durable half of the same guard is the journal
	// lookup below plus its one-hash-one-operation rule.
	r.mu.Lock()
	if _, seen := r.byNote[fingerprint]; seen {
		r.mu.Unlock()
		return "", ErrLNURLCashNoteAlreadyRedeemed
	}
	r.byNote[fingerprint] = struct{}{}
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		delete(r.byNote, fingerprint)
		r.mu.Unlock()
	}

	// Durable history: a note that has been presented before is refused unless
	// every prior attempt ended in a definitive failure. This survives restart,
	// which the in-memory set does not.
	prior, err := r.priorAttempts(fingerprint)
	if err != nil {
		release()
		return "", err
	}
	for _, op := range prior {
		if op.State != paymentjournal.Failed {
			// Includes `unknown` and `paid`: the note may be burned or already
			// honoured. Both mean "do not touch it again".
			log.Printf("lnurlcash rail: refusing a re-presented note (issuer %s) — prior operation %s is %s", note.Domain, op.ID, op.State)
			return "", ErrLNURLCashNoteAlreadyRedeemed
		}
	}

	opID := "lnurlcash-" + fingerprint[:32]
	if len(prior) > 0 {
		// A definitive failure frees the note, but the journal has no
		// failed→submitted transition (by design). A fresh, distinct id for the
		// next attempt keeps the history and the audit trail intact.
		//
		// BOUNDED ON PURPOSE. Each retry costs one permanent journal slot, and the
		// journal has no pruner: at its cap, Prepare refuses for everyone, so an
		// unbounded retry loop lets one caller disable redemption for the whole gate
		// (the endpoint rejects unaccepted mints before we get here, but a mint that
		// is accepted and merely down reaches this path through no fault of the
		// caller). A note that has failed this many times is not going to work on the
		// next try; it needs a human, and refusing is honest about that.
		if len(prior) >= maxRedeemAttemptsPerNote {
			log.Printf("lnurlcash rail: refusing a note that has already failed %d times (issuer %s) — retries are bounded so one note cannot exhaust the payment journal",
				len(prior), note.Domain)
			return "", ErrLNURLCashRetriesExhausted
		}
		opID += "-r" + strconv.Itoa(len(prior)+1)
	}

	cp1, err := note.CP1()
	if err != nil {
		release()
		return "", err
	}

	// The record carries the note's cp1 (which names the note but cannot spend it)
	// and the issuer domain. It deliberately does NOT carry the spend: the
	// fingerprint identifies the note to a reconciler, and the spend is only ever
	// in the request the issuer receives.
	op, err := r.store.Prepare(paymentjournal.Operation{
		ID:         opID,
		WalletID:   walletKeyForMint(mintURL),
		Direction:  paymentjournal.Incoming,
		AmountMSat: confirmedMSat,
		Recipient:  note.Domain,
		Request:    []byte(cp1),
	}, paymentjournal.Budget{})
	if err != nil {
		release()
		return "", fmt.Errorf("lnurlcash rail: cannot record the note's intent: %w", err)
	}

	// Our invoice, for the confirmed amount. From here the note is payable.
	issuedQuote, bolt11, err := r.invoice(macAddress, mintURL, sats)
	if err != nil || issuedQuote == "" || bolt11 == "" {
		release()
		// An empty quote id or invoice is NOT a success: the endpoint reports status:1
		// with whatever quote id it is handed, so an invoice function that "succeeds"
		// empty would tell the customer they are paid with nothing behind it. Treat it
		// as the pre-issuer failure it is — nothing has been sent to the issuer, so the
		// note is still free to try again.
		if err == nil {
			err = fmt.Errorf("lnurlcash rail: the invoice step returned no quote")
		}
		// Nothing was spent. Marking the record failed frees the note for a later
		// attempt: a mint that is down is not the customer's fault, and the
		// issuer was never contacted.
		if _, aerr := r.store.Advance(op.ID, op.Version, paymentjournal.Failed, 0); aerr != nil {
			log.Printf("ERROR: lnurlcash rail: could not mark operation %s failed after the mint refused an invoice: %v — the note stays reserved and a retry is refused until it is reconciled", op.ID, aerr)
		}
		return "", err
	}

	// submitted, then bound to OUR invoice. A crash between these two leaves an
	// `unknown` operation with no spend in flight — reconcilable, never a silent
	// second melt.
	submitted, err := r.store.Advance(op.ID, op.Version, paymentjournal.Submitted, 0)
	if err != nil {
		release()
		return "", fmt.Errorf("lnurlcash rail: cannot mark the note submitted: %w", err)
	}
	bound, err := r.store.BindInvoice(submitted.ID, submitted.Version, fingerprint, []byte(bolt11))
	if err != nil {
		release()
		return "", fmt.Errorf("lnurlcash rail: cannot bind the note to its invoice: %w", err)
	}
	_ = bound

	// The melt. This is the point of no return: the issuer may burn the note and
	// lose the response.
	meltPath := note.WithdrawURL.Path
	if callbackPath != "" {
		meltPath = callbackPath
	}
	err = r.melt(note, IssuerCallback{Host: note.Domain, Path: meltPath}, bolt11)
	if err != nil {
		// The note is NOT released. Whether the issuer burned it is exactly what we
		// no longer know, so the record stays `awaiting` and the fingerprint refuses
		// a re-presentation. If the melt did succeed, the quote settles and the
		// quote monitor grants anyway — the customer is served either way.
		log.Printf("ERROR: lnurlcash rail: redemption at %s did not confirm (operation %s, quote %s): %v — the note is NOT reusable until reconciled; if the issuer burned it, the quote will settle and the monitor will grant",
			note.Domain, op.ID, issuedQuote, err)
		return "", err
	}

	return issuedQuote, nil
}

// priorAttempts returns every journal operation that names this note, oldest
// first. It is a bounded scan: the journal caps its records, and a redemption
// path runs at human speed.
func (r *LNURLCashRail) priorAttempts(fingerprint string) ([]paymentjournal.Operation, error) {
	ops, err := r.store.List()
	if err != nil {
		return nil, fmt.Errorf("lnurlcash rail: cannot read the journal: %w", err)
	}
	var out []paymentjournal.Operation
	for _, op := range ops {
		if op.Direction != paymentjournal.Incoming {
			continue
		}
		if op.PaymentHash == fingerprint {
			out = append(out, op)
			continue
		}
		// A record that was prepared but never bound still names the note through
		// its id, which is derived from the fingerprint. Compare by prefix rather
		// than slicing: op.ID comes from any journal writer, and a short id from an
		// unrelated subsystem must not panic the redemption path.
		if strings.HasPrefix(op.ID, "lnurlcash-"+fingerprint[:32]) {
			out = append(out, op)
		}
	}
	return out, nil
}
