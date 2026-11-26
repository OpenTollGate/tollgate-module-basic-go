// Exit-admission gate for fips2go exits.
//
// This layer models, as pure data and pure functions, the durable entitlement
// record a TollGate-run exit needs on top of the upstream fips-exit daemon.
// fr34aky/fips-exit (2fe3901) admits peers from a plain text clients file: one
// FIPS IPv6 address per line, an optional "# name" comment, whole-line "#"
// comments, re-read on SIGHUP by fips-exitd. It has no API, no expiry, no
// per-peer entitlement record and no restart recovery of grants. That gap is
// what this layer fills on OUR side; the upstream project is not modified.
//
// Nothing here is wired to anything. Concretely:
//   - No HTTP, payment, nftables, process signalling or filesystem path lives
//     here. Persistence is expressed only through the Store seam below, and a
//     future Linux implementation of it owns the real path and the temp file.
//   - Signalling fips-exitd (SIGHUP after a changed clients file) and taking the
//     purchase that produces a GrantRequest are separate steps for callers.
//   - Because this layer performs no I/O and observes no live exit, a passing
//     test here proves nothing about a running exit: it proves only that the
//     decisions and the file bytes are right for the inputs supplied.
//
// Accounting is deliberately split:
//   - BillingTime authorises a wall-clock window and nothing else. Any byte
//     counter a caller supplies for such a peer is ignored, and bytes_used
//     stays zero.
//   - BillingBytes authorises up to ByteBudget bytes. This layer NEVER invents a
//     counter: it records what an Observation reports, monotonic in the caller's
//     favour (a lower reported value never rewinds usage), and an unobserved peer
//     keeps the usage already on record, so a missing observation can neither
//     grant nor revoke. A caller that cannot read real counters must not select
//     byte billing at all.
package fips_exit

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
)

// ErrNoState reports that no entitlement state has been persisted yet. It is a
// normal cold start, not a failure: the zero State admits nobody.
var ErrNoState = errors.New("fips_exit: no persisted admission state")

// Store is the only way this package reaches persisted state. A future Linux
// implementation must, in WriteAtomic, write a temporary file in the same
// directory as the target, fsync it, rename it over the target and fsync the
// directory, so a crash leaves either the old or the new bytes and never a
// truncated file. It must return ErrNoState (wrapped or bare) from Read when
// nothing has been persisted yet, and must surface every other error.
type Store interface {
	Read() ([]byte, error)
	WriteAtomic([]byte) error
}

// PeerKey is a decoded 32-byte x-only Nostr public key. It is the identity of a
// peer; outer/inner FIPS addresses are derived from it and are not identities.
type PeerKey [32]byte

// IsZero reports the all-zero key, which is not a valid x-only point.
func (k PeerKey) IsZero() bool { return k == PeerKey{} }

// Hex returns the lower-case 64-character hex spelling.
func (k PeerKey) Hex() string { return hex.EncodeToString(k[:]) }

// OuterAddress returns the FIPS outer IPv6 address the exit would see.
func (k PeerKey) OuterAddress() netip.Addr {
	outer, _ := Derive(k)
	return outer
}

// innerAddress returns the exit-tunnel inner IPv4 address for this peer.
func (k PeerKey) innerAddress() netip.Addr {
	_, inner := Derive(k)
	return inner
}

// ParsePeerKey decodes a hex or npub spelling of a 32-byte x-only public key.
// Curve membership is NOT checked here; the FIPS peer handshake does that. An
// nsec, an nprofile or an all-zero key is refused.
func ParsePeerKey(s string) (PeerKey, error) {
	s = strings.TrimSpace(s)
	var out PeerKey
	switch {
	case len(s) == 64:
		raw, err := hex.DecodeString(s)
		if err != nil {
			return out, fmt.Errorf("fips_exit: peer key is not hex: %w", err)
		}
		copy(out[:], raw)
	case strings.HasPrefix(s, "npub1"):
		// bech32 accepts an all-upper spelling; keep the canonical form only so
		// two spellings can never mean two different records.
		if s != strings.ToLower(s) {
			return out, errors.New("fips_exit: npub must be lower-case")
		}
		prefix, value, err := nip19.Decode(s)
		if err != nil {
			return out, fmt.Errorf("fips_exit: decode npub: %w", err)
		}
		if prefix != "npub" {
			return out, fmt.Errorf("fips_exit: expected npub, got %q", prefix)
		}
		keyHex, ok := value.(string)
		if !ok || len(keyHex) != 64 {
			return out, errors.New("fips_exit: npub did not decode to a 32-byte key")
		}
		raw, err := hex.DecodeString(keyHex)
		if err != nil {
			return out, fmt.Errorf("fips_exit: npub payload is not hex: %w", err)
		}
		copy(out[:], raw)
	default:
		return out, fmt.Errorf("fips_exit: peer key must be 64 hex characters or an npub, got %q", s)
	}
	if out.IsZero() {
		return out, errors.New("fips_exit: the all-zero key is not an identity")
	}
	return out, nil
}

// BillingMode selects what a grant is bounded by.
type BillingMode string

const (
	// BillingTime bounds a grant by its wall-clock window only.
	BillingTime BillingMode = "time"
	// BillingBytes bounds a grant by a caller-supplied byte counter as well.
	BillingBytes BillingMode = "bytes"
)

// Entitlement is one durable grant. Revision is the state revision that
// produced this record, so a record can never look older than its own write.
type Entitlement struct {
	Key        PeerKey     `json:"key"`
	Name       string      `json:"name,omitempty"`
	Mode       BillingMode `json:"mode"`
	GrantedAt  time.Time   `json:"granted_at"`
	ExpiresAt  time.Time   `json:"expires_at"`
	ByteBudget uint64      `json:"byte_budget,omitempty"`
	BytesUsed  uint64      `json:"bytes_used,omitempty"`
	Revision   uint64      `json:"revision"`
}

// ClientsEntry is one line of the fips-exit clients file.
type ClientsEntry struct {
	Addr netip.Addr
	Name string
}

// InvalidLine reports a clients-file line this package could not represent.
// Skipping such a line is deliberate: the upstream daemon skips it too, and a
// single typo must not take the whole admission list down.
type InvalidLine struct {
	Line   int
	Text   string
	Reason string
}

// ClientsFile is a parsed clients file. Invalid is reported, never fatal.
type ClientsFile struct {
	Entries []ClientsEntry
	Invalid []InvalidLine
}

// State is the durable admission state. Revision is monotonic and advances by
// exactly one per entitlement change; it does not advance for bookkeeping about
// unmanaged lines or the last written file, which are not decisions.
// Unmanaged holds operator-added clients-file lines this layer does not own and
// therefore must never drop. Snapshot is the clients file this layer last wrote
// or adopted, and is the base for reporting changes so that a reload with no
// decision never churns the file.
type State struct {
	Revision     uint64         `json:"revision"`
	Entitlements []Entitlement  `json:"entitlements"`
	Unmanaged    []ClientsEntry `json:"unmanaged,omitempty"`
	Snapshot     []ClientsEntry `json:"snapshot,omitempty"`
}

// Entitlement returns the grant held for key.
func (s State) Entitlement(k PeerKey) (Entitlement, bool) {
	for _, e := range s.Entitlements {
		if e.Key == k {
			return e, true
		}
	}
	return Entitlement{}, false
}

// sortedEntitlements returns a copy ordered by key, the order used on disk.
func (s State) sortedEntitlements() []Entitlement {
	out := append([]Entitlement(nil), s.Entitlements...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Key[:], out[j].Key[:]) < 0 })
	return out
}

// isExitAddr reports whether addr can appear in a fips-exit clients file.
// Upstream only requires an fd-prefixed IPv6 address, so that is all this
// checks: it cannot tell a derived peer address from a hand-typed one.
func isExitAddr(addr netip.Addr) bool {
	return addr.Is6() && addr.IsValid() && !addr.Is4In6() && addr.As16()[0] == 0xfd
}

// validName reports whether name can be rendered as a trailing comment. A
// name holding '#' or a control character would change the meaning of the line.
func validName(name string) bool {
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r == '#' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ParseClientsFile parses the fips-exit clients-file format. One fd-prefixed
// IPv6 address per line, then an optional "# name" comment; whole-line comments
// and blank lines are ignored. CRLF and a missing final newline are accepted.
// Lines the format cannot express, and repeated addresses, are reported in
// Invalid rather than failing the parse.
func ParseClientsFile(data []byte) ClientsFile {
	var out ClientsFile
	seen := make(map[netip.Addr]bool)
	for i, raw := range strings.Split(string(data), "\n") {
		line := i + 1
		text := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		addrText, rest := trimmed, ""
		if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
			addrText, rest = trimmed[:idx], strings.TrimSpace(trimmed[idx+1:])
		}
		addrs := strings.TrimSpace(addrText)
		addr, err := netip.ParseAddr(addrs)
		if err != nil {
			out.Invalid = append(out.Invalid, InvalidLine{line, text, "not an IP address"})
			continue
		}
		if !isExitAddr(addr) {
			out.Invalid = append(out.Invalid, InvalidLine{line, text, "not a fd-prefixed IPv6 address"})
			continue
		}
		name := ""
		if rest != "" {
			if !strings.HasPrefix(rest, "#") {
				out.Invalid = append(out.Invalid, InvalidLine{line, text, "trailing text is not a # comment"})
				continue
			}
			name = strings.TrimSpace(rest[1:])
			if !validName(name) {
				out.Invalid = append(out.Invalid, InvalidLine{line, text, "name contains an unsafe character"})
				continue
			}
		}
		if seen[addr] {
			out.Invalid = append(out.Invalid, InvalidLine{line, text, "duplicate address"})
			continue
		}
		seen[addr] = true
		out.Entries = append(out.Entries, ClientsEntry{Addr: addr, Name: name})
	}
	return out
}

// RenderClientsFile renders the clients-file bytes: "addr\n" or
// "addr # name\n", always LF-terminated, no header, no trailing blank line. An
// empty file renders as zero bytes. Ambiguous input is refused rather than
// written: the daemon would silently skip what it cannot parse.
func RenderClientsFile(f ClientsFile) ([]byte, error) {
	var buf bytes.Buffer
	seen := make(map[netip.Addr]bool, len(f.Entries))
	for _, e := range f.Entries {
		if !isExitAddr(e.Addr) {
			return nil, fmt.Errorf("fips_exit: %q is not a fd-prefixed IPv6 address", e.Addr)
		}
		if !validName(e.Name) {
			return nil, fmt.Errorf("fips_exit: peer name %q cannot be rendered", e.Name)
		}
		if seen[e.Addr] {
			return nil, fmt.Errorf("fips_exit: address %q appears twice", e.Addr)
		}
		seen[e.Addr] = true
		buf.WriteString(e.Addr.String())
		if e.Name != "" {
			buf.WriteString(" # ")
			buf.WriteString(e.Name)
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// line renders one entry exactly as RenderClientsFile would.
func (e ClientsEntry) line() string {
	if e.Name == "" {
		return e.Addr.String()
	}
	return e.Addr.String() + " # " + e.Name
}

// EncodeState serialises state deterministically: entitlements sorted by key,
// UTF-8 JSON, two-space indent, one trailing newline. Re-encoding a decoded
// state reproduces the same bytes.
func EncodeState(s State) ([]byte, error) {
	type wire struct {
		Revision     uint64         `json:"revision"`
		Entitlements []Entitlement  `json:"entitlements"`
		Unmanaged    []ClientsEntry `json:"unmanaged,omitempty"`
		Snapshot     []ClientsEntry `json:"snapshot,omitempty"`
	}
	entitlements := s.sortedEntitlements()
	if entitlements == nil {
		entitlements = []Entitlement{}
	}
	for _, e := range entitlements {
		if err := validateEntitlement(e, s.Revision); err != nil {
			return nil, err
		}
	}
	for _, e := range append(append([]ClientsEntry(nil), s.Unmanaged...), s.Snapshot...) {
		if !isExitAddr(e.Addr) {
			return nil, fmt.Errorf("fips_exit: %q is not a fd-prefixed IPv6 address", e.Addr)
		}
	}
	payload, err := json.MarshalIndent(wire{
		Revision:     s.Revision,
		Entitlements: entitlements,
		Unmanaged:    s.Unmanaged,
		Snapshot:     s.Snapshot,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("fips_exit: encode state: %w", err)
	}
	return append(payload, '\n'), nil
}

// DecodeState parses persisted state, refusing unknown fields, duplicate keys
// and any record this package would not itself have granted. Corrupt state is
// an error, never an empty state: silently starting empty would be safe from
// the file's point of view but would hide a lost ledger from the operator.
func DecodeState(data []byte) (State, error) {
	type wire struct {
		Revision     uint64         `json:"revision"`
		Entitlements []Entitlement  `json:"entitlements"`
		Unmanaged    []ClientsEntry `json:"unmanaged,omitempty"`
		Snapshot     []ClientsEntry `json:"snapshot,omitempty"`
	}
	var w wire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return State{}, fmt.Errorf("fips_exit: decode state: %w", err)
	}
	out := State{Revision: w.Revision, Entitlements: w.Entitlements, Unmanaged: w.Unmanaged, Snapshot: w.Snapshot}
	seen := make(map[PeerKey]bool, len(out.Entitlements))
	for _, e := range out.Entitlements {
		if err := validateEntitlement(e, out.Revision); err != nil {
			return State{}, err
		}
		if seen[e.Key] {
			return State{}, fmt.Errorf("fips_exit: state holds two records for key %s", e.Key.Hex())
		}
		seen[e.Key] = true
	}
	for _, e := range append(append([]ClientsEntry(nil), out.Unmanaged...), out.Snapshot...) {
		if !isExitAddr(e.Addr) {
			return State{}, fmt.Errorf("fips_exit: state holds %q, not a fd-prefixed IPv6 address", e.Addr)
		}
	}
	return out, nil
}

// validateEntitlement re-checks the invariants Grant enforces, so persisted
// state cannot contain a record this package would have refused to create.
func validateEntitlement(e Entitlement, stateRevision uint64) error {
	switch {
	case e.Key.IsZero():
		return errors.New("fips_exit: entitlement has no peer key")
	case e.Revision == 0 || e.Revision > stateRevision:
		return fmt.Errorf("fips_exit: entitlement revision %d outside 1..%d", e.Revision, stateRevision)
	case e.GrantedAt.IsZero() || e.ExpiresAt.IsZero():
		return errors.New("fips_exit: entitlement window is unset")
	case !e.ExpiresAt.After(e.GrantedAt):
		return errors.New("fips_exit: entitlement window is empty or inverted")
	case !validName(e.Name):
		return fmt.Errorf("fips_exit: entitlement name %q cannot be rendered", e.Name)
	}
	switch e.Mode {
	case BillingTime:
		if e.ByteBudget != 0 {
			return errors.New("fips_exit: time-billed entitlement carries a byte budget")
		}
	case BillingBytes:
		if e.ByteBudget == 0 {
			return errors.New("fips_exit: byte-billed entitlement has no budget")
		}
		if e.BytesUsed > e.ByteBudget {
			return errors.New("fips_exit: recorded usage exceeds the byte budget")
		}
	default:
		return fmt.Errorf("fips_exit: unknown billing mode %q", e.Mode)
	}
	return nil
}

// LoadState reads persisted state through the store. ErrNoState yields the zero
// State, which admits nobody: a missing file is a cold start, not free access.
func LoadState(store Store) (State, error) {
	data, err := store.Read()
	if errors.Is(err, ErrNoState) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("fips_exit: read state: %w", err)
	}
	return DecodeState(data)
}

// SaveState writes state through the store's single atomic write. Nothing is
// written when the encoding fails, so a refused record never reaches disk.
func SaveState(store Store, s State) error {
	data, err := EncodeState(s)
	if err != nil {
		return err
	}
	if err := store.WriteAtomic(data); err != nil {
		return fmt.Errorf("fips_exit: write state: %w", err)
	}
	return nil
}

// GrantRequest is one purchase the TollGate side has already settled. Times are
// supplied by the caller; this package never reads a clock.
type GrantRequest struct {
	Key        PeerKey
	Name       string
	Mode       BillingMode
	GrantedAt  time.Time
	ExpiresAt  time.Time
	ByteBudget uint64
}

// Grant appends one entitlement and advances the state revision by one. It
// refuses a zero or duplicate key, an empty or inverted window, a byte mode
// with no budget, a time mode with a budget, and an inner-IPv4 collision.
//
// The collision rule exists because Derive maps a 256-bit key onto the inner
// IPv4 100.64.0.0/10 with only 22 identity bits, so two distinct peers can share
// an inner address. The file keys peers by OUTER address, which is collision
// free in practice; an inner collision would nevertheless make NAT and
// accounting ambiguous, so it is refused instead of papered over.
func Grant(state State, req GrantRequest) (State, error) {
	if req.Key.IsZero() {
		return State{}, errors.New("fips_exit: cannot grant the all-zero key")
	}
	ent := Entitlement{
		Key:        req.Key,
		Name:       req.Name,
		Mode:       req.Mode,
		GrantedAt:  req.GrantedAt,
		ExpiresAt:  req.ExpiresAt,
		ByteBudget: req.ByteBudget,
		Revision:   state.Revision + 1,
	}
	if err := validateEntitlement(ent, ent.Revision); err != nil {
		return State{}, err
	}
	inner := req.Key.innerAddress()
	for _, existing := range state.Entitlements {
		if existing.Key == req.Key {
			return State{}, fmt.Errorf("fips_exit: %s already holds a grant (renew instead)", req.Key.Hex())
		}
		if existing.Key.innerAddress() == inner {
			return State{}, fmt.Errorf("fips_exit: %s shares inner IPv4 %s with another peer",
				req.Key.Hex(), inner)
		}
	}
	out := State{
		Revision:     ent.Revision,
		Entitlements: append(append([]Entitlement(nil), state.Entitlements...), ent),
		Unmanaged:    append([]ClientsEntry(nil), state.Unmanaged...),
		Snapshot:     append([]ClientsEntry(nil), state.Snapshot...),
	}
	return out, nil
}

// Verdict is the decision for one peer at one moment.
type Verdict string

const (
	// Keep means the grant is live and the peer stays admitted.
	Keep Verdict = "keep"
	// RevokeExpired means the wall-clock window has closed.
	RevokeExpired Verdict = "revoke-expired"
	// RevokeExhausted means the byte budget has been reached.
	RevokeExhausted Verdict = "revoke-exhausted"
	// Renew means no live grant is held, so admission requires a new purchase.
	Renew Verdict = "renew"
)

// VerdictFor decides one peer. observedBytes is the caller's counter for this
// peer, used only under BillingBytes; pass the recorded value to ask about the
// grant as it stands. Expiry is inclusive: at ExpiresAt the grant is gone.
func VerdictFor(state State, now time.Time, key PeerKey, observedBytes uint64) Verdict {
	ent, ok := state.Entitlement(key)
	if !ok {
		return Renew
	}
	if !now.Before(ent.ExpiresAt) {
		return RevokeExpired
	}
	if ent.Mode == BillingBytes && max(ent.BytesUsed, observedBytes) >= ent.ByteBudget {
		return RevokeExhausted
	}
	return Keep
}

// Observation is one peer the caller saw, with the counter it measured. Invalid
// marks an observation the caller could not vouch for (for example a reset
// counter); Apply refuses it rather than guessing what the peer used.
type Observation struct {
	Key       PeerKey
	BytesUsed uint64
	Invalid   string
}

// Outcome is the verdict Apply reached for one peer.
type Outcome struct {
	Key     PeerKey
	Verdict Verdict
}

// Applied is the result of one Apply pass.
type Applied struct {
	State    State
	File     ClientsFile
	Rendered []byte
	// Changed lists, in ascending address order, every peer whose clients-file
	// line differs from the last written file. A peer that reached neither its
	// expiry nor its budget produces no entry, so a reload never churns.
	Changed  []netip.Addr
	Outcomes []Outcome
}

// Apply recomputes the clients file from state plus the caller's observations.
// Revoked peers are dropped from the state, not merely from the file, so no
// later pass can resurrect them. Unmanaged operator lines are carried through.
// The state revision advances only when an entitlement or a written line
// changes, so a no-op pass leaves both the state and the file untouched.
func Apply(state State, now time.Time, observations []Observation) (Applied, error) {
	seen := make(map[PeerKey]bool, len(observations))
	for _, obs := range observations {
		if obs.Invalid != "" {
			return Applied{}, fmt.Errorf("fips_exit: unusable observation for %s: %s", obs.Key.Hex(), obs.Invalid)
		}
		if obs.Key.IsZero() {
			return Applied{}, errors.New("fips_exit: observation has no peer key")
		}
		if seen[obs.Key] {
			return Applied{}, fmt.Errorf("fips_exit: %s observed twice in one pass", obs.Key.Hex())
		}
		seen[obs.Key] = true
	}

	observed := make(map[PeerKey]uint64, len(observations))
	for _, obs := range observations {
		observed[obs.Key] = obs.BytesUsed
	}

	next := State{Revision: state.Revision, Unmanaged: append([]ClientsEntry(nil), state.Unmanaged...)}
	var outcomes []Outcome
	entitlementsChanged := false
	for _, ent := range state.sortedEntitlements() {
		used := ent.BytesUsed
		if ent.Mode == BillingBytes {
			if reported, ok := observed[ent.Key]; ok && reported > used {
				used = reported
			}
		}
		// An unobserved peer keeps the counter on record; only a live grant is
		// ever written, so silence about a counter never revokes anyone.
		verdict := VerdictFor(state, now, ent.Key, used)
		outcomes = append(outcomes, Outcome{Key: ent.Key, Verdict: verdict})
		switch verdict {
		case RevokeExpired, RevokeExhausted:
			entitlementsChanged = true
			continue
		}
		ent.BytesUsed = used
		ent.Revision = state.Revision
		next.Entitlements = append(next.Entitlements, ent)
	}

	for _, obs := range observations {
		if _, ok := state.Entitlement(obs.Key); !ok {
			outcomes = append(outcomes, Outcome{Key: obs.Key, Verdict: Renew})
		}
	}

	// Live grants first, then the operator's own lines.
	wanted := make([]ClientsEntry, 0, len(next.Entitlements)+len(next.Unmanaged))
	for _, ent := range next.sortedEntitlements() {
		wanted = append(wanted, ClientsEntry{Addr: ent.Key.OuterAddress(), Name: ent.Name})
	}
	wanted = append(wanted, next.Unmanaged...)

	file := ClientsFile{Entries: orderEntries(state.Snapshot, wanted)}
	rendered, err := RenderClientsFile(file)
	if err != nil {
		return Applied{}, err
	}
	// A rename changes no entitlement set relative to the incoming state, but it
	// does change what gets written, so the revision must advance for it too.
	if entitlementsChanged || !sameEntitlements(state, next) || !sameEntries(state.Snapshot, file.Entries) {
		next.Revision++
		for i := range next.Entitlements {
			next.Entitlements[i].Revision = next.Revision
		}
	}
	next.Snapshot = append([]ClientsEntry(nil), file.Entries...)
	changed := changedEntries(state.Snapshot, file.Entries)

	return Applied{
		State:    next,
		File:     file,
		Rendered: rendered,
		Changed:  changed,
		Outcomes: outcomes,
	}, nil
}

// sameEntries compares two clients-file line lists, order included.
func sameEntries(a, b []ClientsEntry) bool {
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

// sameEntitlements compares two states by entitlement content, ignoring order.
func sameEntitlements(a, b State) bool {
	as, bs := a.sortedEntitlements(), b.sortedEntitlements()
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		x, y := as[i], bs[i]
		if x.Key != y.Key || x.Name != y.Name || x.Mode != y.Mode ||
			!x.GrantedAt.Equal(y.GrantedAt) || !x.ExpiresAt.Equal(y.ExpiresAt) ||
			x.ByteBudget != y.ByteBudget || x.BytesUsed != y.BytesUsed {
			return false
		}
	}
	return true
}

// orderEntries keeps the previous order for addresses it already had and
// appends new ones in ascending address order, so a reload never reorders lines.
func orderEntries(previous, wanted []ClientsEntry) []ClientsEntry {
	byAddr := make(map[netip.Addr]ClientsEntry, len(wanted))
	for _, e := range wanted {
		byAddr[e.Addr] = e
	}
	out := make([]ClientsEntry, 0, len(wanted))
	for _, e := range previous {
		if want, ok := byAddr[e.Addr]; ok {
			out = append(out, want)
			delete(byAddr, e.Addr)
		}
	}
	rest := make([]ClientsEntry, 0, len(byAddr))
	for _, e := range byAddr {
		rest = append(rest, e)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Addr.Compare(rest[j].Addr) < 0 })
	return append(out, rest...)
}

// changedEntries lists addresses whose rendered line differs, ascending.
func changedEntries(previous, current []ClientsEntry) []netip.Addr {
	before := make(map[netip.Addr]string, len(previous))
	for _, e := range previous {
		before[e.Addr] = e.line()
	}
	after := make(map[netip.Addr]string, len(current))
	for _, e := range current {
		after[e.Addr] = e.line()
	}
	var changed []netip.Addr
	for addr, line := range after {
		if old, ok := before[addr]; !ok || old != line {
			changed = append(changed, addr)
		}
	}
	for addr := range before {
		if _, ok := after[addr]; !ok {
			changed = append(changed, addr)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Compare(changed[j]) < 0 })
	return changed
}

// ReconcileReport is what the caller must look at after a restart.
type ReconcileReport struct {
	// NeedsGrant lists observed lines with no live grant behind them. These are
	// operator or foreign lines, not customers: the exit must not admit them on
	// the strength of their presence in the file alone.
	NeedsGrant []ClientsEntry
	// Revoked lists addresses whose grant is no longer live. Their lines are
	// removed, never re-adopted as unmanaged: a stale grant must not be able to
	// keep a line alive across a restart.
	Revoked []netip.Addr
	// Conflicts lists observed lines that name a managed address differently
	// from its durable record. The record wins; the line is rewritten.
	Conflicts []Conflict
}

// Conflict records a managed address the clients file described differently.
type Conflict struct {
	Addr         netip.Addr
	ObservedName string
	DurableName  string
}

// Reconcile folds a persisted state and an observed clients file together at
// startup. Live grants keep their lines; expired or exhausted grants are dropped
// from both the state and the output, so a stale grant is never resurrected and
// a restart never grants free access. Lines this layer does not own are
// preserved verbatim and reported, never silently adopted as entitlements:
// an address in the file cannot be inverted into a peer key, so there is nothing
// to adopt. The returned State carries them as Unmanaged.
func Reconcile(state State, now time.Time, observed ClientsFile) (State, ClientsFile, ReconcileReport, error) {
	seen := make(map[netip.Addr]bool, len(observed.Entries))
	for _, e := range observed.Entries {
		if !isExitAddr(e.Addr) {
			return State{}, ClientsFile{}, ReconcileReport{},
				fmt.Errorf("fips_exit: observed %q is not a fd-prefixed IPv6 address", e.Addr)
		}
		if seen[e.Addr] {
			return State{}, ClientsFile{}, ReconcileReport{},
				fmt.Errorf("fips_exit: observed %q twice", e.Addr)
		}
		seen[e.Addr] = true
	}

	var report ReconcileReport
	next := State{Revision: state.Revision}
	entitlementsChanged := false
	live := make(map[netip.Addr]Entitlement, len(state.Entitlements))
	revoked := make(map[netip.Addr]bool)
	for _, ent := range state.sortedEntitlements() {
		verdict := VerdictFor(state, now, ent.Key, ent.BytesUsed)
		if verdict == RevokeExpired || verdict == RevokeExhausted {
			entitlementsChanged = true
			addr := ent.Key.OuterAddress()
			revoked[addr] = true
			report.Revoked = append(report.Revoked, addr)
			continue
		}
		ent.Revision = state.Revision
		next.Entitlements = append(next.Entitlements, ent)
		live[ent.Key.OuterAddress()] = ent
	}

	var wanted []ClientsEntry
	for _, e := range observed.Entries {
		if revoked[e.Addr] {
			// Ours, and no longer entitled: drop the line rather than preserve it.
			continue
		}
		ent, managed := live[e.Addr]
		if !managed {
			// Not ours: preserve the line and say a grant is needed.
			report.NeedsGrant = append(report.NeedsGrant, e)
			next.Unmanaged = append(next.Unmanaged, e)
			wanted = append(wanted, e)
			continue
		}
		if e.Name != "" && e.Name != ent.Name {
			report.Conflicts = append(report.Conflicts, Conflict{
				Addr: e.Addr, ObservedName: e.Name, DurableName: ent.Name,
			})
		}
		wanted = append(wanted, ClientsEntry{Addr: e.Addr, Name: ent.Name})
		delete(live, e.Addr)
	}
	for _, ent := range live {
		wanted = append(wanted, ClientsEntry{Addr: ent.Key.OuterAddress(), Name: ent.Name})
	}

	sort.Slice(report.Revoked, func(i, j int) bool { return report.Revoked[i].Compare(report.Revoked[j]) < 0 })
	file := ClientsFile{Entries: orderEntries(observed.Entries, wanted)}
	if entitlementsChanged {
		next.Revision++
		for i := range next.Entitlements {
			next.Entitlements[i].Revision = next.Revision
		}
	}
	next.Snapshot = append([]ClientsEntry(nil), file.Entries...)
	return next, file, report, nil
}

// An outer address cannot be inverted into a peer key, and this layer never
// pretends otherwise: that is why an unmanaged clients-file line can only be
// reported as needing a grant, never adopted.
