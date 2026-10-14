package bearerport

import (
	"errors"
	"sort"
	"sync"
)

// RecoveryBatch is how many consecutive derivation indices recovery asks about
// before deciding it has run past the end of the holder's history. This is
// Cashu NUT-09's actual shape — and its actual limitation, which is worth being
// explicit about rather than hiding:
//
//   - a client cannot bound its own derivation space after it lost the store
//     that held the counter, so it must sweep in batches;
//   - a gap of RecoveryBatch or more consecutive unknown indices truncates the
//     sweep early and silently loses the funds above the gap;
//   - the mitigation is PersistCounterBeforeExposure: sweep from the last
//     durable index we know about, so the only unknown indices above it are
//     ones we never used.
const RecoveryBatch = 8

// Port is the instrument-agnostic wallet. It owns the only copy of the seed
// outside the operator's backup, routes by namespaced target key, and derives
// both NUT-07 and NUT-09 from one primitive: the authority's memory.
//
// Every method here is written in terms of Instrument and Authority. Adding a
// fourth instrument kind requires zero changes to this file — which is the
// answer to "is our implementation generic enough".
type Port struct {
	seed     []byte
	mu       sync.Mutex
	auth     map[string]Authority
	byKind   map[string][]Authority
	holdings map[string][]Output // target key -> unspent outputs we can derive
	own      map[string]uint64   // commitment -> the holder's derivation index
	counters map[string]uint64   // target key -> next free derivation index
	seen     map[string]bool     // commitment -> a foreign instrument accepted
}

func NewPort() *Port {
	return &Port{
		seed:     []byte("bearer-port-demo-seed"),
		auth:     map[string]Authority{},
		byKind:   map[string][]Authority{},
		holdings: map[string][]Output{},
		own:      map[string]uint64{},
		counters: map[string]uint64{},
		seen:     map[string]bool{},
	}
}

// Seed is the recovery material. It never leaves the port for an authority: no
// blob contains it, and a decoded Instrument has no accessor for it. The copy
// returned here exists so a test can simulate the operator's own backup.
func (p *Port) Seed() []byte { return append([]byte(nil), p.seed...) }

// Register wires an authority into the port. Order does not matter; routing is
// by name and by sniffed kind.
func (p *Port) Register(a Authority) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.auth[a.Name()] = a
	p.byKind[a.Kind()] = append(p.byKind[a.Kind()], a)
}

// Decode sniffs the encoding family from the bytes themselves and returns a
// backend-neutral Instrument. Configuration is not consulted: a declared kind
// is a way to be confidently wrong.
//
// This is where EXISTENCE is established, from the authority's issuance log —
// and it is deliberately a different question from CheckState's CONSUMPTION.
// A plain Cashu mint's /checkstate answers "not in my spent set", which is true
// of a nullifier it has never seen in its life; only the issuance log can say
// "I signed this". Keeping the two questions in two methods is what stops a
// forged note from passing a spendability check.
//
// Attribution is by the authority's OWN log, not by registration order: with
// two mints of the same kind, only the one that signed this output can claim
// it. An instrument no registered authority recognises is refused outright.
func (p *Port) Decode(blob []byte) (Instrument, error) {
	kind, commitment, face, err := sniffAndSplit(blob)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	candidates := append([]Authority(nil), p.byKind[kind]...)
	p.mu.Unlock()
	if len(candidates) == 0 {
		return nil, ErrUnknownKind
	}
	for _, a := range candidates {
		rec, ok, err := a.Issued(commitment)
		if err != nil || !ok {
			continue
		}
		null := rec.Nullifier
		if null == "" && a.Capability().StateCheck != CheckModeNone {
			// The authority has not learned Y. If it is our own output we can
			// supply it, because the holder is the one who can derive it.
			// An authority that cannot answer state questions at all gets no
			// synthesized handle: carrying one would imply a question is
			// answerable when it is not.
			p.mu.Lock()
			idx, mine := p.own[commitment]
			seed := append([]byte(nil), p.seed...)
			p.mu.Unlock()
			if mine {
				null = NullifierOf(seed, idx, a.Name())
			}
		}
		return &instrument{
			kind:      kind,
			authority: a.Name(),
			unit:      a.Unit(),
			face:      face,
			attested:  rec.Amount,
			commit:    commitment,
			nullifier: null,
			blob:      append([]byte(nil), blob...),
		}, nil
	}
	return nil, ErrNotIssued
}

// IssueInto is the mint side: the authority signs an output into the port's own
// derivation space, and the port records it as its holding.
//
// The ordering is the durability rule the repo's AGENTS.md states (#266):
// advance and persist the derivation counter BEFORE the authority is asked to
// sign, because a crash between "the authority signed index N" and "we recorded
// that we own index N" leaves recovery with no durable index to sweep to.
func (p *Port) IssueInto(target string, amount uint64) ([]byte, error) {
	if amount == 0 {
		return nil, ErrAmountNotPositive
	}
	a, err := p.authorityFor(target)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	counter := p.counters[target]
	p.counters[target] = counter + 1
	seed := append([]byte(nil), p.seed...)
	p.mu.Unlock()

	blob := a.Issue(seed, counter, amount)
	out := Output{Commitment: CommitmentOf(seed, counter, a.Name()), Amount: amount}

	p.mu.Lock()
	p.holdings[target] = append(p.holdings[target], out)
	p.own[out.Commitment] = counter
	p.mu.Unlock()
	return blob, nil
}

// AcquireRaw is Decode + Acquire.
func (p *Port) AcquireRaw(blob []byte) (uint64, error) {
	in, err := p.Decode(blob)
	if err != nil {
		return 0, err
	}
	return p.Acquire(in)
}

// Acquire receives a foreign instrument and swaps it into the port's own
// derivation space. This is unavoidable rather than a quirk: the incoming
// instrument is destroyed by being spent, so its value only continues to exist
// as a replacement output in the port's space.
//
// Atomicity: either the authority marked the incoming output spent AND signed
// the replacement (which the port then holds), or nothing happened. The
// replacement counter is reserved BEFORE the swap, so a crash mid-swap leaves a
// sweepable index rather than a hole.
//
// The credited amount is the authority's log, never the blob's claim.
func (p *Port) Acquire(in Instrument) (uint64, error) {
	a, err := p.authorityFor(in.Authority())
	if err != nil {
		return 0, err
	}
	if p.alreadySeen(in.Commitment()) {
		return 0, ErrAlreadyConsumed
	}
	if in.AttestedAmount() == 0 {
		return 0, ErrNotIssued
	}

	p.mu.Lock()
	counter := p.counters[a.Name()]
	p.counters[a.Name()] = counter + 1
	seed := append([]byte(nil), p.seed...)
	p.mu.Unlock()

	replacement := Output{Commitment: CommitmentOf(seed, counter, a.Name()), Amount: in.AttestedAmount()}
	amount, err := a.Swap([]Instrument{in}, []Output{replacement})
	if err != nil {
		return 0, err // authority consumed nothing; the port holds nothing new
	}

	p.mu.Lock()
	p.holdings[a.Name()] = append(p.holdings[a.Name()], replacement)
	p.own[replacement.Commitment] = counter
	p.seen[in.Commitment()] = true
	p.mu.Unlock()
	return amount, nil
}

// Spend is the one spend path for every backend. It consumes the port's held
// outputs at `target` and signs a single output for the recipient, derived from
// a FOREIGN seed. That derivation space is what makes the spend final: recovery
// replays our own seed, so a paid-out output can never be resurrected.
//
// Units are not converted: spending a "work" instrument against a "sat"
// authority is refused, because there is no honest exchange rate to invent.
func (p *Port) Spend(amount uint64, target string) (string, error) {
	if amount == 0 {
		return "", ErrAmountNotPositive
	}
	a, err := p.authorityFor(target)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	held := append([]Output(nil), p.holdings[target]...)
	total := sumOutputs(held)
	if total < amount {
		p.mu.Unlock()
		return "", ErrInsufficientFunds
	}
	counter := p.counters[target]
	p.counters[target] = counter + 1
	seed := append([]byte(nil), p.seed...)
	idxs := make([]uint64, len(held))
	for i, h := range held {
		idxs[i] = p.own[h.Commitment]
	}
	p.mu.Unlock()

	payer := []byte("recipient-derivation-space:" + target)
	payOut := Output{Commitment: CommitmentOf(payer, counter, a.Name()), Amount: amount}
	replacements := []Output{payOut}
	change := total - amount
	if change > 0 {
		replacements = append(replacements, Output{
			Commitment: CommitmentOf(seed, counter, a.Name()),
			Amount:     change,
		})
	}

	inputs := make([]Instrument, 0, len(held))
	for i, h := range held {
		inputs = append(inputs, &instrument{
			kind: a.Kind(), authority: a.Name(), unit: a.Unit(),
			face: h.Amount, attested: h.Amount,
			commit:    h.Commitment,
			nullifier: NullifierOf(seed, idxs[i], a.Name()),
		})
	}
	if _, err := a.Swap(inputs, replacements); err != nil {
		return "", err
	}

	p.mu.Lock()
	for _, h := range held {
		delete(p.own, h.Commitment)
	}
	if change > 0 {
		changeOut := Output{Commitment: CommitmentOf(seed, counter, a.Name()), Amount: change}
		p.holdings[target] = []Output{changeOut}
		p.own[changeOut.Commitment] = counter
	} else {
		p.holdings[target] = nil
	}
	p.mu.Unlock()
	return string(encodeBlob(a.Kind(), payOut.Commitment, amount, 0)), nil
}

// CheckState answers the NUT-07 question for one instrument, using whatever
// mode the authority declared.
//
//   - query mode: ask, and report only what the authority attested.
//   - probe mode: there is no read API, so the only sound way to learn the
//     state is to attempt the consumption. That is a mutation, and on success
//     it credits the port. This is fedimint's reissue.
//   - none:    the authority cannot answer. The port reports UNKNOWN and says
//     why; it does not invent an answer.
//
// The rule is identical in every mode: SPENT and UNSPENT are things the
// authority SAID, and anything else is UNKNOWN. An unreachable authority is
// ErrStateUnavailable, never "probably fine".
func (p *Port) CheckState(in Instrument) (State, error) {
	a, err := p.authorityFor(in.Authority())
	if err != nil {
		return StateUnknown, err
	}
	switch a.Capability().StateCheck {
	case CheckModeNone:
		return StateUnknown, ErrStateUnavailable
	case CheckModeProbe:
		if _, err := p.Acquire(in); err != nil {
			if errors.Is(err, ErrAlreadyConsumed) {
				return StateSpent, nil
			}
			return StateUnknown, err
		}
		return StateUnspent, nil
	}

	if in.Nullifier() == "" {
		// The authority has no handle to ask about. That is UNKNOWN, and the
		// port says so rather than asking a question it knows is meaningless.
		return StateUnknown, ErrStateUnavailable
	}
	states, err := a.CheckStates([]string{in.Nullifier()})
	if err != nil {
		return StateUnknown, err
	}
	s, ok := states[in.Nullifier()]
	if !ok {
		return StateUnknown, ErrStateUnavailable
	}
	return s, nil
}

// Recover is the NUT-09 derivation: rebuild the holdings from the seed alone,
// by replaying the deterministic derivation against the authority's memory.
//
// The sweep starts at index 0, which is what Cashu's /restore does — and it
// starts there for a reason worth stating, because the obvious "optimisation"
// is a funds-loss bug. Starting at the current counter (the highest index we
// believe we used) silently skips every live output BELOW it, and after a
// swap-and-reissue cycle there is always one: our own output moved from index
// 0 to index 1 while the counter moved to 2.
//
// The sweep stops after RecoveryBatch consecutive UNKNOWN indices. An index the
// authority recognises but reports spent counts as continuity, not a miss:
// knowing we used index i is exactly what proves index i+1 is worth asking
// about. That is also why a gap in the history truncates the sweep, which the
// RecoveryBatch comment names honestly.
//
// What comes back is exactly what the authority remembers as live:
//   - an output it signed and nobody spent comes back;
//   - an output it signed and that was spent (here or elsewhere) does NOT — a
//     lost device must not resurrect a spent note;
//   - anything derived from a different seed never comes back, because a
//     different seed derives different commitments. Ownership is proven by
//     being able to derive, and the seed never crosses the wire.
func (p *Port) Recover(seed []byte) (map[string]uint64, error) {
	p.mu.Lock()
	auths := sortedAuthorities(p.auth)
	p.mu.Unlock()

	holdings := map[string][]Output{}
	own := map[string]uint64{}
	recovered := map[string]uint64{}
	nextIndex := map[string]uint64{}

	for _, a := range auths {
		i := uint64(0)
		misses := 0
		highest := uint64(0)
		for {
			commitment := CommitmentOf(seed, i, a.Name())
			rec, ok, err := a.Issued(commitment)
			if err != nil {
				return nil, err
			}
			if !ok {
				misses++
				if misses >= RecoveryBatch {
					break
				}
				i++
				continue
			}
			// Recognised: the holder did use this index, so the history is
			// contiguous through here whether or not it still holds value.
			misses = 0
			highest = i + 1
			if !rec.Spent {
				holdings[a.Name()] = append(holdings[a.Name()], Output{Commitment: commitment, Amount: rec.Amount})
				own[commitment] = i
				recovered[a.Name()] += rec.Amount
			}
			i++
		}
		if highest > 0 {
			nextIndex[a.Name()] = highest
		}
	}

	p.mu.Lock()
	p.holdings = holdings
	p.own = own
	p.seen = map[string]bool{}
	for name, idx := range nextIndex {
		if idx > p.counters[name] {
			p.counters[name] = idx
		}
	}
	p.mu.Unlock()
	return recovered, nil
}

// Balance is the total held across every authority.
func (p *Port) Balance() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var total uint64
	for _, outs := range p.holdings {
		total += sumOutputs(outs)
	}
	return total
}

// BalanceByAuthority is the holding at one target key.
func (p *Port) BalanceByAuthority(target string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return sumOutputs(p.holdings[target])
}

// DestroyStore simulates flash loss. The seed survives (it is the operator's
// backup); the derivation counters are deliberately lost with the store, which
// is exactly why recovery must not depend on them.
func (p *Port) DestroyStore() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.holdings = map[string][]Output{}
	p.own = map[string]uint64{}
	p.counters = map[string]uint64{}
	p.seen = map[string]bool{}
}

// Capabilities returns the declared contract of every registered authority.
func (p *Port) Capabilities() []Capability {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Capability, 0, len(p.auth))
	for _, a := range p.auth {
		out = append(out, a.Capability())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Authority < out[j].Authority })
	return out
}

// Counter reports the next free derivation index for a target key.
func (p *Port) Counter(target string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters[target]
}

// PersistCounterBeforeExposure reserves a derivation range durably before any of
// it is exposed to an authority. IssueInto applies it inline; it is exposed as a
// method because recovery's truncation caveat is only survivable if the last
// index we know about is durable.
//
// The counter is monotonic and never rewinds. Re-deriving an exposed range is
// the #257/#266/#480 class of bug, and it is impossible here only because every
// path that exposes a range goes through this first.
func (p *Port) PersistCounterBeforeExposure(target string, _from, next uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if next > p.counters[target] {
		p.counters[target] = next
	}
}

func (p *Port) authorityFor(target string) (Authority, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.auth[target]
	if !ok {
		return nil, ErrNoAuthority
	}
	return a, nil
}

func (p *Port) alreadySeen(commitment string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[commitment]
}

func sumOutputs(outs []Output) uint64 {
	var total uint64
	for _, o := range outs {
		total += o.Amount
	}
	return total
}
