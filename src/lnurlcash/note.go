// Package lnurlcash decodes and verifies LNURLcash bearer notes (LUD-25) and
// talks to the note's issuer. It never decides whether access is granted: it
// answers "is this a well-formed bearer note at an issuer this router allows,
// and what does the issuer say it is worth".
//
// Scope of this first version, stated so it cannot be over-read:
//
//   - Supported spend: the BEARER note only, in both of LUD-25's encodings —
//     the `k1` short form (64 hex characters, the preimage) and the `cw1` full
//     form of the standard bearer leaf `OP_SHA256 <h> OP_EQUAL`.
//   - Refused: `ck1` key-path spends and any other tapscript leaf, because
//     verifying those needs a BIP-342 tapscript interpreter this build does not
//     carry. They are refused with a named reason, never accepted unverified:
//     "cannot check" must not read as "checked".
//   - A `cs1` certificate is decoded and, when the issuer publishes a
//     mintPubkey, verified — and it is used ONLY to cross-check the issuer's
//     answer. It never grants access. LUD-25 says a certificate proves the note
//     was outstanding at `t`, not that it still is, and the previous holder
//     still knows the spend.
package lnurlcash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil/bech32"
)

// bearerInternalKeyHex is BIP-341's nothing-up-my-sleeve point, the internal key
// of a bearer note (a bearer note deliberately has no key path).
const bearerInternalKeyHex = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"

// maxNoteInput bounds the string handed to Parse before any parsing. One note
// is ~200 bytes; a merged set is bounded by URL length. Anything longer is
// refused rather than parsed.
const maxNoteInput = 2048

// Kind names the spend encoding a note was presented in.
type Kind string

const (
	// KindBearerPreimage is LUD-25's `k1` short form: the 32-byte preimage in hex.
	KindBearerPreimage Kind = "bearer-preimage"
	// KindBearerCW1 is the full `cw1` encoding of the same bearer spend.
	KindBearerCW1 Kind = "bearer-cw1"
)

// Note is a decoded, locally verified bearer note. It holds the spend the
// customer already handed us; treat it as a bearer secret.
type Note struct {
	Kind        Kind
	Domain      string   // canonical issuer host LUD-25 commits to
	WithdrawURL *url.URL // issuer endpoint, query stripped
	Q           [32]byte // x-only taproot output key
	Hash        [32]byte // sha256(preimage), the bearer leaf's hashlock
	Certificate string   // customer-supplied cs1 (may be empty); never a grant condition

	preimage [32]byte
}

// String redacts the spend: a Note must never hand a bearer secret to a log
// line, an error message or a %v formatting.
func (n Note) String() string {
	return fmt.Sprintf("lnurlcash note %s at %s (Q=%x…, cert=%t)", n.Kind, n.Domain, n.Q[:4], n.Certificate != "")
}

// GoString keeps %#v from printing the preimage.
func (n Note) GoString() string { return n.String() }

// Spend returns the `k1` value to send to the issuer: the preimage in lowercase
// hex. This is the secret; call it only where the request is built.
func (n Note) Spend() string { return hex.EncodeToString(n.preimage[:]) }

// CP1 returns the `cp1` encoding of the note's Q — what a check that must not
// reveal the spend uses (`?p=`, LUD-25 "Checking a note without exposing it").
func (n Note) CP1() (string, error) { return EncodeCP1(n.Q) }

// Named refusals. Callers map these to a customer-visible refusal and an
// operator-visible reason; none of them carries a spend.
var (
	ErrMalformedNote    = fmt.Errorf("lnurlcash: malformed note")
	ErrUnsupportedSpend = fmt.Errorf("lnurlcash: unsupported spend encoding")
	ErrNotBearer        = fmt.Errorf("lnurlcash: spend does not open the note's output key")
	ErrInsecureScheme   = fmt.Errorf("lnurlcash: issuer URL must be https (http only for .onion)")
	ErrIssuerMismatch   = fmt.Errorf("lnurlcash: note names a different issuer than the one allowed")
)

// Parse decodes a note from either a withdraw link
// (`lnurlw://host/path?k1=…`, `https://host/path?k1=…`, or `http://…onion`) or
// a bare spend (64-hex `k1`, or `cw1…`) with the issuer named by issuerHint.
//
// It verifies the spend locally: a bearer preimage must hash to the leaf's
// hashlock and produce the taproot output key the spend opens. No network call
// happens here, so a malformed or foreign note costs the issuer nothing.
func Parse(raw, issuerHint string) (Note, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxNoteInput {
		return Note{}, fmt.Errorf("%w: length %d", ErrMalformedNote, len(s))
	}

	var (
		note      Note
		spendText string
		link      *url.URL
		err       error
	)

	switch {
	case strings.HasPrefix(s, "lnurlw://"), strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		link, err = url.Parse(s)
		if err != nil {
			return Note{}, fmt.Errorf("%w: not a URL", ErrMalformedNote)
		}
		if link.Host == "" {
			return Note{}, fmt.Errorf("%w: no host in note URL", ErrMalformedNote)
		}
		q := link.Query()
		spendText = strings.TrimSpace(q.Get("k1"))
		note.Certificate = strings.TrimSpace(q.Get("c"))
		if spendText == "" {
			return Note{}, fmt.Errorf("%w: no k1 in note URL", ErrMalformedNote)
		}
		// LUD-25 lets a note URL carry `amount` for display and requires the
		// SERVICE to ignore it; so do we. Everything else in the query is
		// dropped when the redemption link is rebuilt below.
	default:
		spendText = s
	}

	if link == nil {
		hint := strings.TrimSpace(issuerHint)
		if hint == "" {
			return Note{}, fmt.Errorf("%w: a bare spend needs the issuer domain", ErrMalformedNote)
		}
		link, err = url.Parse("https://" + hint + "/")
		if err != nil {
			return Note{}, fmt.Errorf("%w: issuer %q is not a usable host", ErrMalformedNote, hint)
		}
		// http for an onion is legitimate; the hint names a host, so scheme is
		// fixed here and the .onion case is unreachable through this path.
	}

	domain, err := CanonicalDomain(link)
	if err != nil {
		return Note{}, err
	}
	if hint := strings.TrimSpace(issuerHint); hint != "" {
		hinted, herr := CanonicalDomain(&url.URL{Scheme: "https", Host: hint})
		if herr != nil || hinted != domain {
			return Note{}, fmt.Errorf("%w (%q vs %q)", ErrIssuerMismatch, domain, hint)
		}
	}
	note.Domain = domain

	switch scheme := strings.ToLower(link.Scheme); scheme {
	case "https", "lnurlw":
	case "http":
		if !strings.HasSuffix(domain, ".onion") {
			return Note{}, fmt.Errorf("%w: %q is cleartext http and not an onion service", ErrInsecureScheme, domain)
		}
	default:
		return Note{}, fmt.Errorf("%w: scheme %q", ErrInsecureScheme, link.Scheme)
	}

	// Rebuild the endpoint with an empty query: the redemption request adds
	// exactly the parameters LUD-25 allows, and nothing the note smuggled in.
	note.WithdrawURL = &url.URL{Scheme: "https", Host: domain, Path: link.Path}

	if err := note.setSpend(spendText); err != nil {
		return Note{}, err
	}
	return note, nil
}

// setSpend decodes a spend in either supported encoding and proves it opens the
// note's own output key.
func (n *Note) setSpend(spend string) error {
	switch {
	case isHex64(spend):
		pre, err := hex.DecodeString(strings.ToLower(spend))
		if err != nil {
			return fmt.Errorf("%w: k1", ErrMalformedNote)
		}
		copy(n.preimage[:], pre)
		n.Hash = sha256.Sum256(n.preimage[:])
		n.Kind = KindBearerPreimage
		n.Q = BearerOutputKey(n.Hash)
		return nil

	case strings.HasPrefix(spend, "cw1"):
		cb, err := DecodeCW1(spend)
		if err != nil {
			return err
		}
		// Only the unlocked bearer spend is supported. A timelock claim would
		// require the gate to assert its own clock (LUD-25 calls that custodial
		// policy, not consensus), so it is refused, never silently honoured.
		if cb.LockTime != 0 || cb.Sequence != 0xffffffff {
			return fmt.Errorf("%w: cw1 claims locktime %d sequence %d; only the unlocked bearer spend is supported",
				ErrUnsupportedSpend, cb.LockTime, cb.Sequence)
		}
		h, ok := BearerLeafHash(cb.Script)
		if !ok {
			return fmt.Errorf("%w: leaf is not the bearer OP_SHA256 <h> OP_EQUAL script", ErrUnsupportedSpend)
		}
		if len(cb.Witness) != 1 {
			return fmt.Errorf("%w: the bearer spend carries exactly one witness item, this one carries %d", ErrUnsupportedSpend, len(cb.Witness))
		}
		if len(cb.Witness[0]) != 32 {
			return fmt.Errorf("%w: the bearer preimage is 32 bytes, this witness item is %d", ErrMalformedNote, len(cb.Witness[0]))
		}
		if sum := sha256.Sum256(cb.Witness[0]); sum != h {
			// Provably worthless, and said so before any network call.
			return fmt.Errorf("%w: witness does not hash to the leaf's hashlock", ErrNotBearer)
		}
		q, err := ControlBlockOutputKey(cb.ControlBlock, cb.Script)
		if err != nil {
			return err
		}
		copy(n.preimage[:], cb.Witness[0])
		n.Hash = h
		n.Q = q
		n.Kind = KindBearerCW1
		return nil

	case strings.HasPrefix(spend, "ck1"):
		return fmt.Errorf("%w: ck1 key-path spends need a BIP-342 signature check this build does not carry", ErrUnsupportedSpend)

	default:
		return fmt.Errorf("%w: a spend is 64 hex characters (k1) or a cw1…/ck1… string", ErrMalformedNote)
	}
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// CanonicalDomain reduces an issuer URL to the `domain` string LUD-25 commits
// to: the host, lowercased, A-label, with no port, userinfo or trailing dot.
// This exact string appears in a `cs1` message, so a mismatch is a signature
// that can never verify.
func CanonicalDomain(u *url.URL) (string, error) {
	if u == nil || u.Host == "" {
		return "", fmt.Errorf("%w: no host", ErrMalformedNote)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("%w: empty host", ErrMalformedNote)
	}
	if !strings.Contains(host, ".") {
		return "", fmt.Errorf("%w: %q is not a fully qualified domain", ErrMalformedNote, host)
	}
	// A raw UTF-8 label would not match the string the issuer signed, and
	// comparing a punycode allowlist against a Unicode host is how two names
	// become one.
	for _, r := range host {
		if r > 127 {
			return "", fmt.Errorf("%w: %q is not in A-label (punycode) form", ErrMalformedNote, host)
		}
	}
	return host, nil
}

// CW1 is a decoded `cw1` payload:
//
//	u32 locktime || u32 sequence || (u16 len || item)* over script, control block, witness items
type CW1 struct {
	LockTime     uint32
	Sequence     uint32
	Script       []byte
	ControlBlock []byte
	Witness      [][]byte
}

// DecodeCW1 decodes LUD-25's `cw1` spend encoding (bech32m, hrp "cw").
func DecodeCW1(s string) (CW1, error) {
	var out CW1
	hrp, data, err := bech32.DecodeNoLimit(strings.TrimSpace(s))
	if err != nil {
		return out, fmt.Errorf("%w: cw1: %v", ErrMalformedNote, err)
	}
	if hrp != "cw" {
		return out, fmt.Errorf("%w: expected cw1, got %s1", ErrMalformedNote, hrp)
	}
	raw, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return out, fmt.Errorf("%w: cw1 payload", ErrMalformedNote)
	}
	if len(raw) < 12 {
		return out, fmt.Errorf("%w: cw1 payload is %d bytes", ErrMalformedNote, len(raw))
	}
	out.LockTime = uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	out.Sequence = uint32(raw[4])<<24 | uint32(raw[5])<<16 | uint32(raw[6])<<8 | uint32(raw[7])

	items, err := decodeLengthPrefixed(raw[8:])
	if err != nil {
		return out, err
	}
	// script, control block, then the witness items bottom of stack first.
	if len(items) < 2 {
		return out, fmt.Errorf("%w: cw1 needs a script and a control block", ErrMalformedNote)
	}
	out.Script = items[0]
	out.ControlBlock = items[1]
	out.Witness = items[2:]
	return out, nil
}

func decodeLengthPrefixed(b []byte) ([][]byte, error) {
	var items [][]byte
	for i := 0; i < len(b); {
		if i+2 > len(b) {
			return nil, fmt.Errorf("%w: cw1 truncated length prefix", ErrMalformedNote)
		}
		n := int(b[i])<<8 | int(b[i+1])
		i += 2
		if i+n > len(b) {
			return nil, fmt.Errorf("%w: cw1 length prefix overruns the payload", ErrMalformedNote)
		}
		items = append(items, b[i:i+n])
		i += n
	}
	return items, nil
}

// bearerLeafScript builds LUD-25's bearer leaf: OP_SHA256 <h> OP_EQUAL.
func bearerLeafScript(h [32]byte) []byte {
	// OP_SHA256 <32-byte h> OP_EQUAL
	script := make([]byte, 0, 35)
	script = append(script, 0xa8, 0x20)
	script = append(script, h[:]...)
	return append(script, 0x87)
}

// BearerLeafHash reports whether script is exactly the bearer leaf, returning
// its hashlock. Anything else is a script this build cannot evaluate.
func BearerLeafHash(script []byte) ([32]byte, bool) {
	var h [32]byte
	if len(script) != 35 || script[0] != 0xa8 || script[1] != 0x20 || script[34] != 0x87 {
		return h, false
	}
	copy(h[:], script[2:34])
	return h, true
}

// taggedHash is BIP-340's tagged hash: sha256(sha256(tag) || sha256(tag) || msg…).
func taggedHash(tag string, msg ...[]byte) [32]byte {
	t := sha256.Sum256([]byte(tag))
	h := sha256.New()
	h.Write(t[:])
	h.Write(t[:])
	for _, m := range msg {
		h.Write(m)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// tapleafHash is BIP-341's leaf hash with leaf version 0xc0:
// tagged_hash("TapLeaf", leafVersion || compact_size(script) || script).
func tapleafHash(script []byte) ([32]byte, error) {
	if len(script) >= 253 {
		// No supported leaf is this long; refuse rather than mis-encode the
		// compact size and silently commit to a different leaf.
		return [32]byte{}, fmt.Errorf("%w: leaf script is %d bytes", ErrUnsupportedSpend, len(script))
	}
	return taggedHash("TapLeaf", []byte{0xc0, byte(len(script))}, script), nil
}

// bearerInternalKey parses the NUMS point a bearer note commits to.
func bearerInternalKey() (*btcec.PublicKey, error) {
	raw, err := hex.DecodeString(bearerInternalKeyHex)
	if err != nil {
		return nil, err
	}
	// x-only: lift with the even-y prefix, as BIP-340 does.
	return btcec.ParsePubKey(append([]byte{0x02}, raw...))
}

// BearerOutputKey computes the note's x-only output key from its hashlock:
// Q = lift_x(NUMS) + tagged_hash("TapTweak", NUMS || tapleaf_hash)·G.
func BearerOutputKey(h [32]byte) [32]byte {
	q, _ := outputKeyFor(bearerLeafScript(h)) // the bearer leaf is always encodable
	return q
}

// ControlBlockOutputKey recomputes Q from a revealed leaf and its control block.
// A control block that is not a single-leaf bearer tree names a different note
// and is refused rather than partially trusted. For a single-leaf tree the leaf
// hash IS the merkle root, so recommitting to the witness's own script is the
// check that the control block and script belong together.
func ControlBlockOutputKey(cb, script []byte) ([32]byte, error) {
	if len(cb) != 33 {
		return [32]byte{}, fmt.Errorf("%w: control block is %d bytes; only the single-leaf bearer tree (33) is supported", ErrUnsupportedSpend, len(cb))
	}
	if cb[0]&0xfe != 0xc0 {
		return [32]byte{}, fmt.Errorf("%w: control block leaf version 0x%02x is not 0xc0", ErrUnsupportedSpend, cb[0])
	}
	if !equalBytes(cb[1:], mustHex(bearerInternalKeyHex)) {
		return [32]byte{}, fmt.Errorf("%w: control block names a non-bearer internal key", ErrNotBearer)
	}
	return outputKeyFor(script)
}

// outputKeyFor commits to script: Q = lift_x(NUMS) + tagged_hash("TapTweak", NUMS || tapleaf_hash(script))·G.
func outputKeyFor(script []byte) ([32]byte, error) {
	ik, err := bearerInternalKey()
	if err != nil {
		return [32]byte{}, fmt.Errorf("lnurlcash: internal key: %w", err)
	}
	leaf, err := tapleafHash(script)
	if err != nil {
		return [32]byte{}, err
	}
	xonly := ik.SerializeCompressed()[1:]
	tweak := taggedHash("TapTweak", xonly, leaf[:])

	var t btcec.ModNScalar
	if overflow := t.SetByteSlice(tweak[:]); overflow || t.IsZero() {
		return [32]byte{}, fmt.Errorf("lnurlcash: invalid taproot tweak")
	}
	tweakPoint := btcec.Generator()
	var jac btcec.JacobianPoint
	tweakPoint.AsJacobian(&jac)
	var scaled btcec.JacobianPoint
	btcec.ScalarMultNonConst(&t, &jac, &scaled)
	var internal btcec.JacobianPoint
	ik.AsJacobian(&internal)
	var sum btcec.JacobianPoint
	btcec.AddNonConst(&internal, &scaled, &sum)
	sum.ToAffine()
	out := btcec.NewPublicKey(&sum.X, &sum.Y)

	var res [32]byte
	copy(res[:], out.SerializeCompressed()[1:])
	return res, nil
}

// EncodeCP1 encodes Q as LUD-25's `cp1` (bech32m, above BIP-173's length limit).
func EncodeCP1(q [32]byte) (string, error) {
	data, err := bech32.ConvertBits(q[:], 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.EncodeM("cp", data)
}

// DecodeCP1 decodes a `cp1` (or a bearer note's 64-hex `h` short form) to Q.
func DecodeCP1(s string) ([32]byte, error) {
	s = strings.TrimSpace(s)
	if isHex64(s) {
		h, err := hex.DecodeString(strings.ToLower(s))
		if err != nil {
			return [32]byte{}, fmt.Errorf("%w: %v", ErrMalformedNote, err)
		}
		var hash [32]byte
		copy(hash[:], h)
		return BearerOutputKey(hash), nil
	}
	hrp, data, err := bech32.DecodeNoLimit(s)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: cp1: %v", ErrMalformedNote, err)
	}
	if hrp != "cp" {
		return [32]byte{}, fmt.Errorf("%w: expected cp1, got %s1", ErrMalformedNote, hrp)
	}
	raw, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("%w: cp1 payload must be 32 bytes", ErrMalformedNote)
	}
	var q [32]byte
	copy(q[:], raw)
	if _, err := btcec.ParsePubKey(append([]byte{0x02}, raw...)); err != nil {
		return [32]byte{}, fmt.Errorf("%w: cp1 is not the x coordinate of a curve point", ErrMalformedNote)
	}
	return q, nil
}

// Certificate is a decoded `cs1`: what one issuer attested about one note. The
// payload carries only r || s || recid || t, so the domain, Q and message are
// inputs to verification, not fields of the certificate — a certificate cannot
// state which note it is about, and pretending otherwise invites a caller to
// trust a self-declared binding.
type Certificate struct {
	AmountMSat uint64
	SignedAt   int64
	sig        []byte
}

// DecodeCertificate decodes a `cs1` without checking it. The signature is
// checked by Verify; a caller that only needs the claimed value uses this and
// MUST NOT treat the claim as evidence.
func DecodeCertificate(cs1 string) (Certificate, error) {
	var c Certificate
	s := strings.TrimSpace(cs1)
	// The prefix is `cs` followed by the amount (BOLT-11 style), so it is `cs1`
	// only when the amount happens to start with a 1. Checking for a literal
	// "cs1" would reject every certificate for 21u or 100n.
	if !strings.HasPrefix(s, "cs") || len(s) < 4 || s[2] < '0' || s[2] > '9' {
		return c, fmt.Errorf("%w: a certificate starts with cs<amount>", ErrMalformedNote)
	}
	hrp, data, err := bech32.DecodeNoLimit(s)
	if err != nil {
		return c, fmt.Errorf("%w: certificate: %v", ErrMalformedNote, err)
	}
	amount, err := parseCSAmount(hrp)
	if err != nil {
		return c, err
	}
	raw, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return c, fmt.Errorf("%w: certificate payload", ErrMalformedNote)
	}
	// LUD-25: r || s || recovery-id || u32 t, 69 bytes exactly.
	if len(raw) != 69 {
		return c, fmt.Errorf("%w: certificate payload is %d bytes, want 69", ErrMalformedNote, len(raw))
	}
	if raw[64] > 3 {
		return c, fmt.Errorf("%w: certificate recovery id %d is out of range", ErrMalformedNote, raw[64])
	}
	c.AmountMSat = amount
	c.SignedAt = int64(uint32(raw[65])<<24 | uint32(raw[66])<<16 | uint32(raw[67])<<8 | uint32(raw[68]))
	// RecoverCompact wants the header byte FIRST (27 + 4 for a compressed key,
	// plus the recovery id), then R and S — the lnd/Core-Lightning `signmessage`
	// layout, which LUD-25 reorders into r || s || recid for the wire.
	c.sig = make([]byte, 65)
	c.sig[0] = 27 + 4 + raw[64]
	copy(c.sig[1:33], raw[:32])
	copy(c.sig[33:65], raw[32:64])
	return c, nil
}

// Message is the exact string LUD-25 signs.
func certificateMessage(domain string, amountMSat uint64, q [32]byte, t int64) []byte {
	return []byte(fmt.Sprintf("LNURLcash:%s:%d:%x:%d", domain, amountMSat, q[:], t))
}

// Verify checks a certificate against the issuer's published mintPubkey, the
// note's own domain, Q and the amount this gate recorded.
//
// It answers only "this issuer attested this Q at this value at time t". It is
// NOT a grant condition and the result must never be used as one: a certificate
// says nothing about whether the note still holds, because the previous holder
// still knows the spend.
//
// maxFutureSkewSeconds bounds how far ahead of the gate's clock a `t` may be:
// LUD-25 forbids a SERVICE signing ahead of its own clock, so a future-dated
// certificate is evidence the answer is not this issuer's.
func Verify(c Certificate, mintPubkeyHex, domain string, q [32]byte, amountMSat uint64, nowUnix int64, maxFutureSkewSeconds int64) error {
	if c.AmountMSat != amountMSat {
		return fmt.Errorf("lnurlcash: certificate attests %d msat, this gate recorded %d msat", c.AmountMSat, amountMSat)
	}
	if c.SignedAt > nowUnix+maxFutureSkewSeconds {
		return fmt.Errorf("lnurlcash: certificate timestamp %d is %ds ahead of the gate's clock", c.SignedAt, c.SignedAt-nowUnix)
	}

	want, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(mintPubkeyHex)), "0x"))
	if err != nil || len(want) != 33 {
		return fmt.Errorf("lnurlcash: issuer mintPubkey must be a 33-byte compressed key")
	}

	msg := certificateMessage(domain, amountMSat, q, c.SignedAt)
	first := sha256.Sum256(append([]byte("Lightning Signed Message:"), msg...))
	digest := sha256.Sum256(first[:])

	recovered, _, err := ecdsa.RecoverCompact(c.sig, digest[:])
	if err != nil {
		return fmt.Errorf("lnurlcash: certificate signature does not recover: %w", err)
	}
	if !equalBytes(recovered.SerializeCompressed(), want) {
		return fmt.Errorf("lnurlcash: certificate was signed by a different key than the issuer's mintPubkey")
	}
	return nil
}

// parseCSAmount decodes a `cs1` human-readable amount ("cs10n" = 1000 msat, per
// BOLT-11's rules as LUD-25 specifies).
func parseCSAmount(hrp string) (uint64, error) {
	if !strings.HasPrefix(hrp, "cs") {
		return 0, fmt.Errorf("%w: certificate HRP %q does not start with cs", ErrMalformedNote, hrp)
	}
	rest := hrp[2:]
	if rest == "" {
		return 0, fmt.Errorf("%w: certificate carries no amount", ErrMalformedNote)
	}
	// msat per unit of the multiplier suffix.
	mult := uint64(100_000_000_000) // 1 BTC in msat
	switch rest[len(rest)-1] {
	case 'm':
		mult = 100_000_000 // 1 mBTC in msat
		rest = rest[:len(rest)-1]
	case 'u':
		mult = 100_000 // 1 uBTC in msat
		rest = rest[:len(rest)-1]
	case 'n':
		mult = 100 // 1 nBTC in msat
		rest = rest[:len(rest)-1]
	case 'p':
		return 0, fmt.Errorf("%w: certificate amount in pico-bitcoin is below one millisatoshi", ErrMalformedNote)
	}
	if rest == "" || len(rest) > 12 {
		return 0, fmt.Errorf("%w: certificate amount %q is not a plain integer", ErrMalformedNote, hrp)
	}
	var v uint64
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: certificate amount %q is not a plain integer", ErrMalformedNote, hrp)
		}
		v = v*10 + uint64(r-'0')
	}
	if v != 0 && mult > ^uint64(0)/v {
		return 0, fmt.Errorf("%w: certificate amount %q overflows", ErrMalformedNote, hrp)
	}
	return v * mult, nil
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("lnurlcash: package constant is not hex: " + s)
	}
	return b
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
