package lnurlcash

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// Config is the operator's opt-in. Every field defaults to OFF / conservative,
// and Enabled is the only switch: with it false the route is never registered and
// Redeem refuses.
type Config struct {
	// Enabled turns the payment method on. Default FALSE. Absent or false means
	// the endpoint does not exist (404, not 403): the gate does not advertise a
	// payment method it will not honour.
	Enabled bool
	// MaxNoteSats caps what one note may buy, in satoshis. A note's own
	// maxWithdrawable is an issuer CLAIM, so this is the operator's ceiling on
	// both value at risk and the size of the invoice we ask our mint for.
	MaxNoteSats uint64
	// RedemptionTimeoutSeconds bounds the whole issuer interaction (the two GETs
	// and the callback). Past it the redemption is ambiguous, never retried.
	RedemptionTimeoutSeconds int
	// RetentionSeconds is how long a TERMINAL redemption record is kept before
	// pruning. Pending and submitted records are never pruned. Zero disables
	// pruning entirely (the safer default on a router whose storage is unknown).
	RetentionSeconds int
}

// DefaultConfig is the shipped default: off, with conservative bounds that apply
// from the moment an operator turns it on.
func DefaultConfig() Config {
	return Config{
		Enabled:                  false,
		MaxNoteSats:              1000,
		RedemptionTimeoutSeconds: 30,
		RetentionSeconds:         7 * 24 * 60 * 60,
	}
}

// ErrDisabled is returned by every entry point when the operator has not opted in.
var ErrDisabled = errors.New("lnurlcash: the payment method is disabled")

// Gate is the settlement half: the gate's own accepted mint, and the client the
// grant applies to. It is an interface so the redeemer can be tested without a
// mint, and so the merchant package owns the pricing, quote, and session logic
// rather than this one duplicating it.
type Gate interface {
	// RequestRedeemInvoice asks the settlement mint for a NUT-04 quote of
	// amountSats and returns the quote's ID and its BOLT11 invoice. This is the
	// same quote path the portal's `/ln-invoice` flow uses.
	RequestRedeemInvoice(macAddress, mintURL string, amountSats uint64) (quoteID, invoice string, err error)
	// RedemptionSettled reports whether the mint has been paid for this quote,
	// and whether the access grant has been applied for macAddress. It is the
	// ONLY source of "value arrived": the issuer's word is not.
	RedemptionSettled(quoteID, macAddress string) (paid, granted bool, err error)
	// SettlementMint names the mint that invoices are requested from: the first
	// accepted mint (the same rule the upstream settlement path uses).
	SettlementMint() (string, error)
}

// The result states the API layer answers with. They are deliberately distinct:
// "the issuer accepted" and "the mint paid" are different facts, and only the
// second one is value.
const (
	// StateUnavailable means the operator has not enabled the method.
	StateUnavailable = "unavailable"
	// StatePending means an earlier attempt of this note never left the box.
	StatePending = "pending"
	// StateAmbiguous means the issuer's answer was lost. Never retried.
	StateAmbiguous = "ambiguous"
	// StateRedeemed means the issuer accepted AND the quote exists; Granted says
	// whether the mint confirmed the money and the gate opened.
	StateRedeemed = "redeemed"
	// StateAbandoned means the note was refused and nothing was sent.
	StateAbandoned = "abandoned"
)

// Result is what one redemption attempt tells the caller. It is sufficient for
// the API layer to answer the customer and for the portal to poll the quote with
// the existing `/ln-invoice` status endpoint.
type Result struct {
	State      string
	Granted    bool
	QuoteID    string
	Invoice    string
	AmountSats uint64
	MintURL    string
	Message    string
}

// Redeemer performs LUD-03 redemptions against a note's issuer, journaling intent
// before the irreversible step.
type Redeemer struct {
	cfg    Config
	gate   Gate
	store  *IntentStore
	client *Client
	now    func() time.Time
}

// NewRedeemer wires a redeemer. store may not be nil when the feature is enabled:
// a redemption without a journal is an unguarded money path, and the constructor
// would rather refuse at boot than grant twice at 3 a.m.
func NewRedeemer(cfg Config, gate Gate, store *IntentStore, client *Client) *Redeemer {
	return &Redeemer{cfg: cfg, gate: gate, store: store, client: client,
		now: func() time.Time { return time.Now().UTC() }}
}

// Fingerprint is the journal key for a note. It is also the log correlate: a
// fingerprint identifies a note without being one.
func (r *Redeemer) Fingerprint(n Note) string { return Fingerprint(n) }

// FingerprintOfPresented lets a caller fingerprint a note it has not yet handed
// over (the API layer logs it before calling Redeem).
func (r *Redeemer) FingerprintOfPresented(presented string) string {
	return fingerprintOf(presented)
}

// Config returns the configuration the redeemer was built with.
func (r *Redeemer) Config() Config { return r.cfg }

// Enabled reports whether the operator opted in.
func (r *Redeemer) Enabled() bool { return r.cfg.Enabled }

// Redeem runs one redemption for the client at macAddress.
//
// The order is the design, and each step states what it protects:
//
//  1. decode the note and fingerprint it (no network, no secret logged);
//  2. consult the journal — a note we have already acted on is answered from
//     disk, never re-sent, whatever its state;
//  3. fetch the withdrawRequest and validate its callback host (no invoice yet:
//     a hostile issuer must not be able to cost us a quote request);
//  4. cap the amount, then ask OUR mint for the invoice that the issuer will pay;
//  5. write the intent as submitted (durable) — from here the redemption cannot be
//     re-sent;
//  6. send the callback;
//  7. ask the MINT whether the quote is paid. Only then is there a grant.
func (r *Redeemer) Redeem(ctx context.Context, presentedNote, macAddress string) (Result, error) {
	if !r.Enabled() {
		return Result{State: StateUnavailable, Message: "this TollGate does not accept LNURLcash notes"}, ErrDisabled
	}
	if r.store == nil {
		return Result{}, errors.New("lnurlcash: no redemption journal; refusing to redeem")
	}

	note, err := decodeNote(presentedNote, r.client.allowInsecureLoopbackForTests)
	if err != nil {
		return Result{State: StateAbandoned, Message: "That is not a usable LNURLcash note."}, err
	}
	fp := Fingerprint(note)

	// Step 2: the journal decides. This is what makes a second presentation free
	// of network effects.
	var pending *Intent
	if rec, ok, err := r.store.Get(fp); err != nil {
		return Result{}, err
	} else if ok {
		if rec.State != IntentPending {
			return r.resume(fp, rec)
		}
		// A pending record means an earlier attempt died before anything left the
		// box. Nothing was promised to the issuer and the note is untouched, so
		// this attempt may proceed — on the SAME record, so it can still only
		// happen once.
		pending = &rec
	}

	// Step 3.
	withdraw, err := r.client.FetchWithdrawRequest(ctx, note.URL)
	if err != nil {
		return r.refuse(fp, note, macAddress, err)
	}

	// Step 4: the amount is the note's claim, capped by the operator's ceiling.
	amountSats := redeemableSats(withdraw.MaxWithdrawableMSat, r.cfg.MaxNoteSats)
	if amountSats == 0 {
		return r.refuse(fp, note, macAddress, fmt.Errorf("note is worth less than one satoshi"))
	}

	mintURL, err := r.gate.SettlementMint()
	if err != nil {
		return Result{}, fmt.Errorf("lnurlcash: no settlement mint: %w", err)
	}

	var rec Intent
	if pending != nil {
		// Keep the amount that was journalled: the note's own claim is the
		// issuer's, and a second fetch of it may say anything.
		amountSats = pending.AmountSats
		mintURL = pending.MintURL
		rec = *pending
	} else {
		// Step 5a: the intent is durable BEFORE any invoice exists, so a crash here
		// leaves a `pending` record that plainly says "nothing was sent".
		rec, err = r.store.Begin(fp, Intent{
			MacAddress: macAddress,
			MintURL:    mintURL,
			AmountSats: amountSats,
			IssuerHost: withdraw.Callback.Host,
		})
		if err != nil {
			return Result{}, err
		}
	}

	quoteID, invoice, err := r.gate.RequestRedeemInvoice(macAddress, mintURL, amountSats)
	if err != nil {
		// No invoice, no callback: the record stays pending and nothing was
		// promised to the issuer. The customer keeps their note.
		return Result{State: StateAbandoned, AmountSats: amountSats, MintURL: mintURL,
			Message: "The router could not obtain an invoice to settle this note against. Your note was not used."}, err
	}
	if _, err := r.store.SetInvoice(fp, invoice); err != nil {
		return Result{}, err
	}

	// Step 5b: submitted is the point of no return.
	rec, err = r.store.MarkSubmitted(fp, quoteID)
	if err != nil {
		return Result{}, err
	}

	// Step 6: the callback. One attempt; a transport failure is ambiguous, not a
	// failure to retry.
	redeemCtx, cancel := context.WithTimeout(ctx, r.callbackTimeout())
	defer cancel()

	outcome, err := r.client.RequestRedemption(redeemCtx, withdraw.Callback, withdraw.K1, invoice)
	if err != nil {
		// The client refused to even build the request (a URL shape it does not
		// accept). Nothing was sent.
		_, _ = r.store.Advance(fp, rec.Version, IntentAbandoned, "callback refused before sending")
		return Result{State: StateAbandoned, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
			Message: "The note's issuer address was unusable; your note was not used."}, err
	}
	if outcome.Ambiguous {
		_, _ = r.store.Advance(fp, rec.Version, IntentAmbiguous, outcome.Reason)
		log.Printf("lnurlcash: redemption for note %s is AMBIGUOUS (%s) — not retried; reconcile against mint quote %s",
			shortFP(fp), outcome.Reason, quoteID)
		return Result{State: StateAmbiguous, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
			Message: "The note's issuer did not confirm. Do NOT present the note again — ask the operator to check it."}, nil
	}
	if !outcome.OK {
		_, _ = r.store.Advance(fp, rec.Version, IntentAbandoned, outcome.Reason)
		log.Printf("lnurlcash: issuer REFUSED note %s: %s", shortFP(fp), outcome.Reason)
		return Result{State: StateAbandoned, AmountSats: amountSats, MintURL: mintURL,
			Message: "The note's issuer refused it: " + outcome.Reason}, nil
	}

	// Step 7: the mint decides whether value arrived. We just watched the issuer
	// accept, so the settlement answer is a confirmation, not a reconstruction.
	return r.settle(fp, rec, quoteID, invoice, amountSats, mintURL, macAddress, true)
}

// resume answers a presentation of a note the journal already knows.
func (r *Redeemer) resume(fp string, rec Intent) (Result, error) {
	switch rec.State {
	case IntentRedeemed:
		// Already resolved as paid. Report the recorded outcome; the grant itself is
		// idempotent (the merchant applies it by MAC), so answering does not need to
		// grant again.
		return Result{State: StateRedeemed, Granted: true, QuoteID: rec.QuoteID, Invoice: rec.Invoice,
			AmountSats: rec.AmountSats, MintURL: rec.MintURL,
			Message: "This note was already redeemed."}, nil
	case IntentAbandoned:
		return Result{State: StateAbandoned, AmountSats: rec.AmountSats, MintURL: rec.MintURL,
			Message: "This note was already presented and its issuer refused it."}, nil
	case IntentAmbiguous:
		return Result{State: StateAmbiguous, QuoteID: rec.QuoteID, Invoice: rec.Invoice,
			AmountSats: rec.AmountSats, MintURL: rec.MintURL,
			Message: "This note's earlier redemption did not confirm. Do NOT present it again."}, nil
	case IntentSubmitted:
		// The callback went out and the answer was lost. The mint's quote state is
		// the authoritative recovery source — never a second callback.
		return r.settle(fp, rec, rec.QuoteID, rec.Invoice, rec.AmountSats, rec.MintURL, rec.MacAddress, false)
	default: // IntentPending
		// Nothing was sent. Report it that way; the caller may retry the flow,
		// which will re-enter with this same record and proceed once.
		return Result{State: StatePending, AmountSats: rec.AmountSats, MintURL: rec.MintURL,
			Message: "This note's earlier attempt never reached the issuer."}, nil
	}
}

// settle asks the mint whether the quote we invoiced has been paid, and reports the
// outcome. It never grants on the issuer's word: paid is the mint's answer, and the
// grant is applied by the merchant's existing allotment path.
func (r *Redeemer) settle(fp string, rec Intent, quoteID, invoice string, amountSats uint64, mintURL, macAddress string, observed bool) (Result, error) {
	paid, granted, err := r.gate.RedemptionSettled(quoteID, macAddress)
	if err != nil {
		if !observed {
			// A record we did not watch, and a mint that will not answer: the
			// outcome is genuinely unknown. Say so; never imply a grant.
			return Result{State: StateAmbiguous, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
				Message: "This note's earlier redemption could not be checked. Do NOT present it again."}, nil
		}
		// A mint that will not answer is not a failure of the redemption: the quote
		// stays open and the portal's poll will see it settle later.
		return Result{State: StateRedeemed, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
			Message: "The issuer accepted the note; the mint has not confirmed settlement yet."}, nil
	}
	if !paid {
		if !observed {
			return Result{State: StateAmbiguous, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
				Message: "This note's earlier redemption was not confirmed by the mint. Do NOT present it again."}, nil
		}
		return Result{State: StateRedeemed, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
			Message: "The issuer accepted the note; the mint has not confirmed settlement yet."}, nil
	}

	// The mint has the money. Resolve the record if it is not resolved yet.
	if rec.State == IntentSubmitted {
		if _, err := r.store.Advance(fp, rec.Version, IntentRedeemed, "mint reported the quote paid"); err != nil {
			log.Printf("lnurlcash: could not mark note %s redeemed: %v", rec.QuoteID, err)
		}
	}

	if !granted {
		return Result{State: StateRedeemed, QuoteID: quoteID, Invoice: invoice, AmountSats: amountSats, MintURL: mintURL,
			Message: "Your note settled, but the router could not open the gate yet. Do NOT present the note again."}, nil
	}
	return Result{State: StateRedeemed, Granted: true, QuoteID: quoteID, Invoice: invoice,
		AmountSats: amountSats, MintURL: mintURL, Message: "Redeemed and granted."}, nil
}

// refuse records a refusal that happened before anything was sent. It is a
// terminal `abandoned` record only when there is an intent to attach it to;
// otherwise it is simply an error the caller answers.
func (r *Redeemer) refuse(fp string, note Note, macAddress string, cause error) (Result, error) {
	log.Printf("lnurlcash: note %s refused before any callback: %v", shortFP(fp), cause)
	return Result{State: StateAbandoned, Message: "The note's issuer did not answer usefully. Your note was not used."}, cause
}

func (r *Redeemer) callbackTimeout() time.Duration {
	secs := r.cfg.RedemptionTimeoutSeconds
	if secs <= 0 {
		secs = DefaultConfig().RedemptionTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

func redeemableSats(maxWithdrawableMSat, maxNoteSats uint64) uint64 {
	sats := maxWithdrawableMSat / 1000 // millisats -> sats, floored: never ask for more than the note holds
	if maxNoteSats > 0 && sats > maxNoteSats {
		sats = maxNoteSats
	}
	return sats
}

func shortFP(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
