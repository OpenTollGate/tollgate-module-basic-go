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

// Fingerprint returns the stable, non-secret correlate the redemption journal is
// keyed by.
//
// It is keyed on the note's IDENTITY: the issuer host (lower-case, default port
// dropped) and the k1 secret. NOT the URL string.
//
// Why this matters, and it is the whole point of the function: a note is burnt ONCE
// by its issuer. If two spellings of one note hash differently they become two
// journal records, and "a second presentation never grants twice" quietly stops being
// true — an attacker changes the host's case, or appends a query parameter the issuer
// ignores, and buys a second redemption with the same note. A probe measured seven
// distinct keys for one note across ordinary spellings before this was fixed.
//
// The identity is (host, k1) rather than the URL because those are the two things the
// issuer keys its ledger on. Everything else in the URL — scheme, port, path, extra
// parameters — is a way of writing the same claim.
//
// It stays a plain SHA-256: a k1 is high-entropy, so there is no dictionary to attack.
// It is a fingerprint, not a MAC; nothing here needs to resist a reader who is already
// root on the router (the journal is 0600).
func Fingerprint(note Note) string {
	host, k1 := noteIdentity(note)
	return fingerprintOf(host + "\x00" + k1)
}

// noteIdentity extracts what the issuer's ledger keys on.
//
// The PATH is part of the identity, not just the query. LUD-03 has two shapes in the
// wild: `/withdraw?k1=<secret>` and `/withdraw/<secret>`. In the path shape there is no
// k1 parameter at all, and keying on (host, k1) alone collapsed every note at one issuer
// to a single key — so the first note redeemed would block every other note from that
// issuer. Measured: /withdraw/aaaaaaaa and /withdraw/bbbbbbbb both hashed to
// 428f46e1235fdbca before this was fixed.
//
// What is deliberately NOT part of the identity: scheme, port (default), query parameter
// ORDER, and parameters the issuer ignores. Those are spellings of the same claim, and
// including them is how one note became seven journal keys.
func noteIdentity(n Note) (host, k1 string) {
	u, err := url.Parse(n.URL)
	if err != nil || u.Host == "" {
		return "raw:" + n.URL, ""
	}
	host = strings.ToLower(u.Hostname()) // Hostname() already drops the port
	if p := u.Port(); p != "" && p != "443" && p != "80" {
		host += ":" + p
	}
	// The secret lives in the query for the LUD-03 query shape and in the path for the
	// path shape; include both, so neither shape collides and neither needs special
	// casing. Path case is preserved: paths are case-sensitive, unlike hosts.
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	k1 = path + "\x00" + u.Query().Get("k1")
	return host, k1
}

// fingerprintOf hashes a canonical identity string. Callers must pass an identity built
// by noteIdentity, never a presented string.
func fingerprintOf(identity string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(identity)))
	return hex.EncodeToString(sum[:])
}
