package bearerport

import (
	"bytes"
	"strconv"
)

// Instrument is a decoded bearer instrument, expressed in backend-neutral
// terms. Whatever the encoding was (Cashu cashuA/cashuB, a fedimint note, a
// miner nonce), the port and its callers only ever see these facts.
//
// Notice which facts are NOT here: no proof, no signature, no mint keyset.
// The port never parses an instrument's internals — that is the authority's
// business. This is what makes the seam instrument-agnostic.
type Instrument interface {
	// Kind is the encoding family actually observed on the wire
	// ("cashuA", "cashuB", "fed1", "pow1"). Chosen by sniffing, never by
	// configuration — a config-declared kind is a way to be wrong.
	Kind() string
	// Authority is the namespaced target key ("cashu:<url>",
	// "fedi:<federation-id>", "pow:<miner>"). The prefix exists so a
	// federation id and a mint URL can never be spelled the same way.
	Authority() string
	// Unit is the denomination ("sat", "work", ...). Spending across units
	// is refused, not converted.
	Unit() string
	// FaceValue is the amount the INSTRUMENT CLAIMS. It is unverified and
	// must never be used for accounting. Kept only so the port can compare it
	// against the authority's answer.
	FaceValue() uint64
	// AttestedAmount is the amount the AUTHORITY's own log records for this
	// output. It is the only number the port may credit, and it is zero when
	// no authority recognised the instrument (in which case Acquire refuses
	// rather than guessing).
	AttestedAmount() uint64
	// Nullifier is the anti-double-spend tag: the handle the authority is
	// asked about (NUT-07). Derived, never carried as a caller-supplied field.
	Nullifier() string
	// Commitment identifies this output in the authority's own issuance
	// memory. It is the only key recovery (NUT-09) is asked about.
	Commitment() string
	// ReissueBlob is the opaque bytes the authority consumes. The port does
	// not interpret it.
	//
	// Note what a decoded instrument does NOT carry: the seed. The seed is the
	// port's own recovery material and stays in the port's store; an
	// instrument holds only derived, one-way values. There is deliberately no
	// Root() accessor to misuse.
	ReissueBlob() []byte
}

type instrument struct {
	kind      string
	authority string
	unit      string
	face      uint64
	attested  uint64
	commit    string
	nullifier string
	blob      []byte
}

func (i *instrument) Kind() string           { return i.kind }
func (i *instrument) Authority() string      { return i.authority }
func (i *instrument) Unit() string           { return i.unit }
func (i *instrument) FaceValue() uint64      { return i.face }
func (i *instrument) AttestedAmount() uint64 { return i.attested }
func (i *instrument) Nullifier() string      { return i.nullifier }
func (i *instrument) Commitment() string     { return i.commit }
func (i *instrument) ReissueBlob() []byte    { return i.blob }

// encodeBlob builds the on-wire form used by the demo's authorities:
//
//	<kind>|<commitment>:<amount>[:claim<claimed>]
//
// It is deliberately boring. The point of the demo is the PORT's behaviour,
// not an encoding contest — and the port never reads inside this string.
func encodeBlob(kind, commitment string, amount, claimed uint64) []byte {
	s := kind + "|" + commitment + ":" + strconv.FormatUint(amount, 10)
	if claimed != 0 {
		s += ":claim" + strconv.FormatUint(claimed, 10)
	}
	return []byte(s)
}

// sniffAndSplit extracts the kind, the commitment and the instrument's
// self-declared value. A malformed or unrecognised encoding is ErrUnknownKind.
func sniffAndSplit(blob []byte) (kind, commitment string, face uint64, err error) {
	bar := bytes.IndexByte(blob, '|')
	if bar <= 0 {
		return "", "", 0, ErrUnknownKind
	}
	kind = string(blob[:bar])
	switch kind {
	case "cashuA", "cashuB", "fed1", "pow1":
	default:
		return "", "", 0, ErrUnknownKind
	}
	parts := bytes.Split(blob[bar+1:], []byte(":"))
	if len(parts) < 2 {
		return "", "", 0, ErrUnknownKind
	}
	commitment = string(parts[0])
	n, perr := strconv.ParseUint(string(parts[1]), 10, 64)
	if perr != nil {
		return "", "", 0, ErrUnknownKind
	}
	return kind, commitment, n, nil
}
