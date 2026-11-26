package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// /lnurlcash/redeem — the customer-facing entry point for the opt-in rail.
//
// A guest with no internet of their own hands the gate a bearer note. The gate
// does the talking: it asks the note's issuer what the note is worth, asks our own
// mint for an invoice of that value, tells the issuer to melt the note into it,
// and the existing quote monitor grants when the quote settles.
//
// What this endpoint must never do, and why each is a real failure mode:
//
//   - Never grant. It returns a quote id and the customer polls /ln-invoice like
//     any other payment. Access comes from the settled quote. A second grant path
//     would be a second place for the money invariant to be wrong.
//   - Never accept cs1 as payment. A certificate may arrive; it is never read as
//     authorisation. Only a settled quote is.
//   - Never let the note reach the issuer before its intent is durable. A crash
//     mid-melt may leave the note burned at the issuer; the journal record is what
//     stops a second presentation from double-spending or double-granting.
//   - Never retry a melt blindly. A timeout is ambiguous: the issuer may or may not
//     have burned the note. An ambiguous outcome keeps the note reserved.
//
// The customer being offline is the point. The gate being offline is not
// supported: an unreachable issuer is a refusal with a retry hint, never a grant.

const (
	// maxLNURLCashBodyBytes bounds the request before parsing. A note plus a
	// certificate is well under this; the limit makes a hostile body a 413
	// rather than an allocation.
	maxLNURLCashBodyBytes = 16 << 10

	// lnurlCashIssuerTimeout bounds one issuer interaction. A guest stands at a
	// captive portal waiting, so this is a human-scale wait.
	lnurlCashIssuerTimeout = 30 * time.Second
)

type lnurlCashRedeemRequest struct {
	// Note is the bearer note: LUD-25 `k1`/`cw1`, optionally as a withdraw URL.
	Note string `json:"note"`
	// Mint is the mint that invoices us. Required: a note is redeemed into a
	// quote, and a quote only exists at a mint this gate sells time from.
	Mint string `json:"mint"`
	// Issuer names the issuer's domain. Required only for a bare k1/cw1 note,
	// which carries no host of its own — and it is checked against the allowlist
	// before any byte leaves the router. A note that carries its own URL is
	// checked against that URL's host instead, and a hint that disagrees with it
	// is refused by Parse as ErrIssuerMismatch.
	Issuer string `json:"issuer,omitempty"`
	// Certificate is a cs1 the customer was given. Accepted, and never used to
	// authorise anything.
	Certificate string `json:"certificate,omitempty"`
}

type lnurlCashRedeemResponse struct {
	// Status mirrors /ln-invoice so a portal can treat both the same way.
	Status  int    `json:"status"`
	QuoteID string `json:"quote_id,omitempty"`
	Error   string `json:"error,omitempty"`
	// Code is a stable machine-readable refusal reason.
	Code string `json:"code,omitempty"`
	// RetryAfter seconds, when the refusal is transient.
	RetryAfter int `json:"retry_after,omitempty"`
}

func writeLNURLCashRefusal(w http.ResponseWriter, status int, code, msg string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(lnurlCashRedeemResponse{
		Status:     0,
		Error:      msg,
		Code:       code,
		RetryAfter: retryAfter,
	})
}

// lnurlCashIssuer is the slice of the issuer client the endpoint needs. It exists
// so a test can drive the handler with no network at all.
type lnurlCashIssuer interface {
	Info(ctx context.Context, n lnurlcash.Note, withdrawPath string) (lnurlcash.IssuerState, error)
}

// handleLNURLCashRedeem serves POST /lnurlcash/redeem.
//
// rail and issuer are injected rather than looked up globally, so a test can drive
// the handler without a mint, an issuer, or a journal on disk.
func handleLNURLCashRedeem(
	rail *merchant.LNURLCashRail,
	activation func() (bool, string),
	issuer func(domain string) (lnurlCashIssuer, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeLNURLCashRefusal(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.", 0)
			return
		}

		// The opt-in gate is checked per request, not once at startup: a config
		// reload that turns the rail off must take effect without a reboot.
		if activation != nil {
			if ok, reason := activation(); !ok {
				mainLogger.WithField("reason", reason).Debug("LNURLcash redemption refused: rail is not active")
				writeLNURLCashRefusal(w, http.StatusNotFound, "lnurlcash_disabled",
					"LNURLcash redemption is not enabled on this gate.", 0)
				return
			}
		}

		if rail == nil || !rail.Open() {
			writeLNURLCashRefusal(w, http.StatusServiceUnavailable, "lnurlcash_unavailable",
				"LNURLcash redemption is configured but not ready.", 30)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLNURLCashBodyBytes))
		if err != nil {
			writeLNURLCashRefusal(w, http.StatusRequestEntityTooLarge, "request_too_large",
				"Request body too large.", 0)
			return
		}

		var req lnurlCashRedeemRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeLNURLCashRefusal(w, http.StatusBadRequest, "invalid_body", "Invalid request body.", 0)
			return
		}

		rawNote := strings.TrimSpace(req.Note)
		mintURL := strings.TrimSpace(req.Mint)
		if rawNote == "" || mintURL == "" {
			writeLNURLCashRefusal(w, http.StatusBadRequest, "missing_fields",
				"note and mint are required.", 0)
			return
		}

		// The paying device is resolved from the socket, never the body — same rule
		// as /ln-invoice. A caller-named address would bind the grant to a device
		// that may not be the one paying.
		macAddress, err := clientIdentity(w, r, "")
		if err != nil {
			writeLNURLCashRefusal(w, http.StatusBadRequest, errDeviceUnresolvedCode,
				deviceUnresolvedMessage, 0)
			return
		}

		// Parsing is local: it validates the encoding and extracts the issuer's
		// domain. Nothing has been sent anywhere yet.
		//
		// The second argument is a DOMAIN HINT, not a certificate. A note given as a
		// bare k1 carries no host, so the hint names the issuer — and it is checked
		// against the allowlist below before a single byte leaves the router.
		note, err := lnurlcash.Parse(rawNote, strings.TrimSpace(req.Issuer))
		if err != nil {
			mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
				Debug("Refusing LNURLcash note: it did not parse")
			writeLNURLCashRefusal(w, http.StatusBadRequest, "invalid_note",
				"This is not a valid LNURLcash note.", 0)
			return
		}

		if issuer == nil {
			writeLNURLCashRefusal(w, http.StatusServiceUnavailable, "lnurlcash_unavailable",
				"LNURLcash redemption is configured but not ready.", 30)
			return
		}

		// What is this note worth? A read-only informational GET WITH the spend, so
		// the issuer verifies it in full before answering. This does not burn it.
		client, err := issuer(note.Domain)
		if err != nil {
			mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
				Warn("Refusing LNURLcash note: issuer is not usable")
			writeLNURLCashRefusal(w, http.StatusBadRequest, "issuer_not_allowed",
				"This note's issuer is not accepted by this gate.", 0)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), lnurlCashIssuerTimeout)
		defer cancel()
		state, err := client.Info(ctx, note, note.WithdrawURL.Path)
		if err != nil {
			var ie *lnurlcash.IssuerError
			if errors.As(err, &ie) && ie.Definitive {
				// "unknown note" / "already in use": the note will never work here.
				mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
					Warn("Issuer refused the note definitively")
				writeLNURLCashRefusal(w, http.StatusConflict, "note_refused",
					"The note's issuer refused it. It may already have been spent.", 0)
				return
			}
			// Anything else — timeout, 5xx, "pending" — is ambiguous and nothing has
			// been spent, because the lookup does not spend. The note is untouched.
			mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
				Warn("LNURLcash issuer lookup was inconclusive; nothing was spent")
			writeLNURLCashRefusal(w, http.StatusBadGateway, "issuer_unreachable",
				"The note's issuer could not be reached, so nothing has been spent. Try again shortly.", 30)
			return
		}
		if state.WithdrawableMSat == 0 {
			// The issuer answered, and the answer is "nothing". Unspent-but-zero and
			// pending both land here; either way this note buys nothing now.
			writeLNURLCashRefusal(w, http.StatusConflict, "note_not_redeemable",
				"The issuer reports this note as not currently redeemable.", 0)
			return
		}

		quoteID, err := rail.Redeem(note, state.WithdrawableMSat, macAddress, mintURL)
		if err != nil {
			switch {
			case errors.Is(err, merchant.ErrLNURLCashNoteAlreadyRedeemed):
				// Same shape as a replay refusal elsewhere: the note may already be
				// redeemed and a grant may already exist, so a bare "no" would lose
				// the customer their money.
				mainLogger.WithField("remote_addr", r.RemoteAddr).
					Warn("Refused an LNURLcash note that was already presented")
				writeLNURLCashRefusal(w, http.StatusConflict, "note_already_presented",
					"This note has already been presented to this gate. Check your session; if no access was granted, contact the operator with this message.", 0)
				return
			default:
				// Includes an ambiguous melt. The note is NOT released: whether the
				// issuer burned it is exactly what is unknown, and if it did the quote
				// settles and the monitor grants anyway.
				mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
					Warn("LNURLcash redemption did not confirm; note stays reserved")
				writeLNURLCashRefusal(w, http.StatusBadGateway, "redeem_unconfirmed",
					"The note could not be confirmed as redeemed. It has not been reused; if payment went through, access will follow shortly.", 30)
				return
			}
		}

		mainLogger.WithFields(map[string]interface{}{
			"remote_addr": r.RemoteAddr,
			"quote_id":    quoteID,
		}).Info("LNURLcash note redeemed; access follows the quote's settlement")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lnurlCashRedeemResponse{Status: 1, QuoteID: quoteID})
	}
}

// newProductionLNURLCashRail builds the rail a live gate uses, or returns nil when
// the operator has not opted in.
//
// Returning nil rather than a half-wired rail is deliberate: a rail with a nil melt
// compiles, serves, and refuses every note, which reads as a network fault and hides
// a wiring bug.
func newProductionLNURLCashRail(
	activation func() (bool, string),
	issuerAllowed func(domain string) (string, bool),
	maxNoteMSat uint64,
	store *paymentjournal.Store,
	invoice func(macAddress, mintURL string, sats uint64) (string, string, error),
) *merchant.LNURLCashRail {
	if activation != nil {
		if ok, _ := activation(); !ok {
			return nil
		}
	}
	if store == nil || invoice == nil {
		return nil
	}

	melt := func(note lnurlcash.Note, callback merchant.IssuerCallback, bolt11 string) error {
		// Re-check the issuer here, not only at the endpoint: this is the last moment
		// before a bearer secret leaves the router, and the note's own domain is
		// attacker-controlled.
		if issuerAllowed != nil {
			if _, ok := issuerAllowed(note.Domain); !ok {
				return fmt.Errorf("lnurlcash: issuer %q is not allowlisted", note.Domain)
			}
		}
		client, err := lnurlcash.NewIssuerClient(note.Domain)
		if err != nil {
			return fmt.Errorf("lnurlcash: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), lnurlCashIssuerTimeout)
		defer cancel()
		cb := &url.URL{Scheme: "https", Host: callback.Host, Path: callback.Path}
		return client.Melt(ctx, note, cb, bolt11)
	}

	return merchant.NewLNURLCashRail(store, maxNoteMSat/1000, invoice, melt)
}
