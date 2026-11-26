// Package lnurlcash implements LNURLcash withdraw notes as an OPT-IN payment
// method for the gate.
//
// A note is not payment; REDEMPTION is payment. The customer hands the gate a
// withdraw link, and the gate performs the LUD-03 withdraw sequence against the
// note's issuer:
//
//  1. GET the note's `lnurl` target -> a `withdrawRequest` carrying the
//     callback, the note secret `k1`, and `maxWithdrawable`;
//  2. ask OUR OWN accepted mint for a NUT-04 quote of the redeemable amount —
//     this repo has no Lightning node, so the invoice that the issuer will pay
//     comes from the mint, exactly as it does for every other purchase;
//  3. GET the callback with `k1` and `pr=<that invoice>`, which makes the issuer
//     melt the note into our mint;
//  4. grant access only when the MINT reports that quote paid. Neither the
//     issuer's "OK" nor anything the note itself claims is value.
//
// The customer may be offline; the gate may not. Custody by the issuer is an
// accepted, disclosed trade-off — see
// docs/architecture/lnurlcash-opt-in-payment-method-decision.md.
//
// Nothing in this package is enabled unless the operator turns it on: the caller
// (the API layer) only registers the route when the config says so, and Redeem
// itself refuses with ErrDisabled when the config is off.
package lnurlcash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/btcsuite/btcd/btcutil/bech32"
)

// Note is a decoded, validated LNURLcash withdraw note. The Raw form is a
// bearer secret and must never be logged or put in an error; URL still carries
// it (the gate has to send it), so only String() and the errors are safe
// surfaces.
type Note struct {
	// Raw is the note as presented, trimmed. Bearer material.
	Raw string
	// URL is the https target the note decodes to. Bearer material.
	URL string
}

// String is the log-safe rendering: it names the host and nothing else.
func (n Note) String() string {
	u, err := url.Parse(n.URL)
	if err != nil || u.Host == "" {
		return "lnurlcash note"
	}
	return "lnurlcash note for " + u.Host
}

// DecodeNote validates a presented note and returns the https URL it addresses.
//
// Two spellings are legal, and both are attacker-controlled: a bare https URL,
// or an `lnurl1…` bech32 string (LUD-01) whose payload is that URL. The refusals
// are deliberately strict, because everything downstream — which hosts the gate
// will dial, and where the k1 secret will be sent — is decided here:
//
//   - plain http is refused: the k1 is a bearer secret, and this repo has no
//     "insecure but convenient" mode in production. (Tests reach a loopback
//     httptest server through a separate, explicitly named seam.)
//   - a fragment is refused rather than silently dropped, because dropping it
//     would send the gate to a different resource than the customer was given.
//   - userinfo is refused: `https://user:pw@host/` is a credential-smuggling and
//     mis-attribution shape, and no issuer emits it.
func DecodeNote(presented string) (Note, error) {
	return decodeNote(presented, relaxScheme{})
}

// relaxScheme names how far the https requirement is relaxed for one client,
// derived from that client's own options so there is exactly one source of truth.
//
// Production is the zero value: https only, nothing relaxed. The two test flags
// each widen a DIFFERENT thing — allowLoopback lets a real httptest server on a
// loopback literal be dialled, allowPlaintext lets a lab container be reached by
// hostname. Neither is set by the daemon's default wiring.
type relaxScheme struct {
	allowLoopback  bool
	allowPlaintext bool
}

// forClient derives the relaxation from a client's own options; this is the only
// constructor used outside tests.
func forClient(c *Client) relaxScheme {
	return relaxScheme{
		allowLoopback:  c.allowInsecureLoopbackForTests,
		allowPlaintext: c.plainTextIssuersForTests,
	}
}

func (r relaxScheme) accepts(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	if r.allowPlaintext {
		return true
	}
	return r.allowLoopback && isLoopbackLiteral(u.Hostname())
}
func decodeNote(presented string, relax relaxScheme) (Note, error) {
	raw := strings.TrimSpace(presented)
	if raw == "" {
		return Note{}, errors.New("note is empty")
	}

	target := raw
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "lnurl1") && !strings.Contains(lower, "://") {
		decoded, err := decodeBech32LNURL(raw)
		if err != nil {
			return Note{}, fmt.Errorf("note is not a decodable lnurl: %w", err)
		}
		target = decoded
	}

	u, err := url.Parse(target)
	if err != nil {
		// Do NOT wrap: url.Parse's error text quotes the input it was given, and
		// the input is the note — a bearer secret that would then be logged verbatim
		// by every caller that logs a refusal.
		return Note{}, errors.New("note does not decode to a usable URL")
	}
	if u.Scheme != "https" {
		if !relax.accepts(u) {
			return Note{}, fmt.Errorf("note must decode to an https URL (got scheme %q)", u.Scheme)
		}
	}
	if u.Host == "" {
		return Note{}, errors.New("note must decode to an https URL with a host")
	}
	if u.User != nil {
		return Note{}, errors.New("note URL carries userinfo, which no issuer emits")
	}
	if u.Fragment != "" || strings.Contains(target, "#") {
		return Note{}, errors.New("note URL carries a fragment; the gate would fetch a different resource than the customer was given")
	}

	return Note{Raw: raw, URL: target}, nil
}

// decodeBech32LNURL decodes a LUD-01 `lnurl1…` string: bech32 with the "lnurl"
// HRP, payload grouped 5 bits per character, converted back to the UTF-8 bytes
// of a URL.
func decodeBech32LNURL(s string) (string, error) {
	hrp, fiveBit, err := bech32.Decode(s)
	if err != nil {
		return "", err
	}
	if hrp != "lnurl" {
		return "", fmt.Errorf("bech32 prefix is %q, not \"lnurl\"", hrp)
	}
	decoded, err := bech32.ConvertBits(fiveBit, 5, 8, false)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// Fingerprint returns the stable, non-secret correlate the redemption journal
// is keyed by.
//
// It is keyed on the DECODED CANONICAL URL, not the presented string. One note
// has two spellings in the wild — the `lnurl1…` bech32 form a wallet shows and the
// https URL it decodes to — and the QR a customer scans may be either. Hashing
// the raw string gives those two spellings two journal records, so the same note
// could be redeemed twice: exactly the double-grant the journal exists to prevent.
// Canonicalising first makes "have I seen this note?" answer the question the
// caller means. (A reviewer reproduced this: 2819fac9… for the URL, 9b0c62… for
// the same note in bech32.)
//
// It stays a plain SHA-256 because a note is high-entropy (a k1 secret), so there
// is no dictionary to attack; it is a fingerprint, not a MAC, and nothing here
// needs to resist a reader who is already root on the router (the journal is 0600).
func Fingerprint(note Note) string {
	return fingerprintOf(note.URL)
}

// fingerprintOf hashes a canonical note URL. Callers must pass note.URL, never
// the presented string — see Fingerprint.
func fingerprintOf(canonicalURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(canonicalURL)))
	return hex.EncodeToString(sum[:])
}
