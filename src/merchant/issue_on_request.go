package merchant

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// issueQuotePollInterval and issueQuoteTimeout bound how long issuance waits
// for a quote to be paid. The wait exists because the operator's payment and
// the mint's recognition are separate events; it is deliberately finite, so a
// never-paid quote fails with an error the operator can act on rather than
// hanging a request forever.
const (
	defaultIssueQuotePollInterval = 2 * time.Second
	defaultIssueQuoteTimeout      = 2 * time.Minute
)

// issueWait returns the poll bounds for this merchant, falling back to the
// defaults. They are fields rather than constants so a test can exercise the
// timeout path without sitting through it — a two-minute test is a test that
// gets skipped.
func (m *Merchant) issueWait() (time.Duration, time.Duration) {
	poll, timeout := m.issuePollInterval, m.issueTimeout
	if poll <= 0 {
		poll = defaultIssueQuotePollInterval
	}
	if timeout <= 0 {
		timeout = defaultIssueQuoteTimeout
	}
	return poll, timeout
}

// IssueOnRequest issues ecash on request: quote, wait for the payment to be
// recognised, mint, and hand back a serialized token the customer can hold in
// their own wallet and spend in small transactions.
//
// This is the operator-facing shape of the 2026-10 CDK mint integration. It
// is intentionally the simplest thing that can work — the operator asks for an
// amount in a unit, and gets a token — because issuance is "on request" for
// now: no teller ceremony, no batching, no queue.
//
// Order of operations, and why each step is where it is:
//
//  1. Validate locally BEFORE touching the mint (AGENTS.md: no irreversible
//     monetary operation before validations that can be done locally). Amount
//     and unit are checked here so an obviously-bad request costs nothing.
//  2. RequestMintQuote — no value moves; a quote is a promise, not a payment.
//  3. Wait for the quote to be PAID. MintTokens on an unpaid quote is refused
//     by the mint, but issuance must not lean on the mint as its only guard.
//  4. MintTokens — the POINT OF NO RETURN for this operation: the mint creates
//     the ecash and marks the quote ISSUED. There is no compensating action
//     that un-mints it.
//  5. Send — splits the minted value out into a token to hand over. If this
//     fails, the value exists in the operator's wallet and no token was
//     delivered; that is reported explicitly (see below) because a plain
//     "failed" invites a blind retry, and re-minting an already-ISSUED quote
//     is the duplicate-outputs brick (#257/#266).
//
// Unit handling: the unit is not decoration, it is half of the price. A token
// whose unit does not match the mint's keyset is refused rather than issued,
// because the alternative is issuing something the gateway will then refuse or
// misprice.
//
// Known gap, stated rather than hidden: the quote is tracked in memory for the
// duration of this call. A restart between the payment and MintTokens loses
// the quote handle, and the paid value would need to be recovered from the
// mint by quote ID. A durable quote journal is the correct fix and is not
// built yet.
func (m *Merchant) IssueOnRequest(mintURL, unit string, amount uint64) (string, error) {
	if m == nil || m.tollwallet == nil {
		return "", fmt.Errorf("cannot issue: wallet is not available")
	}
	mintURL = strings.TrimSpace(mintURL)
	unit = strings.TrimSpace(unit)
	if mintURL == "" {
		return "", fmt.Errorf("cannot issue: no mint URL given")
	}
	if amount == 0 {
		return "", fmt.Errorf("cannot issue: amount must be greater than zero")
	}
	// An empty unit means "sat" by long-standing convention, but a sat token
	// against an operator's custom-unit mint is a mismatch, and the caller
	// must say which unit it means. Requiring it here keeps the two spellings
	// from drifting apart silently.
	if unit == "" {
		return "", fmt.Errorf("cannot issue: unit is required (use \"sat\" for a satoshi mint)")
	}
	if err := m.unitIsAccepted(mintURL, unit); err != nil {
		return "", err
	}

	quote, err := m.tollwallet.RequestMintQuote(amount, mintURL)
	if err != nil {
		return "", fmt.Errorf("mint quote request failed: %w", err)
	}
	if quote == nil || quote.QuoteID == "" {
		return "", fmt.Errorf("mint quote request returned no quote id")
	}
	log.Printf("issue: quote %s requested for %d %s at %s (payment request %d bytes)",
		quote.QuoteID, amount, unit, mintURL, len(quote.Request))

	if err := m.awaitQuotePaid(quote.QuoteID); err != nil {
		return "", err
	}

	// Point of no return.
	minted, err := m.tollwallet.MintTokens(quote.QuoteID)
	if err != nil {
		return "", fmt.Errorf("quote %s is paid but minting failed: %w — no token was issued; "+
			"the quote is still mintable, so retry the issue rather than paying again",
			quote.QuoteID, err)
	}

	token, err := m.tollwallet.Send(minted, mintURL, false)
	if err != nil {
		return "", fmt.Errorf("FUNDS HELD: %d %s were minted from quote %s and are in the operator wallet, "+
			"but sending the token failed: %w — do NOT re-mint this quote; drain the wallet "+
			"(tollgate wallet drain cashu) to recover the value",
			minted, unit, quote.QuoteID, err)
	}
	if token == nil {
		return "", fmt.Errorf("FUNDS HELD: %d %s were minted from quote %s and are in the operator wallet, "+
			"but no token was produced — do NOT re-mint this quote; drain the wallet to recover the value",
			minted, unit, quote.QuoteID)
	}

	serialized, err := token.Serialize()
	if err != nil {
		token.Close()
		return "", fmt.Errorf("FUNDS HELD: %d %s were minted from quote %s and are in the operator wallet, "+
			"but the token could not be serialized: %w — do NOT re-mint this quote; drain the wallet "+
			"to recover the value", minted, unit, quote.QuoteID, err)
	}
	token.Close()

	if serialized == "" {
		return "", fmt.Errorf("FUNDS HELD: %d %s were minted from quote %s and are in the operator wallet, "+
			"but serialization produced an empty token — do NOT re-mint this quote; drain the wallet "+
			"to recover the value", minted, unit, quote.QuoteID)
	}

	log.Printf("issue: issued %d %s from quote %s (%d byte token)", minted, unit, quote.QuoteID, len(serialized))
	return serialized, nil
}

// awaitQuotePaid blocks until the mint reports the quote as paid, or the
// timeout elapses. The mint's own state is the authority: a local timer would
// race the mint's recognition of the payment.
func (m *Merchant) awaitQuotePaid(quoteID string) error {
	pollInterval, timeout := m.issueWait()
	deadline := time.Now().Add(timeout)
	var last tollwallet.MintQuoteState
	for {
		state, err := m.tollwallet.GetMintQuoteState(quoteID)
		if err != nil {
			// A transient read failure is not a verdict on the quote; keep
			// polling until the deadline rather than failing the issuance.
			log.Printf("issue: quote %s state read failed (will retry): %v", quoteID, err)
		} else {
			last = state
			switch state {
			case tollwallet.StatePaid, tollwallet.StateIssued:
				return nil
			case tollwallet.StatePending:
				// Pending means the mint is still working; keep waiting.
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("quote %s was not paid within %s (last state %s) — the payment may still "+
				"arrive; check the mint before requesting another quote",
				quoteID, timeout, last)
		}
		time.Sleep(pollInterval)
	}
}

// knownUnit reports whether a unit is one this codebase can name at all.
// The list is deliberately small: these are the units the Cashu ecosystem and
// this project's operator mints actually use. Anything else is refused rather
// than passed through, because a token's unit is half its price and an
// unrecognised one cannot be priced honestly.
func knownUnit(unit string) bool {
	switch strings.ToLower(unit) {
	case "sat", "msat", "usd", "eur", "mb", "min":
		return true
	}
	return false
}

// unitIsAccepted checks the requested unit against the configured mints. An
// empty configured unit means "sat", mirroring the config contract.
func (m *Merchant) unitIsAccepted(mintURL, unit string) error {
	if !knownUnit(unit) {
		return fmt.Errorf("cannot issue: %q is not a unit this gateway can price "+
			"(known: sat, msat, usd, eur, mb, min) — a token whose unit cannot be "+
			"priced must not be issued", unit)
	}
	if m.config == nil {
		// No config to cross-check against (unit tests, bare literals). The
		// unit is still known, so this is not the mispricing case; the mint
		// remains the authority on what it actually issues.
		return nil
	}
	normalizedMint := tollwallet.NormalizeMintURL(mintURL)
	for _, accepted := range m.config.AcceptedMints {
		if tollwallet.NormalizeMintURL(accepted.URL) != normalizedMint {
			continue
		}
		configured := accepted.Unit
		if configured == "" {
			configured = "sat"
		}
		if !strings.EqualFold(configured, unit) {
			return fmt.Errorf("cannot issue %s: mint %s is configured for unit %q — "+
				"issuing a %q token against it would produce ecash the gateway refuses "+
				"or misprices", unit, mintURL, configured, unit)
		}
		return nil
	}
	return fmt.Errorf("cannot issue: mint %s is not in accepted_mints", mintURL)
}

// DefaultIssuanceMint returns the configured mint that issues the given unit,
// or "" if none does. A router running an operator mint normally has exactly
// one, so the CLI can omit the URL; being explicit here keeps the "which mint"
// decision in one place instead of in the CLI's argument parsing.
func (m *Merchant) DefaultIssuanceMint(unit string) string {
	if m == nil || m.config == nil {
		return ""
	}
	for _, accepted := range m.config.AcceptedMints {
		configured := accepted.Unit
		if configured == "" {
			configured = "sat"
		}
		if strings.EqualFold(configured, unit) {
			return accepted.URL
		}
	}
	return ""
}
