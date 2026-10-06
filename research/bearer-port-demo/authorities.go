package bearerport

import (
	"fmt"
	"sort"
	"sync"
)

// Output is a signed unit of value in some derivation space. A swap produces
// one or more of them and consumes the instruments that funded them.
type Output struct {
	Commitment string
	Amount     uint64
}

// Authority is the thing on the other end of the wire. Cashu calls it a mint,
// fedimint calls it a federation, a mining pool calls it a pool. The port calls
// it an authority and requires exactly these capabilities — no more, so a new
// instrument kind is an adapter, not a port change.
//
// The one thing EVERY authority in this interface must supply is memory:
// memory of what it signed, and memory of what it consumed. That memory is
// where both NUT-07 and NUT-09 come from. An "authority" with no memory is not
// an authority; it is a signed sheet of paper, and the port cannot use it.
type Authority interface {
	// Name is the namespaced target key ("cashu:<url>", "fedi:<id>",
	// "pow:<miner>"). Distinct names never collide.
	Name() string
	// Kind is the encoding family this authority mints.
	Kind() string
	// Unit is the denomination it issues in.
	Unit() string
	// Capability declares how it can answer the state question and how its
	// recovery memory is shaped. Declared, not discovered at runtime.
	Capability() Capability

	// Issue signs one output into a holder's derivation space. This is the
	// mint side of a mint/receive.
	//
	// The authority learns the COMMITMENT here, not the nullifier: at issuance
	// it sees a blinded message, not the secret. Whether it also records the
	// nullifier is the deliberate extension that separates a sound NUT-07
	// (StateCheck=query) from an unsound one.
	Issue(seed []byte, counter, amount uint64) []byte

	// Reveal records a nullifier the holder disclosed. Redemption is the only
	// moment a secret is revealed, so this is the only sound way an authority
	// learns Y — and an authority that never learns Y cannot be asked about
	// spentness at all (it can only ever answer UNKNOWN).
	Reveal(commitment, nullifier string)

	// Issued is one lookup in the authority's issuance log. It is the
	// primitive recovery is built from: a holder re-derives its children and
	// asks about each one. In Cashu's terms, this is the mint matching a
	// blinded message it has seen before.
	Issued(commitment string) (Issuance, bool, error)

	// Swap is the atomic consume-and-reissue, and it is N:M because that is
	// what a real swap is: Cashu NUT-03 takes N inputs and signs M outputs,
	// balanced in value. Receiving a bearer instrument necessarily means
	// re-issuing it — the incoming instrument is destroyed by being spent.
	//
	// Atomicity is the load-bearing property: either every input is marked
	// spent AND every replacement is signed, or none of it happened. The
	// returned amount is the authority's own record for the consumed inputs,
	// never the blobs' claims.
	Swap(inputs []Instrument, replacements []Output) (uint64, error)

	// CheckStates answers the NUT-07 question for a set of nullifiers.
	//   - SPENT   — the authority consumed it;
	//   - UNSPENT — the authority learned it and has not consumed it;
	//   - UNKNOWN — the authority cannot attest. Never UNSPENT by default.
	CheckStates(nullifiers []string) (map[string]State, error)

	// SetOffline simulates an unreachable authority, so the port's fail-closed
	// behaviour is testable.
	SetOffline(bool)
}

// Issuance is one entry in an authority's issuance log.
//
// Nullifier is the holder's anti-double-spend tag (Y), recorded ONLY once the
// holder disclosed it. That single fact is the tension NUT-07 turns on:
//
//   - at issuance a Cashu mint sees a blinded message, so it knows the
//     commitment but not the secret, hence not Y;
//   - at redemption the holder reveals the secret, so the mint learns Y then;
//   - a mint that records Y at issuance is running a deliberate extension —
//     which is precisely what a sound NUT-07 needs, and precisely what makes
//     the check meaningful *before* the money moves rather than after.
type Issuance struct {
	Nullifier string
	Amount    uint64
	Spent     bool
}

// ---------------------------------------------------------------------------
// Shared base: the issuance log, the spend set, and the swap mechanics that are
// identical for every authority. A concrete authority supplies only its name,
// kind, unit, capability, and whether it learns nullifiers at issuance.
// ---------------------------------------------------------------------------

type baseAuthority struct {
	name string
	kind string
	unit string
	cap  Capability

	// learnNullifiersAtIssuance is the first honest knob. True means the
	// authority records Y when it signs (a deliberate extension — the mint has
	// to be handed the secret, or derive it from a non-blinded output). False
	// means the plain Cashu behaviour: it sees only a blinded message, so it
	// learns Y at redemption and not one moment earlier.
	learnNullifiersAtIssuance bool

	// optimisticUnspent is the second honest knob, and it is the sharp one.
	// True reproduces a RAW Cashu /checkstate: the mint answers SPENT if Y is
	// in its spent set and UNSPENT otherwise — including for a Y it has never
	// seen in its life. Such a mint cannot tell "I signed this and it is
	// unspent" from "I have never heard of this", so its UNSPENT means "not
	// consumed", NOT "exists". That distinction is the whole reason the port
	// separates Decode (existence, answered from the issuance log) from
	// CheckState (consumption, answered from the spend set).
	optimisticUnspent bool

	offline bool
	mu      sync.Mutex
	issued  map[string]Issuance // commitment -> issuance record
	spent   map[string]bool     // commitment -> consumed
}

func (b *baseAuthority) Name() string           { return b.name }
func (b *baseAuthority) Kind() string           { return b.kind }
func (b *baseAuthority) Unit() string           { return b.unit }
func (b *baseAuthority) Capability() Capability { return b.cap }
func (b *baseAuthority) SetOffline(v bool) {
	b.mu.Lock()
	b.offline = v
	b.mu.Unlock()
}

func (b *baseAuthority) Issue(seed []byte, counter, amount uint64) []byte {
	commitment := CommitmentOf(seed, counter, b.name)
	b.mu.Lock()
	rec := Issuance{Amount: amount}
	if b.learnNullifiersAtIssuance {
		rec.Nullifier = NullifierOf(seed, counter, b.name)
	}
	b.issued[commitment] = rec
	b.spent[commitment] = false
	b.mu.Unlock()
	return encodeBlob(b.kind, commitment, amount, 0)
}

func (b *baseAuthority) Reveal(commitment, nullifier string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.issued[commitment]
	if !ok {
		return
	}
	rec.Nullifier = nullifier
	b.issued[commitment] = rec
}

func (b *baseAuthority) Issued(commitment string) (Issuance, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		return Issuance{}, false, fmt.Errorf("%w: %s unreachable", ErrStateUnavailable, b.name)
	}
	rec, ok := b.issued[commitment]
	if !ok {
		return Issuance{}, false, nil
	}
	rec.Spent = b.spent[commitment]
	return rec, true, nil
}

// Swap consumes every input and signs every replacement under one lock.
//
// The order inside the critical section is the whole point:
//  1. read each input's amount from the authority's OWN log (never the blob);
//  2. refuse if any input is unknown or already spent — before mutating;
//  3. check the swap balances;
//  4. learn each revealed nullifier, mark each input spent, and record each
//     replacement.
//
// Nothing is mutated before step 4, so a refusal at step 2 or 3 leaves the
// authority byte-identical. This is the crash-consistency rule the repo's
// AGENTS.md demands of any money-touching change, expressed in one place.
func (b *baseAuthority) Swap(inputs []Instrument, replacements []Output) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		return 0, fmt.Errorf("%w: %s unreachable", ErrStateUnavailable, b.name)
	}
	if len(inputs) == 0 {
		return 0, fmt.Errorf("%w: swap needs at least one input", ErrAmountNotPositive)
	}

	var consumed uint64
	amounts := make([]uint64, len(inputs))
	for i, in := range inputs {
		if in.Kind() != b.kind {
			return 0, fmt.Errorf("%w: %s does not mint %s", ErrNotIssued, b.name, in.Kind())
		}
		rec, ok := b.issued[in.Commitment()]
		if !ok {
			return 0, fmt.Errorf("%w: unknown commitment", ErrNotIssued)
		}
		if b.spent[in.Commitment()] {
			return 0, fmt.Errorf("%w at %s", ErrAlreadyConsumed, b.name)
		}
		amounts[i] = rec.Amount
		consumed += rec.Amount
	}

	var signed uint64
	for _, r := range replacements {
		if r.Amount == 0 {
			return 0, fmt.Errorf("%w: zero-valued replacement", ErrAmountNotPositive)
		}
		signed += r.Amount
	}
	if signed != consumed {
		return 0, fmt.Errorf("%w: in %d != out %d", ErrAmountMismatch, consumed, signed)
	}

	// Point of no return: from here every write is part of one atomic step.
	for i, in := range inputs {
		_ = amounts[i]
		rec := b.issued[in.Commitment()]
		if in.Nullifier() != "" {
			rec.Nullifier = in.Nullifier()
			b.issued[in.Commitment()] = rec
		}
		b.spent[in.Commitment()] = true
	}
	for _, r := range replacements {
		b.issued[r.Commitment] = Issuance{Amount: r.Amount}
		b.spent[r.Commitment] = false
	}
	return consumed, nil
}

func (b *baseAuthority) CheckStates(nullifiers []string) (map[string]State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offline {
		return nil, fmt.Errorf("%w: %s unreachable", ErrStateUnavailable, b.name)
	}
	out := make(map[string]State, len(nullifiers))
	for _, n := range nullifiers {
		// The safe default. Anything not positively attested below stays
		// UNKNOWN — never UNSPENT.
		out[n] = StateUnknown
	}
	for commitment, rec := range b.issued {
		if rec.Nullifier == "" {
			continue // never learned: this authority cannot attest about it
		}
		if _, asked := out[rec.Nullifier]; !asked {
			continue
		}
		if b.spent[commitment] {
			out[rec.Nullifier] = StateSpent
		} else {
			out[rec.Nullifier] = StateUnspent
		}
	}
	if b.optimisticUnspent {
		// A raw mint: anything it has not recorded as spent is reported
		// UNSPENT, whether or not it ever signed it. Faithful to the real
		// behaviour, and the reason the port must never use this answer to
		// establish existence.
		for n, s := range out {
			if s == StateUnknown {
				out[n] = StateUnspent
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The three demonstration authorities. Each differs ONLY in name, kind, unit
// and the two capability fields — which is the whole argument of the demo.
// ---------------------------------------------------------------------------

// CashuAuthority behaves like a Cashu mint: a read-only token-state API
// (NUT-07 /checkstate) and a signing log that /restore replays (NUT-09).
type CashuAuthority struct{ *baseAuthority }

func NewCashuAuthority(url string) *CashuAuthority {
	name := "cashu:" + url
	return &CashuAuthority{&baseAuthority{
		name:                      name,
		kind:                      "cashuA",
		unit:                      "sat",
		learnNullifiersAtIssuance: true,
		optimisticUnspent:         false,
		issued:                    map[string]Issuance{},
		spent:                     map[string]bool{},
		cap: Capability{
			Authority:  name,
			Kind:       "cashuA",
			Unit:       "sat",
			StateCheck: CheckModeQuery,
			Recover:    RecoverModeIssuanceLog,
			Notes:      "NUT-07 /checkstate is a read; NUT-09 recovery replays the mint's signing log",
		},
	}}
}

// Nullifier exposes the derivation tag for a child (the NUT-07 handle).
func (a *CashuAuthority) Nullifier(seed []byte, counter uint64) string {
	return NullifierOf(seed, counter, a.name)
}

// NewRawCashuAuthority is a PLAIN mint, and the most instructive authority in
// the demo. It has no extension at all: it learns Y only at redemption, and it
// answers /checkstate optimistically — SPENT if Y is in the spent set, UNSPENT
// otherwise, including for a Y it has never seen.
//
// That is faithful to the real behaviour, and it is why the port must never use
// a NUT-07 answer to establish EXISTENCE. This mint's UNSPENT means "not
// consumed", not "this is mine and it is real". Decode answers existence, from
// the issuance log; CheckState answers consumption, from the spend set. Mixing
// them is how a fake note passes a "spendability" check.
func NewRawCashuAuthority(url string) *CashuAuthority {
	name := "cashu:" + url
	return &CashuAuthority{&baseAuthority{
		name:                      name,
		kind:                      "cashuA",
		unit:                      "sat",
		learnNullifiersAtIssuance: false,
		optimisticUnspent:         true,
		issued:                    map[string]Issuance{},
		spent:                     map[string]bool{},
		cap: Capability{
			Authority:  name,
			Kind:       "cashuA",
			Unit:       "sat",
			StateCheck: CheckModeQuery,
			Recover:    RecoverModeIssuanceLog,
			Notes:      "plain Cashu: /checkstate answers optimism, so UNSPENT means not-consumed, never exists",
		},
	}}
}

// MarkSpentForTest marks one of the holder's outputs consumed out of band (a
// second device, or a crash mid-swap) so recovery has something it must skip.
func (a *CashuAuthority) MarkSpentForTest(commitment string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.spent[commitment] = true
}

// FedimintAuthority behaves like a guardians' federation: there is no
// token-level liveness API at all, so NUT-07 has to be re-derived as a
// MUTATING probe (reissue), and NUT-09 comes from the federation's encrypted
// backup of the holder's note set.
type FedimintAuthority struct{ *baseAuthority }

func NewFedimintAuthority(id string) *FedimintAuthority {
	name := "fedi:" + id
	return &FedimintAuthority{&baseAuthority{
		name:                      name,
		kind:                      "fed1",
		unit:                      "sat",
		learnNullifiersAtIssuance: true,
		optimisticUnspent:         false,
		issued:                    map[string]Issuance{},
		spent:                     map[string]bool{},
		cap: Capability{
			Authority:  name,
			Kind:       "fed1",
			Unit:       "sat",
			StateCheck: CheckModeProbe,
			Recover:    RecoverModeAuthorityBackup,
			Notes:      "no token-state API: the state is learned by attempting the atomic reissue — a mutation, and it credits",
		},
	}}
}

func (a *FedimintAuthority) Nullifier(seed []byte, counter uint64) string {
	return NullifierOf(seed, counter, a.name)
}

// PowAuthority behaves like a miner's payout account. Note the capability
// difference that makes it instructive: the ledger keys on the account, not on
// the nonce, so the pool never learns a nullifier and can answer nothing about
// spentness. NUT-07 here is honestly "no answer" — which is a capability, not
// an error.
type PowAuthority struct{ *baseAuthority }

func NewPowAuthority(minerID string) *PowAuthority {
	name := "pow:" + minerID
	return &PowAuthority{&baseAuthority{
		name:                      name,
		kind:                      "pow1",
		unit:                      "work",
		learnNullifiersAtIssuance: false,
		optimisticUnspent:         false,
		issued:                    map[string]Issuance{},
		spent:                     map[string]bool{},
		cap: Capability{
			Authority:  name,
			Kind:       "pow1",
			Unit:       "work",
			StateCheck: CheckModeNone,
			Recover:    RecoverModeAccountLedger,
			Notes:      "a nonce is a bearer instrument too, but the pool's ledger keys on the account: it can restore, and it cannot answer spentness",
		},
	}}
}

func (a *PowAuthority) Nullifier(seed []byte, counter uint64) string {
	return NullifierOf(seed, counter, a.name)
}

// sortedAuthorities is a small helper the port and tests share.
func sortedAuthorities(m map[string]Authority) []Authority {
	out := make([]Authority, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
